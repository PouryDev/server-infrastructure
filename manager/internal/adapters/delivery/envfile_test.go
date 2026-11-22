package delivery

import (
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func TestEnvDocPreservesCommentsOrderAndUnknownLines(t *testing.T) {
	src := "# header\n\nAPP_ENV=production\nexport DB_URL=\"postgres://u:p@h/db\" # note\nlowercase=kept\nthis is not an assignment\nSECRET_KEY='a b#c'\n"
	d := parseEnvDoc([]byte(src))
	if err := d.apply([]domain.EnvChange{{Name: "APP_ENV", Action: "set", Value: "staging"}, {Name: "NEW_ONE", Action: "set", Value: "x y"}, {Name: "SECRET_KEY", Action: "delete"}}); err != nil {
		t.Fatal(err)
	}
	want := "# header\n\nAPP_ENV=staging\nexport DB_URL=\"postgres://u:p@h/db\" # note\nlowercase=kept\nthis is not an assignment\nNEW_ONE='x y'\n"
	if string(d.bytes()) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", d.bytes(), want)
	}
	names := []string{}
	for _, v := range d.variables() {
		names = append(names, v.Name)
	}
	if strings.Join(names, ",") != "APP_ENV,DB_URL,NEW_ONE" {
		t.Fatalf("variables %v (unmanaged lowercase names are preserved but not listed)", names)
	}
	if v, _ := d.lookup("DB_URL"); v != "postgres://u:p@h/db" {
		t.Fatal(v)
	}
}
func TestEnvDocLastAssignmentWinsAndDuplicatesCollapse(t *testing.T) {
	d := parseEnvDoc([]byte("A=1\nB=2\nA=3\n"))
	if v, _ := d.lookup("A"); v != "3" {
		t.Fatal(v)
	}
	d.set("A", "9")
	if string(d.bytes()) != "B=2\nA=9\n" {
		t.Fatalf("%q", d.bytes())
	}
	d.delete("A")
	if string(d.bytes()) != "B=2\n" {
		t.Fatalf("%q", d.bytes())
	}
}
func TestEnvDocKeepsCRLFAndHandlesEmptyFile(t *testing.T) {
	d := parseEnvDoc([]byte("A=1\r\nB=2\r\n"))
	d.set("C", "3")
	if string(d.bytes()) != "A=1\r\nB=2\r\nC=3\r\n" {
		t.Fatalf("%q", d.bytes())
	}
	e := parseEnvDoc(nil)
	e.set("A", "1")
	if string(e.bytes()) != "A=1\n" {
		t.Fatalf("%q", e.bytes())
	}
}
func TestEnvValueQuotingRoundTrips(t *testing.T) {
	values := []string{"", "simple", "with space", "has#hash", "it's", `say "hi"`, `back\slash`, "$HOME and ${X}", "a=b=c", "trailing ", " leading", `it's "quoted" \ ok`, "unicode-سلام"}
	for _, v := range values {
		encoded, err := encodeEnvValue(v)
		if err != nil {
			if strings.Contains(v, "'") && strings.Contains(v, "$") {
				continue
			}
			t.Fatalf("%q: %v", v, err)
		}
		if got := decodeEnvValue(encoded); got != v {
			t.Errorf("%q encoded as %q decoded as %q", v, encoded, got)
		}
	}
	if _, err := encodeEnvValue("it's $HOME"); err == nil {
		t.Fatal("ambiguous quoting accepted")
	}
	for _, bad := range []string{"a\nb", "a\rb", "a\x00b"} {
		if _, err := encodeEnvValue(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
	if decodeEnvValue("value # comment") != "value" || decodeEnvValue(`"a\nb"`) != "a\nb" || decodeEnvValue(`'a\nb'`) != `a\nb` {
		t.Fatal("decode forms")
	}
}
func TestEnvChangeValidation(t *testing.T) {
	good := domain.EnvChange{Name: "A_B1", Action: "set", Value: "x"}
	if good.Validate() != nil {
		t.Fatal("valid change rejected")
	}
	for _, bad := range []domain.EnvChange{{Name: "a", Action: "set"}, {Name: "1A", Action: "set"}, {Name: "A-B", Action: "set"}, {Name: "A", Action: "set", Value: "x\ny"}, {Name: "A", Action: "delete", Value: "x"}, {Name: "A", Action: "wipe"}, {Name: "A", Scope: "../x", Action: "set"}} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if domain.ValidateEnvChanges([]domain.EnvChange{good, good}) == nil {
		t.Fatal("duplicate accepted")
	}
	if !domain.SensitiveEnvName("DB_PASSWORD") || !domain.SensitiveEnvName("API_KEY") || !domain.SensitiveEnvName("REDIS_URL") || domain.SensitiveEnvName("APP_ENV") {
		t.Fatal("sensitive-name detection")
	}
	detail := domain.AuditEnvDetail([]domain.EnvChange{{Name: "B", Action: "set", Value: "secret-value"}, {Name: "A", Action: "set", Value: "other-secret"}, {Scope: "api", Name: "C", Action: "delete"}})
	if strings.Contains(detail, "secret") || detail != "api delete=C; project set=A,B" {
		t.Fatal(detail)
	}
}
