package query

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Keyset (cursor) pagination: instead of OFFSET, which makes the database walk and discard every
// skipped row, the next page is "rows after the last one I saw", which an index can answer in constant
// time however deep the client has paged. The price: no page jumping and no total count, and every
// ORDER BY column must be NOT NULL (the schema's tie-breaker, normally the primary key, makes the
// order total).

type cursorToken struct {
	Order  string   `json:"o"` // fingerprint of the ORDER BY the values belong to
	Values []string `json:"v"`
}

func orderFingerprint(orderBy string) string {
	sum := sha256.Sum256([]byte(orderBy))
	return base64.RawURLEncoding.EncodeToString(sum[:6])
}

// EncodeCursor packs the sort-key values of the last row of a page into an opaque token. orderBy is the
// Statement.OrderBy the page was fetched with: a token only works with the same sorting.
func EncodeCursor(orderBy string, values []string) string {
	b, _ := json.Marshal(cursorToken{Order: orderFingerprint(orderBy), Values: values}) // always marshals
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(token string) (cursorToken, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	var t cursorToken
	if err != nil || json.Unmarshal(raw, &t) != nil {
		return cursorToken{}, errors.New("malformed cursor")
	}
	return t, nil
}

// CompileKeyset is Compile for keyset pagination. cursor is the token of the previous page ("" for the
// first page); a token made for another sorting is rejected. p.Page must be unset. The returned Statement has Offset 0; fetch Limit+1
// rows to learn whether another page exists.
func (s *Schema) CompileKeyset(p Params, cursor string, argOffset int) (Statement, error) {
	if p.Page > 1 {
		return Statement{}, apperr.InvalidField("page", "is not supported with cursor pagination, use the cursor")
	}
	st, err := s.Compile(p, argOffset)
	if err != nil {
		return Statement{}, err
	}
	for _, k := range st.Order {
		if !k.typed {
			return Statement{}, errors.New("query: keyset pagination needs the tie-breaker to be a schema field (typically the primary key)")
		}
	}
	if s.tieBreaker == "" {
		return Statement{}, errors.New("query: keyset pagination needs a tie-breaker so the order is total")
	}
	st.Offset, st.Page = 0, 1
	if cursor == "" {
		return st, nil
	}
	tok, err := decodeCursor(cursor)
	if err != nil {
		return Statement{}, apperr.InvalidField("cursor", err.Error())
	}
	after := tok.Values
	if tok.Order != orderFingerprint(st.OrderBy) || len(after) != len(st.Order) {
		return Statement{}, apperr.InvalidField("cursor", "does not match the requested sorting")
	}

	vals := make([]any, len(after))
	for i, raw := range after {
		v, err := parseValue(st.Order[i].Type, raw)
		if err != nil {
			return Statement{}, apperr.InvalidField("cursor", "does not match the requested sorting")
		}
		vals[i] = v
	}
	cond, args := keysetCondition(st.Order, vals, argOffset+len(st.Args))
	st.Args = append(st.Args, args...)
	if st.Where == "" {
		st.Where = cond
	} else {
		st.Where = st.Where + " AND " + cond
	}
	return st, nil
}

// keysetCondition builds "rows strictly after the cursor". With one direction for all keys a row-value
// comparison is used, which Postgres can serve from a composite index; mixed directions need the
// expanded OR form.
func keysetCondition(order []OrderKey, vals []any, argOffset int) (string, []any) {
	same := true
	for _, k := range order[1:] {
		same = same && k.Desc == order[0].Desc
	}
	ph := func(i int) string { return "$" + strconv.Itoa(argOffset+i+1) }
	if same {
		cols := make([]string, len(order))
		phs := make([]string, len(order))
		for i, k := range order {
			cols[i], phs[i] = k.Column, ph(i)
		}
		op := ">"
		if order[0].Desc {
			op = "<"
		}
		if len(order) == 1 {
			return cols[0] + " " + op + " " + phs[0], vals
		}
		return "(" + strings.Join(cols, ", ") + ") " + op + " (" + strings.Join(phs, ", ") + ")", vals
	}
	var ors []string
	for i, k := range order {
		var and []string
		for j := 0; j < i; j++ {
			and = append(and, order[j].Column+" = "+ph(j))
		}
		op := ">"
		if k.Desc {
			op = "<"
		}
		and = append(and, k.Column+" "+op+" "+ph(i))
		ors = append(ors, "("+strings.Join(and, " AND ")+")")
	}
	return "(" + strings.Join(ors, " OR ") + ")", vals
}

// FormatValue renders a typed sort-key value for a cursor, the inverse of the parsing done by
// CompileKeyset. Supported: string, bool, integers, floats, time.Time and fmt.Stringer (uuid.UUID).
func FormatValue(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint8, uint16, uint32:
		return fmt.Sprint(x), nil
	case float32:
		return strconv.FormatFloat(float64(x), 'g', -1, 32), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano), nil
	case fmt.Stringer:
		return x.String(), nil
	}
	return "", fmt.Errorf("query: cannot use %T as a cursor value", v)
}
