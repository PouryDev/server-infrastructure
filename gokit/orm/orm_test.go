package orm_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/orm"
)

func TestDescribe(t *testing.T) {
	m, err := orm.Describe(authorRow{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Table != "authors" || m.Name != "author" || len(m.PK) != 1 || m.PK[0].Column != "id" {
		t.Fatalf("%+v", m)
	}
	if m.CreatedAt == nil || m.UpdatedAt == nil || m.SoftDelete == nil || m.Version == nil {
		t.Fatal("special columns not detected")
	}
	if f, _ := m.Field("created_at"); f.Default != "now()" {
		t.Fatalf("created_at default = %q", f.Default)
	}
	if f, _ := m.Field("name"); f.SQLType != "character varying(100)" {
		t.Fatalf("name type = %q", f.SQLType)
	}
	if f, _ := m.Field("bio"); !f.Nullable || f.SQLType != "text" {
		t.Fatalf("bio = %+v", f)
	}
	rel, ok := m.Relation("Posts")
	if !ok || rel.Kind != orm.HasMany || rel.FK != "author_id" {
		t.Fatalf("%+v", rel)
	}

	// Cached: the same pointer comes back, also through a pointer or reflect.Type argument.
	again, _ := orm.Describe(&authorRow{})
	if again != m {
		t.Fatal("metadata must be cached")
	}

	p, _ := orm.Describe(postRow{})
	for col, want := range map[string]string{"id": "bigint", "labels": "text[]", "meta": "jsonb", "rating": "double precision", "views": "bigint"} {
		if f, _ := p.Field(col); f.SQLType != want {
			t.Errorf("%s: %q, want %q", col, f.SQLType, want)
		}
	}
	if f, _ := p.Field("id"); !f.Auto {
		t.Error("id must be auto")
	}
	tags, _ := p.Relation("Tags")
	if tags.JoinTable != "post_tags" || tags.JoinFK != "post_id" || tags.JoinRef != "tag_id" {
		t.Fatalf("join defaults: %+v", tags)
	}
}

func TestDescribeErrors(t *testing.T) {
	type noPK struct {
		Name string `db:"name"`
	}
	type badTable struct {
		ID int `db:"id" orm:"pk"`
	}
	type badAuto struct {
		ID string `db:"id" orm:"pk;auto"`
	}
	type badRel struct {
		ID    int     `db:"id" orm:"pk"`
		Posts postRow `orm:"rel=has_many;fk=x"`
	}
	type badFK struct {
		ID int      `db:"id" orm:"pk"`
		P  *postRow `orm:"rel=belongs_to;fk=missing"`
	}
	type badName struct {
		ID int `db:"Bad-Name" orm:"pk"`
	}
	type unsupported struct {
		ID int      `db:"id" orm:"pk"`
		C  chan int `db:"c"`
	}
	for name, v := range map[string]any{"no pk": noPK{}, "auto on string": badAuto{}, "slice mismatch": badRel{},
		"unknown fk column": badFK{}, "bad column name": badName{}, "unsupported type": unsupported{}, "not a struct": 5} {
		if _, err := orm.Describe(v); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	_ = badTable{}
}

func TestQuerySQL(t *testing.T) {
	r, err := orm.New[authorRow](nil)
	if err != nil {
		t.Fatal(err)
	}
	pr, _ := orm.New[postRow](nil)
	const a = `"authors".`
	cols := a + `"id", ` + a + `"name", ` + a + `"email", ` + a + `"bio", ` + a + `"secret", ` + a + `"created_at", ` + a + `"updated_at", ` + a + `"deleted_at", ` + a + `"version"`
	pcols := `"posts"."id", "posts"."author_id", "posts"."title", "posts"."views", "posts"."rating", "posts"."labels", "posts"."meta"`

	cases := []struct {
		name string
		sql  func() (string, []any, error)
		want string
		args []any
	}{
		{"soft delete hidden by default", r.Query().SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE "authors"."deleted_at" IS NULL`, nil},
		{"with deleted", r.Query().WithDeleted().Where(orm.Eq("name", "a")).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE "authors"."name" = $1`, []any{"a"}},
		{"and/or/not/in", r.Query().WithDeleted().Where(
			orm.Or(orm.Eq("name", "a"), orm.And(orm.Gt("version", 2), orm.Not(orm.Like("email", "%x")))), orm.In("name", []string{"p", "q"})).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE ("authors"."name" = $1 OR ("authors"."version" > $2 AND NOT ("authors"."email" ILIKE $3))) AND "authors"."name" = ANY($4)`,
			[]any{"a", 2, "%x", []string{"p", "q"}}},
		{"null checks and empty in", r.Query().WithDeleted().Where(orm.IsNull("bio"), orm.In("name", []string{}), orm.NotIn("name", []string{})).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE "authors"."bio" IS NULL AND FALSE AND TRUE`, nil},
		{"contains escapes wildcards", r.Query().WithDeleted().Where(orm.Contains("name", "50%_")).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE "authors"."name" ILIKE $1`, []any{`%50\%\_%`}},
		{"order, limit, projection, lock", r.Query().WithDeleted().Select("id", "name").OrderByDesc("created_at").OrderBy("id").Limit(5).Offset(10).ForUpdate().SQL,
			`SELECT ` + a + `"id", ` + a + `"name" FROM "authors" ORDER BY ` + a + `"created_at" DESC, ` + a + `"id" LIMIT 5 OFFSET 10 FOR UPDATE`, nil},
		{"raw placeholders", r.Query().WithDeleted().Where(orm.Raw("lower(name) = ? AND version > ? AND note = '??'", "x", 3)).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE (lower(name) = $1 AND version > $2 AND note = '?')`, []any{"x", 3}},
		{"go field names work too", r.Query().WithDeleted().Where(orm.Eq("CreatedAt", 1)).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE "authors"."created_at" = $1`, []any{1}},

		// relations as conditions: EXISTS, never row multiplication
		{"has: belongs_to", pr.Query().Where(orm.Has("Author", orm.Eq("name", "ada"))).SQL,
			`SELECT ` + pcols + ` FROM "posts" WHERE EXISTS (SELECT 1 FROM "authors" AS "x1" WHERE "x1"."id" = "posts"."author_id" AND "x1"."deleted_at" IS NULL AND "x1"."name" = $1)`, []any{"ada"}},
		{"has: has_many with a not-exists", r.Query().WithDeleted().Where(orm.NotHas("Posts", orm.Gt("views", 10))).SQL,
			`SELECT ` + cols + ` FROM "authors" WHERE NOT EXISTS (SELECT 1 FROM "posts" AS "x1" WHERE "x1"."author_id" = "authors"."id" AND "x1"."views" > $1)`, []any{10}},
		{"has: many_to_many", pr.Query().Where(orm.Has("Tags", orm.In("name", []string{"go", "sql"}))).SQL,
			`SELECT ` + pcols + ` FROM "posts" WHERE EXISTS (SELECT 1 FROM "post_tags" AS "x1j" JOIN "tags" AS "x1" ON "x1"."id" = "x1j"."tag_id" WHERE "x1j"."post_id" = "posts"."id" AND "x1"."name" = ANY($1))`, []any{[]string{"go", "sql"}}},
		{"has: two relations number their placeholders consistently", pr.Query().Where(orm.Eq("title", "t"), orm.Has("Comments", orm.Eq("body", "b")), orm.Has("Tags", orm.Eq("name", "n"))).SQL,
			`SELECT ` + pcols + ` FROM "posts" WHERE "posts"."title" = $1 AND EXISTS (SELECT 1 FROM "comments" AS "x1" WHERE "x1"."post_id" = "posts"."id" AND "x1"."body" = $2) AND EXISTS (SELECT 1 FROM "post_tags" AS "x2j" JOIN "tags" AS "x2" ON "x2"."id" = "x2j"."tag_id" WHERE "x2j"."post_id" = "posts"."id" AND "x2"."name" = $3)`,
			[]any{"t", "b", "n"}},

		// joins
		{"join belongs_to: filter and sort by the related column", pr.Query().Join("Author").Where(orm.Eq("Author.name", "ada")).OrderBy("Author.name").OrderByDesc("views").Limit(5).SQL,
			`SELECT ` + pcols + ` FROM "posts" JOIN "authors" AS "j_author" ON "j_author"."id" = "posts"."author_id" AND "j_author"."deleted_at" IS NULL WHERE "j_author"."name" = $1 ORDER BY "j_author"."name", "posts"."views" DESC LIMIT 5`, []any{"ada"}},
		{"left join + distinct to-many", r.Query().WithDeleted().LeftJoin("Posts").Distinct().Where(orm.Gt("Posts.views", 5)).SQL,
			`SELECT DISTINCT ` + cols + ` FROM "authors" LEFT JOIN "posts" AS "j_posts" ON "j_posts"."author_id" = "authors"."id" WHERE "j_posts"."views" > $1`, []any{5}},
		{"join many_to_many", pr.Query().Join("Tags").Distinct().Where(orm.Eq("Tags.name", "go")).SQL,
			`SELECT DISTINCT ` + pcols + ` FROM "posts" JOIN "post_tags" AS "j_tags_link" ON "j_tags_link"."post_id" = "posts"."id" JOIN "tags" AS "j_tags" ON "j_tags"."id" = "j_tags_link"."tag_id" WHERE "j_tags"."name" = $1`, []any{"go"}},
	}
	for _, c := range cases {
		sql, args, err := c.sql()
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if sql != c.want || !reflect.DeepEqual(args, c.args) {
			t.Errorf("%s:\n got %s %#v\nwant %s %#v", c.name, sql, args, c.want, c.args)
		}
	}

	for name, q := range map[string]*orm.Query[authorRow]{
		"unknown column":                     r.Query().Where(orm.Eq("nope", 1)),
		"unknown order column":               r.Query().OrderBy("x; DROP TABLE authors"),
		"unknown select column":              r.Query().Select("zzz"),
		"in needs a slice":                   r.Query().Where(orm.In("name", "x")),
		"raw arg count mismatch":             r.Query().Where(orm.Raw("a = ? AND b = ?", 1)),
		"unknown relation (Has)":             r.Query().Where(orm.Has("Nope")),
		"unknown relation (Join)":            r.Query().Join("Nope"),
		"column of an unjoined relation":     r.Query().Where(orm.Eq("Posts.views", 1)),
		"column missing on the joined model": r.Query().Join("Posts").Where(orm.Eq("Posts.nope", 1)),
	} {
		if _, _, err := q.SQL(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSchemaFromModel(t *testing.T) {
	r, _ := orm.New[authorRow](nil)
	s, err := r.Schema()
	if err != nil {
		t.Fatal(err)
	}
	filterable := strings.Join(s.Filterable(), ",")
	for _, hidden := range []string{"secret", "deleted_at"} {
		if strings.Contains(filterable, hidden) {
			t.Errorf("%s must not be queryable: %s", hidden, filterable)
		}
	}
	for _, want := range []string{"id", "name", "email", "created_at", "version"} {
		if !strings.Contains(filterable, want) {
			t.Errorf("%s missing from %s", want, filterable)
		}
	}
}

func BenchmarkBuildSelectSQL(b *testing.B) {
	r, _ := orm.New[authorRow](nil)
	b.ReportAllocs()
	for b.Loop() {
		_, _, err := r.Query().Where(orm.Eq("name", "a"), orm.In("version", []int{1, 2, 3}), orm.HasPrefix("email", "x")).
			OrderByDesc("created_at").Limit(20).SQL()
		if err != nil {
			b.Fatal(err)
		}
	}
}
