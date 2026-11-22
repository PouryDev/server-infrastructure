package orm

import "fmt"

// Relations in queries come in two flavours:
//
//   - Has / NotHas conditions ("rows that have a related row matching ..."), compiled to EXISTS. They never
//     multiply rows, work through every relation kind, and are the right tool for filtering by related data.
//   - Join / LeftJoin, which bring a relation's columns into the query so WHERE and ORDER BY can use them
//     ("Author.name"). Joining a to-many relation (has_many, many_to_many) repeats the main row once per
//     related row; add Distinct() or prefer Has.
//
// For anything else (arbitrary joins, CTEs, window functions) use Fetch with SQL, which scans any result
// into a struct by column name.

// Has matches rows that have at least one related row satisfying conds (no conds: any related row).
// Conditions refer to the columns of the related model.
func Has(relation string, conds ...Cond) Cond { return hasCond(relation, false, conds) }

// NotHas matches rows without any related row satisfying conds.
func NotHas(relation string, conds ...Cond) Cond { return hasCond(relation, true, conds) }

func hasCond(relation string, negate bool, conds []Cond) Cond {
	return cond(func(b *builder) {
		rel, ok := b.m.Relation(relation)
		if !ok {
			b.fail("orm: %s has no relation %q", b.m.Type.Name(), relation)
			return
		}
		target, err := Describe(rel.Target)
		if err != nil {
			b.fail("%v", err)
			return
		}
		alias := b.newAlias()
		inner := b.child(target, alias)

		var from, correlate string
		switch rel.Kind {
		case BelongsTo:
			fk, ok1 := b.m.Field(rel.FK)
			ref, err := refField(target, rel.Ref)
			if !ok1 || err != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", b.m.Type.Name(), rel.Name)
				return
			}
			from = target.quotedTable + " AS " + alias
			correlate = alias + "." + ref.quoted + " = " + b.qual + "." + fk.quoted
		case HasOne, HasMany:
			ref, err := refField(b.m, rel.Ref)
			fk, ok1 := target.Field(rel.FK)
			if !ok1 || err != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", b.m.Type.Name(), rel.Name)
				return
			}
			from = target.quotedTable + " AS " + alias
			correlate = alias + "." + fk.quoted + " = " + b.qual + "." + ref.quoted
		case ManyToMany:
			ref, err := refField(b.m, rel.Ref)
			tpk, err2 := refField(target, "")
			if err != nil || err2 != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", b.m.Type.Name(), rel.Name)
				return
			}
			jalias := alias[:len(alias)-1] + `j"`
			from = quote(rel.JoinTable) + " AS " + jalias + " JOIN " + target.quotedTable + " AS " + alias +
				" ON " + alias + "." + tpk.quoted + " = " + jalias + "." + quote(rel.JoinRef)
			correlate = jalias + "." + quote(rel.JoinFK) + " = " + b.qual + "." + ref.quoted
		}

		b.sb.WriteString(map[bool]string{false: "EXISTS", true: "NOT EXISTS"}[negate] + " (SELECT 1 FROM " + from + " WHERE " + correlate)
		if target.SoftDelete != nil {
			b.sb.WriteString(" AND " + alias + "." + target.SoftDelete.quoted + " IS NULL")
		}
		for _, c := range conds {
			inner.sb.Reset()
			c.write(inner)
			b.sb.WriteString(" AND " + inner.sb.String())
		}
		b.sb.WriteString(")")
	})
}

func refField(m *Model, ref string) (*Field, error) { return refColumn(m, ref) }

type joinSpec struct {
	relation string
	left     bool
}

// Join adds an INNER JOIN of a relation so conditions and ordering can use "Relation.column". See the
// note at the top of this file about to-many relations.
func (q *Query[T]) Join(relation string) *Query[T] {
	q.joins = append(q.joins, joinSpec{relation, false})
	return q
}

// LeftJoin is Join keeping main rows without a related row (their related columns are NULL).
func (q *Query[T]) LeftJoin(relation string) *Query[T] {
	q.joins = append(q.joins, joinSpec{relation, true})
	return q
}

// Distinct removes duplicate main rows (needed after joining a to-many relation).
func (q *Query[T]) Distinct() *Query[T] { q.distinct = true; return q }

// toMany reports whether a join can repeat main rows.
func (q *Query[T]) toManyJoin() bool {
	for _, j := range q.joins {
		if rel, ok := q.r.m.Relation(j.relation); ok && (rel.Kind == HasMany || rel.Kind == ManyToMany) {
			return true
		}
	}
	return false
}

// writeJoins renders the JOIN clauses and registers the aliases on the root builder.
func (q *Query[T]) writeJoins(b *builder) {
	m := q.r.m
	if b.joins == nil {
		b.joins = map[string]joined{}
	}
	for _, j := range q.joins {
		rel, ok := m.Relation(j.relation)
		if !ok {
			b.fail("orm: %s has no relation %q", m.Type.Name(), j.relation)
			return
		}
		target, err := Describe(rel.Target)
		if err != nil {
			b.fail("%v", err)
			return
		}
		alias := quote("j_" + snake(rel.Name))
		b.joins[rel.Name] = joined{alias: alias, target: target}
		kind := "JOIN"
		if j.left {
			kind = "LEFT JOIN"
		}
		live := ""
		if target.SoftDelete != nil {
			live = " AND " + alias + "." + target.SoftDelete.quoted + " IS NULL"
		}
		switch rel.Kind {
		case BelongsTo:
			fk, ok1 := m.Field(rel.FK)
			ref, err := refColumn(target, rel.Ref)
			if !ok1 || err != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", m.Type.Name(), rel.Name)
				return
			}
			fmt.Fprintf(&b.sb, " %s %s AS %s ON %s.%s = %s.%s%s", kind, target.quotedTable, alias, alias, ref.quoted, b.qual, fk.quoted, live)
		case HasOne, HasMany:
			ref, err := refColumn(m, rel.Ref)
			fk, ok1 := target.Field(rel.FK)
			if !ok1 || err != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", m.Type.Name(), rel.Name)
				return
			}
			fmt.Fprintf(&b.sb, " %s %s AS %s ON %s.%s = %s.%s%s", kind, target.quotedTable, alias, alias, fk.quoted, b.qual, ref.quoted, live)
		case ManyToMany:
			ref, err := refColumn(m, rel.Ref)
			tpk, err2 := refColumn(target, "")
			if err != nil || err2 != nil {
				b.fail("orm: relation %s.%s: cannot resolve the key columns", m.Type.Name(), rel.Name)
				return
			}
			ja := quote("j_" + snake(rel.Name) + "_link")
			fmt.Fprintf(&b.sb, " %s %s AS %s ON %s.%s = %s.%s", kind, quote(rel.JoinTable), ja, ja, quote(rel.JoinFK), b.qual, ref.quoted)
			fmt.Fprintf(&b.sb, " %s %s AS %s ON %s.%s = %s.%s%s", kind, target.quotedTable, alias, alias, tpk.quoted, ja, quote(rel.JoinRef), live)
		}
	}
}
