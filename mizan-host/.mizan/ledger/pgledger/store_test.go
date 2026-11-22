package pgledger_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/postgres"
	"github.com/PouryDev/server-infrastructure/mizan/internal/testdb"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
	"github.com/PouryDev/server-infrastructure/mizan/ledger/pgledger"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

var now = time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) // 1405/08/18

func irr(n int64) money.Money { return money.Must(n, money.IRR) }

func setup(t *testing.T) (*ledger.Service, *postgres.DB) {
	t.Helper()
	db := testdb.New(t)
	store, err := pgledger.New(db)
	if err != nil {
		t.Fatal(err)
	}
	svc := ledger.NewService(store, db, ledger.WithClock(func() time.Time { return now }))
	ctx := context.Background()
	if err := svc.EnsureCurrentFiscalYear(ctx); err != nil {
		t.Fatal(err)
	}
	must(t, svc.Ensure(ctx,
		ledger.AccountSpec{Code: "cash", Name: "Cash", Type: ledger.Asset, Currency: money.IRR},
		ledger.AccountSpec{Code: "sales", Name: "Sales", Type: ledger.Revenue, Currency: money.IRR},
		ledger.AccountSpec{Code: "wallet.1", Name: "Wallet 1", Type: ledger.Liability, Currency: money.IRR, NonNegative: true},
	))
	return svc, db
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sale(key string, amount int64) ledger.Draft {
	return ledger.Draft{Key: key, Date: now, Description: "sale", Ref: ledger.Ref{Type: "test", ID: key},
		Lines: []ledger.Line{ledger.Dr("cash", irr(amount)), ledger.Cr("sales", irr(amount))}}
}

func TestPostIsIdempotentAndNumbered(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	a, err := svc.Post(ctx, sale("k1", 1000))
	must(t, err)
	b, err := svc.Post(ctx, sale("k1", 1000)) // retry
	must(t, err)
	if a.ID != b.ID || a.Number != 1 {
		t.Fatalf("retry created a second journal: %v %v", a.Number, b.Number)
	}
	if _, err := svc.Post(ctx, sale("k1", 2000)); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("same key with other content must conflict, got %v", err)
	}
	c, err := svc.Post(ctx, sale("k2", 500))
	must(t, err)
	if c.Number != 2 {
		t.Fatalf("numbers must be consecutive, got %d", c.Number)
	}
	tb, err := svc.TrialBalance(ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if !tb.Balanced || tb.Debit != 1500 {
		t.Fatalf("trial balance %+v", tb)
	}
}

func TestConcurrentSameKeyPostsOnce(t *testing.T) {
	svc, _ := setup(t)
	var wg sync.WaitGroup
	ids := make(chan uuid.UUID, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if j, err := svc.Post(context.Background(), sale("same", 100)); err == nil {
				ids <- j.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[uuid.UUID]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("expected exactly one journal, got %d", len(seen))
	}
	bal, _ := svc.Balance(context.Background(), "cash", nil)
	if bal.Amount() != 100 {
		t.Fatalf("money was posted more than once: %v", bal)
	}
}

func TestNumbersHaveNoGapsUnderConcurrencyAndFailures(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := sale("n"+string(rune('a'+i)), 10)
			if i%5 == 0 { // fails after the number would have been taken: must not burn a number
				d.Lines[0].Account = "missing"
			}
			_, _ = svc.Post(ctx, d)
		}()
	}
	wg.Wait()
	n, err := postgres.Value[int64](ctx, db.Querier(ctx), `SELECT count(*) FROM mz_journals`)
	must(t, err)
	max, err := postgres.Value[int64](ctx, db.Querier(ctx), `SELECT max(number) FROM mz_journals`)
	must(t, err)
	if n != 24 || max != 24 {
		t.Fatalf("expected 24 journals numbered 1..24, got count=%d max=%d", n, max)
	}
}

func TestReverse(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	j, err := svc.Post(ctx, sale("r1", 700))
	must(t, err)
	rev, err := svc.Reverse(ctx, j.ID, ledger.ReverseRequest{Key: "rev:r1", Reason: "mistake"})
	must(t, err)
	if rev.Kind != ledger.KindReversal || rev.ReversesID == nil || *rev.ReversesID != j.ID {
		t.Fatalf("bad reversal %+v", rev)
	}
	if bal, _ := svc.Balance(ctx, "cash", nil); bal.Amount() != 0 {
		t.Fatalf("cash after reversal: %v", bal)
	}
	if _, err := svc.Reverse(ctx, j.ID, ledger.ReverseRequest{Key: "rev2:r1"}); !apperr.Is(err, apperr.KindConflict) {
		t.Fatalf("second reversal must conflict, got %v", err)
	}
	if _, err := svc.Reverse(ctx, rev.ID, ledger.ReverseRequest{Key: "rev3"}); !apperr.Is(err, apperr.KindInvalid) {
		t.Fatalf("reversing a reversal must be invalid, got %v", err)
	}
}

func TestAppendOnlyIsEnforcedByTheDatabase(t *testing.T) {
	svc, db := setup(t)
	ctx := context.Background()
	j, err := svc.Post(ctx, sale("a1", 100))
	must(t, err)
	q := db.Querier(ctx)
	for _, stmt := range []string{
		`UPDATE mz_journals SET description = 'x'`,
		`DELETE FROM mz_journals`,
		`UPDATE mz_journal_lines SET debit = debit + 1 WHERE debit > 0`,
		`DELETE FROM mz_journal_lines`,
		`TRUNCATE mz_journals CASCADE`,
	} {
		if _, err := q.Exec(ctx, stmt); err == nil {
			t.Errorf("%q succeeded on an append-only table", stmt)
		}
	}
	// An unbalanced journal cannot be committed even by bypassing the service.
	err = db.WithTx(ctx, func(ctx context.Context) error {
		q := db.Querier(ctx)
		id := uuid.New()
		if _, err := q.Exec(ctx, `INSERT INTO mz_journals (id, fiscal_year_id, period_id, number, kind, entry_date, description, currency, total, idem_key, fingerprint)
			SELECT $1, fiscal_year_id, period_id, 999, 'normal', entry_date, 'x', 'IRR', 100, 'raw', 'f' FROM mz_journals WHERE id = $2`, id, j.ID); err != nil {
			return err
		}
		_, err := q.Exec(ctx, `INSERT INTO mz_journal_lines (journal_id, line_no, account_id, debit) SELECT $1, 1, id, 100 FROM mz_accounts WHERE code = 'cash'`, id)
		return err
	})
	if err == nil {
		t.Fatal("an unbalanced journal was committed")
	}
}

func TestNonNegativeAccountAndPeriodClose(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	topup := ledger.Draft{Key: "t1", Date: now, Description: "topup", Lines: []ledger.Line{ledger.Dr("cash", irr(100)), ledger.Cr("wallet.1", irr(100))}}
	_, err := svc.Post(ctx, topup)
	must(t, err)
	spend := func(key string, amt int64) error {
		_, err := svc.Post(ctx, ledger.Draft{Key: key, Date: now, Description: "spend", Lines: []ledger.Line{ledger.Dr("wallet.1", irr(amt)), ledger.Cr("sales", irr(amt))}})
		return err
	}
	if err := spend("s1", 101); !errors.Is(err, err) || apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("overdraft must be refused, got %v", err)
	}
	must(t, spend("s2", 100))
	if bal, _ := svc.Balance(ctx, "wallet.1", nil); bal.Amount() != 0 {
		t.Fatalf("wallet balance %v", bal)
	}

	// Close the current month: nothing more can be posted into it.
	fys, _ := svc.FiscalYears(ctx)
	for _, p := range fys[0].Periods {
		if !now.Before(p.StartsOn) && now.Before(p.EndsBefore) {
			must(t, svc.SetPeriodStatus(ctx, p.ID, ledger.Closed))
		}
	}
	if _, err := svc.Post(ctx, sale("late", 5)); apperr.KindOf(err) != apperr.KindConflict {
		t.Fatalf("posting into a closed period must be refused, got %v", err)
	}
}

func TestStatementPagingKeepsRunningBalance(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	for i := range 5 {
		_, err := svc.Post(ctx, sale("p"+string(rune('0'+i)), int64(100*(i+1))))
		must(t, err)
	}
	var all []ledger.StatementRow
	after := int64(0)
	for range 10 {
		st, err := svc.Statement(ctx, "cash", ledger.StatementQuery{AfterLineID: after, Limit: 2})
		must(t, err)
		all = append(all, st.Rows...)
		if st.NextAfter == 0 {
			break
		}
		after = st.NextAfter
	}
	if len(all) != 5 || all[4].Running != 1500 || all[1].Running != 300 {
		t.Fatalf("statement: %+v", all)
	}
}

func TestCodingIsOptional(t *testing.T) {
	svc, _ := setup(t)
	ctx := context.Background()
	_, err := svc.Post(ctx, sale("c1", 1000))
	must(t, err)
	rep, err := svc.CodingTrialBalance(ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Nodes) != 0 || len(rep.Unclassified) != 2 {
		t.Fatalf("without a chart everything is unclassified: %+v", rep)
	}
	must(t, svc.ImportCoding(ctx, ledger.IranianCoding()))
	must(t, svc.MapAccount(ctx, "cash", "10101"))
	must(t, svc.MapAccount(ctx, "sales", "60101"))
	rep, err = svc.CodingTrialBalance(ctx, ledger.TrialBalanceQuery{})
	must(t, err)
	if len(rep.Unclassified) != 0 || len(rep.Nodes) == 0 {
		t.Fatalf("mapped chart: %+v", rep)
	}
	if err := svc.MapAccount(ctx, "cash", "99999"); err == nil {
		t.Fatal("mapping to an unknown code must fail")
	}
}
