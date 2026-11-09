package schema

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"VARCHAR(100)": "character varying(100)", "int4": "integer", "timestamptz": "timestamp with time zone",
		"text[]": "text[]", "numeric( 12, 2 )": "numeric(12,2)", "BOOL": "boolean", "int8[]": "bigint[]",
	} {
		if got := NormalizeType(in); got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"now()": "now()", "CURRENT_TIMESTAMP": "now()", "('user'::text)": "'user'", "(0)::bigint": "0",
		"'{}'::text[]": "'{}'", "gen_random_uuid()": "gen_random_uuid()", "((1 + 2))": "1 + 2",
	} {
		if got := NormalizeDefault(in); got != want {
			t.Errorf("NormalizeDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func tbl(name string, cols ...*Column) *Table {
	return &Table{Name: name, Columns: cols, PK: []string{"id"}}
}

func col(name, typ string) *Column { return &Column{Name: name, Type: typ, NotNull: true} }

func schemaOf(ts ...*Table) *Schema {
	s := newSchema()
	for _, t := range ts {
		s.Tables[t.Name] = t
	}
	return s
}

func ups(p *Plan) string { return strings.Join(p.Up(), ";\n") }

func TestRenameTableAndColumnKeepEverythingAttached(t *testing.T) {
	cur := tbl("people", col("id", "uuid"), col("mail", "text"))
	cur.Uniques = []*Unique{{Name: "people_mail_key", Columns: []string{"mail"}, Constraint: true}}
	cur.Indexes = []*Index{{Name: "idx_people_mail", Columns: []string{"mail"}}}
	other := tbl("orders", col("id", "uuid"), col("person_id", "uuid"))
	other.FKs = []*ForeignKey{{Name: "fk_orders_person_id", Columns: []string{"person_id"}, RefTable: "people", RefColumns: []string{"id"}}}

	want := tbl("users", col("id", "uuid"), &Column{Name: "email", Type: "text", NotNull: true, Was: "mail"})
	want.Was = "people"
	want.Uniques = []*Unique{{Name: "users_email_key", Columns: []string{"email"}, Constraint: true}}
	want.Indexes = []*Index{{Name: "idx_users_email", Columns: []string{"email"}}}
	wantOrders := tbl("orders", col("id", "uuid"), col("person_id", "uuid"))
	wantOrders.FKs = []*ForeignKey{{Name: "fk_orders_person_id", Columns: []string{"person_id"}, RefTable: "users", RefColumns: []string{"id"}}}

	p := Diff(schemaOf(cur, other), schemaOf(want, wantOrders), Options{})
	got := ups(p)
	if !strings.Contains(got, `ALTER TABLE "people" RENAME TO "users"`) || !strings.Contains(got, `ALTER TABLE "users" RENAME COLUMN "mail" TO "email"`) {
		t.Fatalf("renames missing:\n%s", got)
	}
	// nothing else: no create, no drop, no duplicate unique / index / foreign key
	for _, bad := range []string{"CREATE", "DROP", "ADD CONSTRAINT", "ADD COLUMN"} {
		if strings.Contains(got, bad) {
			t.Errorf("unexpected %s in:\n%s", bad, got)
		}
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings: %v", p.Warnings)
	}
	// Down undoes the renames in reverse order
	down := strings.Join(p.Down(), ";\n")
	if strings.Index(down, `RENAME COLUMN "email" TO "mail"`) > strings.Index(down, `RENAME TO "people"`) {
		t.Errorf("down order:\n%s", down)
	}
}

func TestRenameIgnoredWhenOldNameIsGone(t *testing.T) {
	// after the rename has been applied the tag may stay: nothing happens
	cur := tbl("users", col("id", "uuid"), col("email", "text"))
	want := tbl("users", col("id", "uuid"), &Column{Name: "email", Type: "text", NotNull: true, Was: "mail"})
	want.Was = "people"
	if p := Diff(schemaOf(cur), schemaOf(want), Options{}); !p.Empty() {
		t.Fatalf("%v", p.Up())
	}
}

func TestChecks(t *testing.T) {
	cur := tbl("users", col("id", "uuid"), col("role", "text"))
	cur.Checks = []*Check{{Name: "users_role_check", Def: "CHECK ((role = ANY (ARRAY['user'::text])))"}, {Name: "old_check", Def: "CHECK ((id IS NOT NULL))"}}
	want := tbl("users", col("id", "uuid"), col("role", "text"), col("age", "integer"))
	want.Checks = []*Check{{Name: "users_role_check", Expr: "role IN ('user')"}, {Name: "users_age_check", Expr: "age >= 0"}}

	p := Diff(schemaOf(cur), schemaOf(want), Options{})
	got := ups(p)
	if !strings.Contains(got, `ADD CONSTRAINT "users_age_check" CHECK (age >= 0)`) || strings.Contains(got, `"users_role_check"`) || strings.Contains(got, "DROP") {
		t.Fatalf("by-name comparison, no drop without AllowDrop:\n%s", got)
	}
	drop := Diff(schemaOf(cur), schemaOf(want), Options{AllowDrop: true})
	if !strings.Contains(ups(drop), `DROP CONSTRAINT "old_check"`) || !strings.Contains(strings.Join(drop.Down(), ";"), `ADD CONSTRAINT "old_check" CHECK ((id IS NOT NULL))`) {
		t.Fatalf("drop with the original definition in Down:\n%s\n%v", ups(drop), drop.Down())
	}
	// a check on a new table is added after the table exists
	create := Diff(newSchema(), schemaOf(want), Options{})
	if !strings.HasPrefix(create.Steps[0].Up, "CREATE TABLE") || !strings.Contains(ups(create), `ADD CONSTRAINT "users_age_check"`) {
		t.Fatalf("%s", ups(create))
	}
}

func TestConcurrentIndexesGoToTheirOwnFile(t *testing.T) {
	cur := tbl("users", col("id", "uuid"), col("email", "text"))
	want := tbl("users", col("id", "uuid"), col("email", "text"), col("city", "text"))
	want.Indexes = []*Index{{Name: "idx_users_email", Columns: []string{"email"}}, {Name: "idx_users_city", Columns: []string{"city"}}}
	fresh := tbl("events", col("id", "uuid"), col("at", "timestamp with time zone"))
	fresh.Indexes = []*Index{{Name: "idx_events_at", Columns: []string{"at"}}}

	p := Diff(schemaOf(cur), schemaOf(want, fresh), Options{ConcurrentIndexes: true})
	main, conc := p.Render(), p.RenderConcurrent()
	if strings.Contains(main, "CONCURRENTLY") || !strings.Contains(main, `ADD COLUMN "city"`) || !strings.Contains(main, `CREATE INDEX "idx_events_at"`) {
		t.Fatalf("main file (a new table's index is created plainly):\n%s", main)
	}
	if !strings.HasPrefix(conc, "-- +goose NO TRANSACTION\n") || !strings.Contains(conc, `CREATE INDEX CONCURRENTLY IF NOT EXISTS "idx_users_email"`) ||
		!strings.Contains(conc, `DROP INDEX CONCURRENTLY IF EXISTS "idx_users_city"`) {
		t.Fatalf("concurrent file:\n%s", conc)
	}
	if plain := Diff(schemaOf(cur), schemaOf(want), Options{}); plain.RenderConcurrent() != "" || strings.Contains(plain.Render(), "CONCURRENTLY") {
		t.Fatal("without the option everything stays in one transactional file")
	}
}

func TestRiskIsWrittenIntoTheFile(t *testing.T) {
	cur := tbl("users", col("id", "uuid"))
	want := tbl("users", col("id", "uuid"), col("phone", "text")) // NOT NULL without default
	out := Diff(schemaOf(cur), schemaOf(want), Options{}).Render()
	if !strings.Contains(out, "-- WARNING: adds NOT NULL column phone without a default") || !strings.Contains(out, "-- +goose Down") {
		t.Fatalf("%s", out)
	}
}

// Models with the new tags go through FromModels end to end (no database).
type renamedUser struct {
	ID    uuid.UUID `db:"id" orm:"pk;default=gen_random_uuid()"`
	Email string    `db:"email" orm:"unique;was=mail;check=length(email) > 3"`
	Age   int       `db:"age" orm:"default=0"`
	Phone *string   `db:"phone" orm:"index"`
}

func (renamedUser) TableName() string { return "users" }
func (renamedUser) WasTable() string  { return "people" }
func (renamedUser) Checks() map[string]string {
	return map[string]string{"users_age_nonneg": "age >= 0"}
}

func TestFromModelsReadsRenameAndCheckTags(t *testing.T) {
	s, err := FromModels(renamedUser{})
	if err != nil {
		t.Fatal(err)
	}
	u := s.Tables["users"]
	if u.Was != "people" || u.column("email").Was != "mail" {
		t.Fatalf("%+v", u)
	}
	names := []string{}
	for _, c := range u.Checks {
		names = append(names, c.Name+"="+c.Expr)
	}
	if strings.Join(names, "|") != "users_email_check=length(email) > 3|users_age_nonneg=age >= 0" {
		t.Fatalf("%v", names)
	}
	_ = time.Now
}
