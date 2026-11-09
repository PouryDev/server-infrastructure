package query

import (
	"net/url"
	"testing"
)

func benchSchema(b *testing.B) *Schema {
	b.Helper()
	s, err := NewSchemaFor[row]("u", WithDefaultSort(Sort{Field: "createdAt", Desc: true}), WithTieBreaker("u.id"))
	if err != nil {
		b.Fatal(err)
	}
	return s
}

func benchValues() url.Values {
	v := url.Values{}
	v.Set("filter[age][gte]", "18")
	v.Set("filter[email][prefix]", "ada")
	v.Set("filter[active]", "true")
	v.Set("filter[id][in]", "0b0a3c3e-0000-4000-8000-000000000001,0b0a3c3e-0000-4000-8000-000000000002")
	v.Set("sort", "-createdAt,age")
	v.Set("page", "3")
	v.Set("page_size", "50")
	return v
}

func BenchmarkParseValues(b *testing.B) {
	v := benchValues()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseValues(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompile(b *testing.B) {
	s := benchSchema(b)
	p, _ := ParseValues(benchValues())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Compile(p, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompileEmpty(b *testing.B) {
	s := benchSchema(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Compile(Params{}, 0); err != nil {
			b.Fatal(err)
		}
	}
}
