package query

import "slices"

// Policy restricts the fields a client may use. Allow, when not empty, is a white list (only those
// fields); Deny is a black list that always wins. Both can be combined.
type Policy struct {
	Allow []string
	Deny  []string
}

// Permits reports whether name may be used.
func (p Policy) Permits(name string) bool {
	if len(p.Allow) > 0 && !slices.Contains(p.Allow, name) {
		return false
	}
	return !slices.Contains(p.Deny, name)
}

func (p Policy) names() []string { return append(slices.Clone(p.Allow), p.Deny...) }

// AllowOnly builds a white-list policy.
func AllowOnly(names ...string) Policy { return Policy{Allow: names} }

// DenyFields builds a black-list policy.
func DenyFields(names ...string) Policy { return Policy{Deny: names} }
