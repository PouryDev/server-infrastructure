package query

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Query-string grammar understood by ParseValues:
//
//	filter[status]=active                 equality (same as filter[status][eq]=active)
//	filter[age][gte]=18                   any operator
//	filter[role][in]=admin,editor         lists are comma-separated (or the key may repeat)
//	filter[created_at][between]=2026-01-01,2026-02-01
//	filter[deleted_at][null]=true
//	sort=-created_at,name                 leading "-" means descending
//	page=2&page_size=50
//
// Other keys are ignored so handlers can add their own parameters.

// ParseValues extracts list parameters from a query string. It only checks syntax; field names,
// operators and values are validated by Schema.Compile.
func ParseValues(v url.Values) (Params, error) {
	var (
		p   Params
		bad []apperr.FieldError
	)
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	slices.Sort(keys) // deterministic placeholder numbering

	for _, k := range keys {
		if !strings.HasPrefix(k, "filter[") {
			continue
		}
		field, op, err := parseFilterKey(k)
		if err != nil {
			bad = append(bad, apperr.FieldError{Field: k, Message: err.Error()})
			continue
		}
		p.Filters = append(p.Filters, Filter{Field: field, Op: op, Values: v[k]})
	}

	for _, part := range strings.Split(v.Get("sort"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		desc := strings.HasPrefix(part, "-")
		p.Sort = append(p.Sort, Sort{Field: strings.TrimLeft(part, "+-"), Desc: desc})
	}

	var err error
	if p.Page, err = atoi(v, "page"); err != nil {
		bad = append(bad, apperr.FieldError{Field: "page", Message: err.Error()})
	}
	if p.PageSize, err = atoi(v, "page_size"); err != nil {
		bad = append(bad, apperr.FieldError{Field: "page_size", Message: err.Error()})
	}

	if len(bad) > 0 {
		return Params{}, apperr.Invalid("invalid list parameters", bad...)
	}
	return p, nil
}

// parseFilterKey splits "filter[field]" or "filter[field][op]".
func parseFilterKey(k string) (string, Operator, error) {
	rest := strings.TrimPrefix(k, "filter[")
	field, rest, ok := strings.Cut(rest, "]")
	if !ok || field == "" {
		return "", "", fmt.Errorf("expected filter[field] or filter[field][operator]")
	}
	if rest == "" {
		return field, OpEq, nil
	}
	if !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, "]") || strings.ContainsAny(rest[1:len(rest)-1], "[]") {
		return "", "", fmt.Errorf("expected filter[field] or filter[field][operator]")
	}
	return field, Operator(rest[1 : len(rest)-1]), nil
}

func atoi(v url.Values, key string) (int, error) {
	s := v.Get(key)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	return n, nil
}
