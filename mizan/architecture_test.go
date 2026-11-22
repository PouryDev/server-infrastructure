package mizan_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mod = "github.com/PouryDev/server-infrastructure/mizan"
	kit = "github.com/PouryDev/server-infrastructure/gokit"
)

// Infrastructure packages of gokit that a domain or application package must not know about.
var infra = []string{kit + "/postgres", kit + "/httpx", kit + "/amqpx", kit + "/migrate", kit + "/orm", kit + "/redisx",
	kit + "/telemetry", kit + "/lifecycle", kit + "/auth", kit + "/logging", kit + "/config"}

func others(self string, contexts ...string) []string {
	var out []string
	for _, c := range contexts {
		if c != self {
			out = append(out, mod+"/"+c)
		}
	}
	return out
}

var contexts = []string{"ledger", "payment", "wallet", "invoice", "reconcile", "treasury", "payroll", "payout", "einvoice", "coupon", "subscription", "fixedasset", "inventory", "appropriation", "vat", "incometax", "approval"}

// The dependency rules of the module, enforced. dir is a package directory (not its sub-packages).
// Contexts are independent of each other and of the platform adapters; only the root package wires them.
var rules = []struct {
	dir    string
	forbid []string
}{
	{"money", append(append([]string{}, infra...), mod+"/")},
	{"jalali", append(append([]string{}, infra...), mod+"/")},
	{"trail", append(append([]string{}, infra...), mod+"/")},
	{"books", append(append([]string{}, infra...), mod+"/ledger", mod+"/payment", mod+"/wallet", mod+"/invoice", mod+"/reconcile", mod+"/treasury", mod+"/payroll", mod+"/payout", mod+"/einvoice", mod+"/coupon", mod+"/subscription", mod+"/fixedasset", mod+"/inventory", mod+"/appropriation", mod+"/vat", mod+"/incometax", mod+"/approval")},
	// bounded contexts: pure domain + use cases + ports
	{"ledger", join(infra, others("ledger", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"payment", join(infra, others("payment", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"wallet", join(infra, others("wallet", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"invoice", join(infra, others("invoice", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"reconcile", join(infra, others("reconcile", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"treasury", join(infra, others("treasury", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"payroll", join(infra, others("payroll", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"payout", join(infra, others("payout", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"einvoice", join(infra, others("einvoice", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"coupon", join(infra, others("coupon", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"subscription", join(infra, others("subscription", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"vat", join(infra, others("vat", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"approval", join(infra, others("approval", contexts...), mod+"/books", mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"incometax", join(infra, others("incometax", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"appropriation", join(infra, others("appropriation", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"inventory", join(infra, others("inventory", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	{"fixedasset", join(infra, others("fixedasset", contexts...), mod+"/outbox", mod+"/audit", mod+"/idempotency", mod+"/api")},
	// persistence adapters: their own context and the database, nothing else
	{"ledger/pgledger", join([]string{kit + "/httpx", kit + "/amqpx"}, others("ledger", contexts...))},
	{"payment/pgpayment", join([]string{kit + "/httpx", kit + "/amqpx"}, others("payment", contexts...))},
	{"wallet/pgwallet", join([]string{kit + "/httpx", kit + "/amqpx"}, others("wallet", contexts...))},
	{"invoice/pginvoice", join([]string{kit + "/httpx", kit + "/amqpx"}, others("invoice", contexts...))},
	{"reconcile/pgreconcile", join([]string{kit + "/httpx", kit + "/amqpx"}, others("reconcile", contexts...))},
	{"treasury/pgtreasury", join([]string{kit + "/httpx", kit + "/amqpx"}, others("treasury", contexts...))},
	{"payroll/pgpayroll", join([]string{kit + "/httpx", kit + "/amqpx"}, others("payroll", contexts...))},
	{"payout/pgpayout", join([]string{kit + "/httpx", kit + "/amqpx"}, others("payout", contexts...))},
	{"einvoice/pgeinvoice", join([]string{kit + "/httpx", kit + "/amqpx"}, others("einvoice", contexts...))},
	{"einvoice/moadian", join([]string{kit + "/postgres", kit + "/httpx", kit + "/amqpx"}, others("einvoice", contexts...))},
	{"einvoice/einvoicetest", join([]string{kit + "/postgres", kit + "/httpx", kit + "/amqpx"}, others("einvoice", contexts...))},
	{"coupon/pgcoupon", join([]string{kit + "/httpx", kit + "/amqpx"}, others("coupon", contexts...))},
	{"subscription/pgsubscription", join([]string{kit + "/httpx", kit + "/amqpx"}, others("subscription", contexts...))},
	{"vat/pgvat", join([]string{kit + "/httpx", kit + "/amqpx"}, others("vat", contexts...))},
	{"approval/pgapproval", join([]string{kit + "/httpx", kit + "/amqpx"}, others("approval", contexts...))},
	{"incometax/pgincometax", join([]string{kit + "/httpx", kit + "/amqpx"}, others("incometax", contexts...))},
	{"appropriation/pgappropriation", join([]string{kit + "/httpx", kit + "/amqpx"}, others("appropriation", contexts...))},
	{"inventory/pginventory", join([]string{kit + "/httpx", kit + "/amqpx"}, others("inventory", contexts...))},
	{"fixedasset/pgfixedasset", join([]string{kit + "/httpx", kit + "/amqpx"}, others("fixedasset", contexts...))},
	{"payment/gateway", join([]string{kit + "/postgres", kit + "/httpx", kit + "/amqpx"}, others("payment", contexts...))},
	{"payment/gateway/zarinpal", join([]string{kit + "/postgres", kit + "/httpx", kit + "/amqpx"}, others("payment", contexts...))},
	{"payment/gateway/idpay", join([]string{kit + "/postgres", kit + "/httpx", kit + "/amqpx"}, others("payment", contexts...))},
	// the HTTP adapter talks to the composed module, never to storage
	{"api", []string{kit + "/postgres", kit + "/orm", kit + "/amqpx", mod + "/ledger/pgledger", mod + "/payment/pgpayment", mod + "/wallet/pgwallet",
		mod + "/invoice/pginvoice", mod + "/reconcile/pgreconcile", mod + "/treasury/pgtreasury", mod + "/payroll/pgpayroll", mod + "/payout/pgpayout", mod + "/einvoice/pgeinvoice", mod + "/coupon/pgcoupon", mod + "/subscription/pgsubscription", mod + "/fixedasset/pgfixedasset", mod + "/inventory/pginventory", mod + "/appropriation/pgappropriation", mod + "/vat/pgvat", mod + "/incometax/pgincometax", mod + "/approval/pgapproval", mod + "/outbox", mod + "/audit"}},
}

// join flattens []string and string parts into one list.
func join(parts ...any) []string {
	var out []string
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		}
	}
	return out
}

func TestDependencyRule(t *testing.T) {
	for _, rule := range rules {
		entries, err := os.ReadDir(filepath.FromSlash(rule.dir))
		if err != nil {
			t.Errorf("%s: %v", rule.dir, err)
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(rule.dir, name), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range rule.forbid {
					if strings.HasSuffix(bad, "/") { // "anything inside the module"
						if strings.HasPrefix(p, bad) {
							t.Errorf("%s/%s imports %s: this package must stay free of other mizan packages", rule.dir, name, p)
						}
						continue
					}
					if p == bad || strings.HasPrefix(p, bad+"/") {
						t.Errorf("%s/%s imports %s, which the dependency rule forbids", rule.dir, name, p)
					}
				}
			}
		}
	}
}
