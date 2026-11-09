package query

import (
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

type row struct {
	ID        uuid.UUID  `db:"id"`
	Email     string     `db:"email" json:"email"`
	Age       int        `db:"age"`
	Active    bool       `db:"active"`
	Hash      string     `db:"password_hash" json:"-" query:"-"`
	Notes     string     `db:"notes" query:"nofilter,nosort"`
	CreatedAt time.Time  `db:"created_at" json:"createdAt"`
	DeletedAt *time.Time `db:"deleted_at"`
	NoTag     string
}

func schema(t *testing.T, opts ...Option) *Schema {
	t.Helper()
	opts = append([]Option{WithDefaultSort(Sort{Field: "createdAt", Desc: true}), WithTieBreaker("u.id")}, opts...)
	s, err := NewSchemaFor[row]("u", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.KindInvalid {
		t.Fatalf("want invalid apperr, got %v", err)
	}
	m := map[string]string{}
	for _, f := range e.Fields {
		m[f.Field] = f.Message
	}
	return m
}

func TestFieldsOfReflection(t *testing.T) {
	fields, err := FieldsOf[row]("u")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range fields {
		names = append(names, f.Name)
	}
	want := []string{"id", "email", "age", "active", "notes", "createdAt", "deleted_at"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if fields[1].Column != "u.email" {
		t.Fatalf("column = %q", fields[1].Column)
	}
}

func TestCompileOperators(t *testing.T) {
	s := schema(t)
	id := uuid.MustParse("0b0a3c3e-0000-4000-8000-000000000001")
	cases := []struct {
		name  string
		f     Filter
		where string
		args  []any
	}{
		{"eq default", Filter{Field: "email", Values: []string{"a@b.c"}}, "u.email = $1", []any{"a@b.c"}},
		{"gte int", Filter{Field: "age", Op: OpGte, Values: []string{"18"}}, "u.age >= $1", []any{int64(18)}},
		{"in", Filter{Field: "age", Op: OpIn, Values: []string{"1,2", "3"}}, "u.age = ANY($1)", []any{[]int64{1, 2, 3}}},
		{"nin uuid", Filter{Field: "id", Op: OpNin, Values: []string{id.String()}}, "u.id <> ALL($1)", []any{[]uuid.UUID{id}}},
		{"between", Filter{Field: "age", Op: OpBetween, Values: []string{"1,5"}}, "u.age BETWEEN $1 AND $2", []any{int64(1), int64(5)}},
		{"like escapes wildcards", Filter{Field: "email", Op: OpLike, Values: []string{"50%_a"}}, "u.email ILIKE $1", []any{`%50\%\_a%`}},
		{"prefix", Filter{Field: "email", Op: OpPrefix, Values: []string{"ab"}}, "u.email ILIKE $1", []any{"ab%"}},
		{"is null", Filter{Field: "deleted_at", Op: OpNull, Values: []string{"true"}}, "u.deleted_at IS NULL", nil},
		{"is not null", Filter{Field: "deleted_at", Op: OpNull, Values: []string{"false"}}, "u.deleted_at IS NOT NULL", nil},
		{"bool", Filter{Field: "active", Values: []string{"true"}}, "u.active = $1", []any{true}},
		{"date", Filter{Field: "createdAt", Op: OpLt, Values: []string{"2026-01-02"}}, "u.created_at < $1",
			[]any{time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := s.Compile(Params{Filters: []Filter{c.f}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if st.Where != c.where || !reflect.DeepEqual(st.Args, c.args) {
				t.Fatalf("got %q %#v\nwant %q %#v", st.Where, st.Args, c.where, c.args)
			}
		})
	}
}

func TestPlaceholdersContinueAfterCallerArgs(t *testing.T) {
	st, err := schema(t).Compile(Params{Filters: []Filter{
		{Field: "age", Op: OpGt, Values: []string{"1"}}, {Field: "email", Values: []string{"x"}},
	}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if st.Where != "u.age > $3 AND u.email = $4" {
		t.Fatalf("where = %q", st.Where)
	}
}

func TestPoliciesAllowAndDeny(t *testing.T) {
	filter := func(field string) Params { return Params{Filters: []Filter{{Field: field, Values: []string{"x"}}}} }

	// Deny list.
	s := schema(t, WithFilterPolicy(DenyFields("email")))
	if _, err := s.Compile(filter("email"), 0); err == nil {
		t.Fatal("denied field must be rejected")
	}
	if _, err := s.Compile(filter("age"), 0); err != nil && strings.Contains(err.Error(), "age") {
		t.Fatalf("other fields stay usable: %v", err)
	}

	// Allow list applies to sorting independently of filtering.
	s = schema(t, WithSortPolicy(AllowOnly("age")))
	if _, err := s.Compile(Params{Sort: []Sort{{Field: "email"}}}, 0); err == nil {
		t.Fatal("sort outside the allow list must be rejected")
	}
	if _, err := s.Compile(Params{Sort: []Sort{{Field: "age", Desc: true}}}, 0); err != nil {
		t.Fatal(err)
	}

	// Struct tags and unknown fields look identical to the client.
	for _, f := range []string{"password_hash", "nope", "u.email; DROP TABLE users", "notes"} {
		_, err := schema(t).Compile(filter(f), 0)
		if msgs := fieldErrors(t, err); len(msgs) != 1 {
			t.Fatalf("%q: %v", f, msgs)
		}
	}
}

func TestRestrictOnlyTightens(t *testing.T) {
	base := schema(t, WithPolicy(DenyFields("notes")))
	admin := base // full access
	user, err := base.Restrict(WithFilterPolicy(AllowOnly("email", "age")))
	if err != nil {
		t.Fatal(err)
	}
	if got := user.Filterable(); !reflect.DeepEqual(got, []string{"email", "age"}) {
		t.Fatalf("user filterable = %v", got)
	}
	if len(admin.Filterable()) <= len(user.Filterable()) {
		t.Fatal("base schema must be unchanged by Restrict")
	}
	if _, err := base.Restrict(WithPolicy(DenyFields("emial"))); err == nil {
		t.Fatal("a typo in a deny list must fail loudly")
	}
	// A restriction cannot re-enable what the base denied, nor raise limits.
	wide, _ := base.Restrict(WithFilterPolicy(AllowOnly("notes", "email")), WithPageSize(500, 1000))
	if got := wide.Filterable(); !reflect.DeepEqual(got, []string{"email"}) {
		t.Fatalf("filterable = %v", got)
	}
	if st, _ := wide.Compile(Params{PageSize: 1000}, 0); st.PageSize != 100 {
		t.Fatalf("page size = %d", st.PageSize)
	}
}

func TestSortAndPagination(t *testing.T) {
	s := schema(t)
	st, err := s.Compile(Params{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.OrderBy != "u.created_at DESC, u.id ASC" || st.Limit != 20 || st.Offset != 0 || st.Page != 1 {
		t.Fatalf("defaults: %+v", st)
	}

	st, err = s.Compile(Params{Sort: []Sort{{Field: "age"}, {Field: "age", Desc: true}}, Page: 3, PageSize: 1000}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if st.OrderBy != "u.age ASC, u.id ASC" || st.Limit != 100 || st.Offset != 200 {
		t.Fatalf("custom: %+v", st)
	}

	if _, err := s.Compile(Params{Page: 1_000_000}, 0); err == nil {
		t.Fatal("deep offsets must be rejected")
	}
	if _, err := s.Compile(Params{Page: -1}, 0); err == nil {
		t.Fatal("negative page must be rejected")
	}
}

func TestCompileReportsEverything(t *testing.T) {
	_, err := schema(t).Compile(Params{
		Filters: []Filter{
			{Field: "age", Op: OpGt, Values: []string{"abc"}},
			{Field: "email", Op: OpGt, Values: []string{"x"}},
			{Field: "age", Op: OpBetween, Values: []string{"1"}},
			{Field: "id", Values: []string{"not-a-uuid"}},
		},
		Sort: []Sort{{Field: "bogus"}},
	}, 0)
	msgs := fieldErrors(t, err)
	for _, k := range []string{"filter[age]", "filter[email]", "filter[id]", "sort"} {
		if _, ok := msgs[k]; !ok {
			t.Errorf("missing error for %s in %v", k, msgs)
		}
	}
}

func TestParseValues(t *testing.T) {
	v := url.Values{}
	v.Set("filter[age][gte]", "18")
	v.Set("filter[email]", "a@b.c")
	v.Add("filter[id][in]", "1")
	v.Add("filter[id][in]", "2")
	v.Set("sort", "-createdAt, age")
	v.Set("page", "2")
	v.Set("page_size", "5")
	v.Set("unrelated", "x")

	p, err := ParseValues(v)
	if err != nil {
		t.Fatal(err)
	}
	want := Params{
		Filters: []Filter{
			{Field: "age", Op: OpGte, Values: []string{"18"}},
			{Field: "email", Op: OpEq, Values: []string{"a@b.c"}},
			{Field: "id", Op: OpIn, Values: []string{"1", "2"}},
		},
		Sort: []Sort{{Field: "createdAt", Desc: true}, {Field: "age"}},
		Page: 2, PageSize: 5,
	}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v\nwant %+v", p, want)
	}

	for _, bad := range []string{"filter[]", "filter[a", "filter[a][b][c]", "filter[a]x"} {
		if _, err := ParseValues(url.Values{bad: {"1"}}); err == nil {
			t.Errorf("%q must be a syntax error", bad)
		}
	}
	if _, err := ParseValues(url.Values{"page": {"x"}}); err == nil {
		t.Error("page=x must be rejected")
	}
}

func TestNewSchemaRejectsUnsafeInput(t *testing.T) {
	for _, f := range []Field{
		{Name: "a", Column: "a; DROP TABLE x"},
		{Name: "a", Column: "lower(a)"},
		{Name: "", Column: "a"},
		{Name: "a", Column: "a", Type: TypeBool, Operators: []Operator{OpGt}},
	} {
		if _, err := NewSchema([]Field{f}); err == nil {
			t.Errorf("%+v must be rejected", f)
		}
	}
	if _, err := NewSchema([]Field{{Name: "a", Column: "a"}, {Name: "a", Column: "b"}}); err == nil {
		t.Error("duplicate names must be rejected")
	}
	if _, err := NewSchema([]Field{{Name: "a", Column: "a"}}, WithPolicy(DenyFields("zzz"))); err == nil {
		t.Error("unknown policy field must be rejected")
	}
}

func TestNewPage(t *testing.T) {
	p := NewPage[int](nil, 2, 10, true, 25)
	if p.Items == nil || *p.Meta.Total != 25 || *p.Meta.TotalPages != 3 {
		t.Fatalf("%+v", p)
	}
	if q := NewPage([]int{1}, 1, 10, false, -1); q.Meta.Total != nil {
		t.Fatal("unknown total must be omitted")
	}
	m := MapPage(NewPage([]int{1, 2}, 1, 10, false, 2), func(i int) string { return strings.Repeat("x", i) })
	if m.Items[1] != "xx" || *m.Meta.Total != 2 {
		t.Fatalf("%+v", m)
	}
	_ = errors.New
}

func TestKeysetCondition(t *testing.T) {
	s := schema(t)
	asc := func(f string) Params { return Params{Sort: []Sort{{Field: f}}} }
	token := func(p Params, vals ...string) string {
		st, err := s.Compile(p, 0)
		if err != nil {
			t.Fatal(err)
		}
		return EncodeCursor(st.OrderBy, vals)
	}

	// first page: no cursor, no extra condition, tie-breaker present.
	st, err := s.CompileKeyset(Params{}, "", 0)
	if err != nil || st.Where != "" || st.Offset != 0 || len(st.Order) != 2 {
		t.Fatalf("%+v %v", st, err)
	}

	// same direction on all keys: one row-value comparison (index friendly).
	id := uuid.MustParse("0b0a3c3e-0000-4000-8000-000000000001")
	st, err = s.CompileKeyset(asc("age"), token(asc("age"), "7", id.String()), 1)
	if err != nil {
		t.Fatal(err)
	}
	if st.Where != "(u.age, u.id) > ($2, $3)" || !reflect.DeepEqual(st.Args, []any{int64(7), id}) {
		t.Fatalf("%q %#v", st.Where, st.Args)
	}

	// mixed directions (default sort is created_at DESC + id ASC): expanded form.
	filtered := Params{Filters: []Filter{{Field: "age", Op: OpGt, Values: []string{"1"}}}}
	st, err = s.CompileKeyset(filtered, token(filtered, "2026-01-02T03:04:05Z", id.String()), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "u.age > $1 AND ((u.created_at < $2) OR (u.created_at = $2 AND u.id > $3))"
	if st.Where != want {
		t.Fatalf("\n got %s\nwant %s", st.Where, want)
	}

	for name, c := range map[string]struct {
		p      Params
		cursor string
	}{
		"cursor of another sort order": {asc("age"), token(Params{}, "2026-01-02T03:04:05Z", id.String())},
		"wrong value count":            {Params{}, EncodeCursor("u.created_at DESC, u.id ASC", []string{"x"})},
		"bad value":                    {asc("age"), token(asc("age"), "notint", id.String())},
		"garbage":                      {Params{}, "%%%"},
		"page > 1":                     {Params{Page: 2}, ""},
	} {
		if _, err := s.CompileKeyset(c.p, c.cursor, 0); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	noTie, _ := NewSchemaFor[row]("u")
	if _, err := noTie.CompileKeyset(Params{}, "", 0); err == nil {
		t.Error("keyset needs a tie-breaker")
	}
}

func TestFormatValue(t *testing.T) {
	for v, want := range map[any]string{
		int64(5): "5", "x": "x", true: "true", 1.5: "1.5",
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC):            "2026-01-02T03:04:05Z",
		uuid.MustParse("0b0a3c3e-0000-4000-8000-000000000001"): "0b0a3c3e-0000-4000-8000-000000000001",
	} {
		if got, err := FormatValue(v); err != nil || got != want {
			t.Errorf("%v: %q %v", v, got, err)
		}
	}
	if _, err := FormatValue(struct{}{}); err == nil {
		t.Error("unsupported types must error")
	}
}
