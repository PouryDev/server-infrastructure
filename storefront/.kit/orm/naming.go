package orm

import (
	"strings"
	"unicode"
)

// snake converts GoName to go_name, keeping acronyms together: UserID -> user_id, HTTPServer -> http_server.
func snake(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			acronymEnd := i > 0 && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if prevLower || acronymEnd {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pluralize applies the common English rules; use a TableName method for anything irregular.
func pluralize(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}

// modelName strips the conventional row-struct suffixes: userRow -> user.
func modelName(typeName string) string {
	for _, suffix := range []string{"Row", "Model", "Entity"} {
		if len(typeName) > len(suffix) && strings.HasSuffix(typeName, suffix) {
			typeName = strings.TrimSuffix(typeName, suffix)
			break
		}
	}
	return snake(typeName)
}

// quote returns a double-quoted identifier. Identifiers are validated when metadata is built, so this
// never has to escape anything.
func quote(ident string) string { return `"` + ident + `"` }
