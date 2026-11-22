package fixedasset_test

import (
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/mizan/fixedasset"
)

func TestLegalTableIsConsistent(t *testing.T) {
	rows := fixedasset.LegalTable()
	seen := map[string]bool{}
	groups := map[int]bool{}
	for _, r := range rows {
		if seen[r.ID] {
			t.Fatalf("duplicate id %s", r.ID)
		}
		seen[r.ID] = true
		groups[r.Group] = true
		if r.Description == "" || r.GroupTitle == "" {
			t.Fatalf("%s: missing text", r.ID)
		}
		if r.Selectable {
			if err := r.Terms().Validate(); err != nil {
				t.Fatalf("%s: %v", r.ID, err)
			}
		} else if r.Note == "" {
			t.Fatalf("%s: a row that cannot be selected must say why", r.ID)
		}
		if !strings.Contains(r.Source(), "۱۳۹۴/۰۴/۳۱") {
			t.Fatalf("%s: source %q", r.ID, r.Source())
		}
	}
	if len(groups) != 25 {
		t.Fatalf("groups: %d", len(groups))
	}
	// Spot checks against the table: years for straight line, percent for declining balance.
	check := func(id string, m fixedasset.Method, months, bp int) {
		t.Helper()
		r, ok := fixedasset.LegalRowByID(id)
		if !ok || !r.Selectable || r.Method != m || r.Terms().LifeMonths != months || r.Terms().RateBP != bp {
			t.Fatalf("%s: %+v", id, r)
		}
	}
	check("g13-r4", fixedasset.StraightLine, 72, 0)   // cars: 6 years
	check("g05-r2", fixedasset.StraightLine, 36, 0)   // software and computers: 3 years
	check("g08-r1a", fixedasset.StraightLine, 300, 0) // concrete and steel buildings: 25 years
	check("g08-r1c", fixedasset.StraightLine, 120, 0) // containers (amendment 96/174): 10 years
	check("g09-r4", fixedasset.StraightLine, 60, 0)   // security systems (amendment 96/174): 5 years
	check("g10-r2b", fixedasset.StraightLine, 60, 0)  // phones (amendment 96/174): 5 years
	check("g04-r2b", fixedasset.StraightLine, 120, 0) // industrial refrigerators (amendment 96/174): 10 years
	check("g13-r6", fixedasset.DecliningBalance, 0, 2000)
	check("g23-r2a", fixedasset.DecliningBalance, 0, 1200)
	if r, _ := fixedasset.LegalRowByID("g21-rA6"); r.Selectable {
		t.Fatal("a dam's life is set by an engineer, not by the table")
	}
}
