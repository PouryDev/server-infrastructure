package schema

import (
	"fmt"
	"slices"
	"strings"
)

// Options tune Diff.
type Options struct {
	// AllowDrop lets the plan drop tables, columns, indexes and constraints that exist in the database
	// but not in the models. Without it they are reported as warnings and left alone.
	AllowDrop bool
	// ConcurrentIndexes creates indexes on tables that already exist with CREATE INDEX CONCURRENTLY, which
	// does not block writes. Such statements cannot run inside a transaction, so they go to a separate
	// migration file (see Plan.RenderConcurrent) marked "-- +goose NO TRANSACTION".
	ConcurrentIndexes bool
}

// Step is one reversible change.
type Step struct {
	Up, Down string
	// Risk explains why this step can fail or lose data on a populated table; it is written into the
	// migration file as a comment.
	Risk string
	// Concurrent marks a CREATE INDEX CONCURRENTLY step (goes to the no-transaction file).
	Concurrent bool
}

// Plan is the result of Diff.
type Plan struct {
	Steps    []Step
	Warnings []string
}

// Empty reports whether the database already matches the models.
func (p *Plan) Empty() bool { return len(p.Steps) == 0 }

// Up returns the statements in application order.
func (p *Plan) Up() []string {
	out := make([]string, len(p.Steps))
	for i, s := range p.Steps {
		out[i] = s.Up
	}
	return out
}

// Down returns the inverse statements in reverse order.
func (p *Plan) Down() []string {
	out := make([]string, 0, len(p.Steps))
	for i := len(p.Steps) - 1; i >= 0; i-- {
		out = append(out, p.Steps[i].Down)
	}
	return out
}

// Diff computes the steps that turn current into desired.
func Diff(current, desired *Schema, opts Options) *Plan {
	p := &Plan{}
	// Phases keep dependencies valid: tables and columns first, then constraints (foreign keys last so
	// creation order and cycles never matter), then indexes.
	var tables, columns, constraints, indexes, drops []Step

	// Table renames are resolved first: foreign keys elsewhere must already point at the new name when
	// those tables are compared, whatever order the tables are visited in.
	renamedTables := map[string]bool{} // old names that are being renamed
	renamedFrom := map[string]string{} // new name -> old name
	for _, want := range desired.sortedTables() {
		if _, ok := current.Tables[want.Name]; ok || want.Was == "" {
			continue
		}
		if _, ok := current.Tables[want.Was]; ok {
			renamedFrom[want.Name], renamedTables[want.Was] = want.Was, true
			for _, t := range current.Tables { // the database moves foreign keys along with the table
				for _, fk := range t.FKs {
					if fk.RefTable == want.Was {
						fk.RefTable = want.Name
					}
				}
			}
		}
	}
	for _, want := range desired.sortedTables() {
		have, exists := current.Tables[want.Name]
		if old, ok := renamedFrom[want.Name]; ok && !exists {
			tables = append(tables, Step{
				Up:   fmt.Sprintf("ALTER TABLE %s RENAME TO %s", quoteIdent(old), quoteIdent(want.Name)),
				Down: fmt.Sprintf("ALTER TABLE %s RENAME TO %s", quoteIdent(want.Name), quoteIdent(old)),
			})
			have, exists = current.Tables[old], true
		}
		if !exists {
			tables = append(tables, Step{Up: createTableSQL(want), Down: "DROP TABLE " + quoteIdent(want.Name)})
			for _, fk := range want.FKs {
				constraints = append(constraints, addFKStep(want.Name, fk))
			}
			for _, ck := range want.Checks {
				constraints = append(constraints, addCheckStep(want.Name, ck))
			}
			for _, ix := range want.Indexes {
				indexes = append(indexes, createIndexStep(want.Name, ix, false)) // a new table is empty: no need to go concurrent
			}
			continue
		}
		diffTable(p, have, want, opts, &columns, &constraints, &indexes, &drops)
	}

	if opts.AllowDrop {
		for _, have := range current.sortedTables() {
			if _, kept := desired.Tables[have.Name]; !kept && !renamedTables[have.Name] {
				// Foreign keys of the table go first (they may point at other dropped tables), and the
				// Down recreates everything from the live definition (the data is gone).
				var downs []string
				downs = append(downs, createTableSQL(have))
				for _, fk := range have.FKs {
					downs = append(downs, addFKStep(have.Name, fk).Up)
					drops = append(drops, Step{Up: dropFKSQL(have.Name, fk.Name), Down: ""})
				}
				drops = append(drops, Step{Up: "DROP TABLE " + quoteIdent(have.Name), Down: strings.Join(downs, ";\n"),
					Risk: "drops table " + have.Name + " and all its data"})
			}
		}
	} else {
		for _, have := range current.sortedTables() {
			if _, kept := desired.Tables[have.Name]; !kept && !renamedTables[have.Name] {
				p.Warnings = append(p.Warnings, fmt.Sprintf("table %q exists in the database but not in the models (not dropped; use AllowDrop)", have.Name))
			}
		}
	}

	p.Steps = slices.Concat(tables, columns, constraints, indexes, drops)
	return p
}

func diffTable(p *Plan, have, want *Table, opts Options, columns, constraints, indexes, drops *[]Step) {
	t := quoteIdent(want.Name)

	renamed := map[string]bool{}
	for _, wc := range want.Columns {
		hc := have.column(wc.Name)
		if hc == nil && wc.Was != "" {
			if old := have.column(wc.Was); old != nil {
				*columns = append(*columns, Step{
					Up:   fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", t, quoteIdent(wc.Was), quoteIdent(wc.Name)),
					Down: fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s", t, quoteIdent(wc.Name), quoteIdent(wc.Was)),
				})
				renamed[wc.Was] = true
				renameColumnRefs(have, wc.Was, wc.Name) // constraints and indexes follow the column
				hc = old
			}
		}
		if hc == nil {
			step := Step{Up: "ALTER TABLE " + t + " ADD COLUMN " + columnSQL(wc), Down: "ALTER TABLE " + t + " DROP COLUMN " + quoteIdent(wc.Name)}
			if wc.NotNull && wc.Default == "" && !wc.Identity {
				step.Risk = "adds NOT NULL column " + wc.Name + " without a default: fails if the table already has rows (add a default, or add it nullable, backfill, then SET NOT NULL)"
			}
			*columns = append(*columns, step)
			continue
		}
		col := quoteIdent(wc.Name)
		if wc.Type != hc.Type {
			*columns = append(*columns, Step{
				Up:   fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::%s", t, col, wc.Type, col, wc.Type),
				Down: fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s TYPE %s USING %s::%s", t, col, hc.Type, col, hc.Type),
				Risk: fmt.Sprintf("changes the type of %s.%s from %s to %s: rewrites the table and fails when values do not convert", want.Name, wc.Name, hc.Type, wc.Type),
			})
		}
		if wc.NotNull != hc.NotNull {
			up, down := "SET NOT NULL", "DROP NOT NULL"
			risk := "SET NOT NULL fails when the column contains NULL"
			if !wc.NotNull {
				up, down, risk = down, up, ""
			}
			*columns = append(*columns, Step{Up: fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s", t, col, up),
				Down: fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s %s", t, col, down), Risk: risk})
		}
		if !wc.Identity && !hc.Identity && NormalizeDefault(wc.Default) != NormalizeDefault(hc.Default) {
			step := Step{}
			if wc.Default == "" {
				step.Up = fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT", t, col)
			} else {
				step.Up = fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s", t, col, wc.Default)
			}
			if hc.Default == "" {
				step.Down = fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s DROP DEFAULT", t, col)
			} else {
				step.Down = fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET DEFAULT %s", t, col, hc.Default)
			}
			*columns = append(*columns, step)
		}
		if wc.Identity != hc.Identity {
			p.Warnings = append(p.Warnings, fmt.Sprintf("%s.%s: identity differs between model and database (not changed automatically)", want.Name, wc.Name))
		}
	}
	for _, hc := range have.Columns {
		if want.column(hc.Name) != nil || renamed[hc.Name] {
			continue
		}
		if !opts.AllowDrop {
			p.Warnings = append(p.Warnings, fmt.Sprintf("column %s.%s exists in the database but not in the model (not dropped; use AllowDrop)", want.Name, hc.Name))
			continue
		}
		*drops = append(*drops, Step{Up: "ALTER TABLE " + t + " DROP COLUMN " + quoteIdent(hc.Name),
			Down: "ALTER TABLE " + t + " ADD COLUMN " + columnSQL(hc), Risk: "drops column " + want.Name + "." + hc.Name + " and its data"})
	}

	if !slices.Equal(want.PK, have.PK) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s: primary key differs (model %v, database %v); change it by hand", want.Name, want.PK, have.PK))
	}

	for _, wu := range want.Uniques {
		if !slices.ContainsFunc(have.Uniques, func(u *Unique) bool { return slices.Equal(u.Columns, wu.Columns) }) {
			*constraints = append(*constraints, Step{
				Up:   fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s)", t, quoteIdent(wu.Name), quoteList(wu.Columns)),
				Down: fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", t, quoteIdent(wu.Name)),
				Risk: "fails if existing rows already contain duplicates",
			})
		}
	}
	for _, wf := range want.FKs {
		if !slices.ContainsFunc(have.FKs, func(f *ForeignKey) bool { return sameFK(f, wf) }) {
			// A foreign key with the same columns but another rule is replaced.
			if old := slices.IndexFunc(have.FKs, func(f *ForeignKey) bool {
				return slices.Equal(f.Columns, wf.Columns) && f.RefTable == wf.RefTable
			}); old >= 0 {
				of := have.FKs[old]
				*constraints = append(*constraints, Step{Up: dropFKSQL(want.Name, of.Name), Down: addFKStep(want.Name, of).Up})
			}
			*constraints = append(*constraints, addFKStep(want.Name, wf))
		}
	}
	for _, wi := range want.Indexes {
		if !slices.ContainsFunc(have.Indexes, func(i *Index) bool { return slices.Equal(i.Columns, wi.Columns) }) {
			*indexes = append(*indexes, createIndexStep(want.Name, wi, opts.ConcurrentIndexes))
		}
	}
	for _, wc := range want.Checks {
		if !slices.ContainsFunc(have.Checks, func(c *Check) bool { return c.Name == wc.Name }) {
			step := addCheckStep(want.Name, wc)
			step.Risk = "fails if existing rows violate the check " + wc.Name
			*constraints = append(*constraints, step)
		}
	}

	// Anything the database has beyond the models.
	if opts.AllowDrop {
		for _, hu := range have.Uniques {
			if !slices.ContainsFunc(want.Uniques, func(u *Unique) bool { return slices.Equal(u.Columns, hu.Columns) }) {
				if hu.Constraint {
					*drops = append(*drops, Step{Up: fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", t, quoteIdent(hu.Name)),
						Down: fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE (%s)", t, quoteIdent(hu.Name), quoteList(hu.Columns))})
				} else {
					*drops = append(*drops, Step{Up: "DROP INDEX " + quoteIdent(hu.Name),
						Down: fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", quoteIdent(hu.Name), t, quoteList(hu.Columns))})
				}
			}
		}
		for _, hc := range have.Checks {
			if !slices.ContainsFunc(want.Checks, func(c *Check) bool { return c.Name == hc.Name }) {
				*drops = append(*drops, Step{Up: fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", t, quoteIdent(hc.Name)),
					Down: fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", t, quoteIdent(hc.Name), hc.Def)})
			}
		}
		for _, hf := range have.FKs {
			if !slices.ContainsFunc(want.FKs, func(f *ForeignKey) bool { return slices.Equal(f.Columns, hf.Columns) && f.RefTable == hf.RefTable }) {
				*drops = append(*drops, Step{Up: dropFKSQL(want.Name, hf.Name), Down: addFKStep(want.Name, hf).Up})
			}
		}
		for _, hi := range have.Indexes {
			if !slices.ContainsFunc(want.Indexes, func(i *Index) bool { return slices.Equal(i.Columns, hi.Columns) }) {
				*drops = append(*drops, Step{Up: "DROP INDEX " + quoteIdent(hi.Name),
					Down: fmt.Sprintf("CREATE INDEX %s ON %s (%s)", quoteIdent(hi.Name), t, quoteList(hi.Columns))})
			}
		}
	}
}

func sameFK(a, b *ForeignKey) bool {
	return slices.Equal(a.Columns, b.Columns) && a.RefTable == b.RefTable && slices.Equal(a.RefColumns, b.RefColumns) && a.OnDelete == b.OnDelete
}

func columnSQL(c *Column) string {
	var b strings.Builder
	b.WriteString(quoteIdent(c.Name) + " " + c.Type)
	if c.Identity {
		b.WriteString(" GENERATED BY DEFAULT AS IDENTITY")
	}
	if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	if c.Default != "" && !c.Identity {
		b.WriteString(" DEFAULT " + c.Default)
	}
	return b.String()
}

func createTableSQL(t *Table) string {
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "    "+columnSQL(c))
	}
	if len(t.PK) > 0 {
		lines = append(lines, "    PRIMARY KEY ("+quoteList(t.PK)+")")
	}
	for _, u := range t.Uniques {
		lines = append(lines, fmt.Sprintf("    CONSTRAINT %s UNIQUE (%s)", quoteIdent(u.Name), quoteList(u.Columns)))
	}
	return "CREATE TABLE " + quoteIdent(t.Name) + " (\n" + strings.Join(lines, ",\n") + "\n)"
}

func addFKStep(table string, fk *ForeignKey) Step {
	sql := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		quoteIdent(table), quoteIdent(fk.Name), quoteList(fk.Columns), quoteIdent(fk.RefTable), quoteList(fk.RefColumns))
	if fk.OnDelete != "" {
		sql += " ON DELETE " + strings.ToUpper(fk.OnDelete)
	}
	return Step{Up: sql, Down: dropFKSQL(table, fk.Name)}
}

func dropFKSQL(table, name string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", quoteIdent(table), quoteIdent(name))
}

func createIndexStep(table string, ix *Index, concurrent bool) Step {
	if concurrent {
		return Step{
			Up:         fmt.Sprintf("CREATE INDEX CONCURRENTLY IF NOT EXISTS %s ON %s (%s)", quoteIdent(ix.Name), quoteIdent(table), quoteList(ix.Columns)),
			Down:       "DROP INDEX CONCURRENTLY IF EXISTS " + quoteIdent(ix.Name),
			Concurrent: true,
		}
	}
	return Step{
		Up:   fmt.Sprintf("CREATE INDEX %s ON %s (%s)", quoteIdent(ix.Name), quoteIdent(table), quoteList(ix.Columns)),
		Down: "DROP INDEX " + quoteIdent(ix.Name),
	}
}

func addCheckStep(table string, c *Check) Step {
	return Step{
		Up:   fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s)", quoteIdent(table), quoteIdent(c.Name), c.Expr),
		Down: fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s", quoteIdent(table), quoteIdent(c.Name)),
	}
}

// renameColumnRefs updates the in-memory snapshot of a table after a column rename: PostgreSQL moves
// constraints and indexes with the column, so they must keep matching the model's new column name.
func renameColumnRefs(t *Table, from, to string) {
	fix := func(cols []string) {
		for i, c := range cols {
			if c == from {
				cols[i] = to
			}
		}
	}
	fix(t.PK)
	for _, u := range t.Uniques {
		fix(u.Columns)
	}
	for _, i := range t.Indexes {
		fix(i.Columns)
	}
	for _, f := range t.FKs {
		fix(f.Columns)
	}
}

// Render writes the transactional part of the plan as a goose migration file body (everything except
// CREATE INDEX CONCURRENTLY steps).
func (p *Plan) Render() string {
	var main []Step
	for _, s := range p.Steps {
		if !s.Concurrent {
			main = append(main, s)
		}
	}
	return render(main, p.Warnings, false)
}

// RenderConcurrent writes the CREATE INDEX CONCURRENTLY steps as a second migration marked
// "-- +goose NO TRANSACTION"; it returns "" when there are none. Apply it after the main file.
func (p *Plan) RenderConcurrent() string {
	var conc []Step
	for _, s := range p.Steps {
		if s.Concurrent {
			conc = append(conc, s)
		}
	}
	if len(conc) == 0 {
		return ""
	}
	return render(conc, nil, true)
}

// HasTransactional reports whether Render has anything to write.
func (p *Plan) HasTransactional() bool {
	for _, s := range p.Steps {
		if !s.Concurrent {
			return true
		}
	}
	return false
}

func render(steps []Step, warnings []string, noTx bool) string {
	var b strings.Builder
	if noTx {
		b.WriteString("-- +goose NO TRANSACTION\n")
	}
	for _, w := range warnings {
		b.WriteString("-- NOTE: " + w + "\n")
	}
	b.WriteString("-- +goose Up\n")
	for _, s := range steps {
		if s.Risk != "" {
			b.WriteString("-- WARNING: " + s.Risk + "\n")
		}
		b.WriteString("-- +goose StatementBegin\n" + s.Up + ";\n-- +goose StatementEnd\n")
	}
	b.WriteString("\n-- +goose Down\n")
	for i := len(steps) - 1; i >= 0; i-- {
		if steps[i].Down == "" {
			continue
		}
		b.WriteString("-- +goose StatementBegin\n" + steps[i].Down + ";\n-- +goose StatementEnd\n")
	}
	return b.String()
}
