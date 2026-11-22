package ledger

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// DefaultYearEndChunk is the most lines one year-end document gets before the accounts are split over several
// documents. The database checks every journal for balance per inserted line, so the cost of a journal grows with the
// square of its size; 3000 lines stay in the range of seconds. MaxYearEndAccounts bounds the whole closing, which runs in
// one transaction.
const (
	DefaultYearEndChunk = 3000
	MaxYearEndAccounts  = 100000
)

// RefYearClosing is the Ref.Type of every journal made by closing a fiscal year (the closing documents in the
// closed year and the opening document in the next one). Ref.ID is "<fiscal year id>:<generation>".
const RefYearClosing = "fiscal_year_closing"

// ClosingState is the state of a fiscal year's closing.
type ClosingState string

const (
	ClosingClosed   ClosingState = "closed"
	ClosingReopened ClosingState = "reopened"
)

// YearClosing records that a fiscal year was closed. Generation counts the closings of the year: reopening reverses
// the documents of the current generation and the next closing starts a new one, so no idempotency key is reused.
type YearClosing struct {
	FiscalYearID    int64
	Generation      int
	State           ClosingState
	RetainedAccount string
	Actor           string
}

// YearEndPlan is what closing a fiscal year posts. Lines are in account-code order.
//
// The sequence is the usual Iranian one (verified against accounting-software documentation, not against a law, see
// the README): (1) every revenue and expense account is closed into the temporary profit-and-loss summary account
// (حساب خلاصه سود و زیان); (2) the summary is moved to retained earnings (سود (زیان) انباشته); (3) every remaining
// balance-sheet account is closed (سند اختتامیه); (4) the next fiscal year opens with the mirror image of (3)
// (سند افتتاحیه).
//
// Temporary, Closing and Opening are lists of documents. Normally each is one document; when a document would exceed the
// chunk size it is split, and every part is balanced against a transit account (the profit-and-loss summary for the
// temporary accounts, the closing and opening balance accounts, تراز اختتامیه و افتتاحیه, for the others), which nets to
// zero over all parts.
type YearEndPlan struct {
	FiscalYear      string
	NextYear        string
	Currency        money.Currency
	RetainedAccount string
	SummaryAccount  string
	// TotalRevenue and TotalExpense are the closed balances by side: credit balances and debit balances of the
	// revenue and expense accounts. NetProfit is TotalRevenue-TotalExpense; negative is a loss.
	TotalRevenue int64
	TotalExpense int64
	NetProfit    int64
	Temporary    [][]Line // closes revenue and expense into the summary
	Transfer     []Line   // summary into retained earnings
	Closing      [][]Line // closes the balance-sheet accounts (اختتامیه)
	Opening      [][]Line // the mirror image, dated in the next year (افتتاحیه)
	Blockers     []string
}

// NeedsTransit reports whether the closing or opening documents are split and so need the transit accounts.
func (p YearEndPlan) NeedsTransit() bool { return len(p.Closing) > 1 || len(p.Opening) > 1 }

// PlanInput is the data PlanYearEnd needs: year-scoped rows of the temporary accounts and cumulative rows of the
// permanent ones, both as of the last day of the year.
type PlanInput struct {
	Rows     []TrialBalanceRow // year-scoped for revenue/expense, cumulative for the others
	Currency money.Currency
	Summary  string
	Retained string
	// ClosingTransit and OpeningTransit balance the parts of a split closing and opening document.
	ClosingTransit, OpeningTransit string
	ChunkSize                      int // lines per document; default DefaultYearEndChunk
}

// chunkLines splits lines into documents of at most size lines. Every part that is not balanced by itself gets one more
// line on the balancer account, so each part is a valid journal.
func chunkLines(lines []Line, size int, balancer string, amt func(int64) money.Money) [][]Line {
	if len(lines) == 0 {
		return nil
	}
	if size < 3 {
		size = 3
	}
	var out [][]Line
	for len(lines) > 0 {
		n := min(size-1, len(lines))
		part := append([]Line(nil), lines[:n]...)
		lines = lines[n:]
		var dr, cr int64
		for _, l := range part {
			if l.Side == Debit {
				dr += l.Amount.Amount()
			} else {
				cr += l.Amount.Amount()
			}
		}
		switch {
		case dr > cr:
			part = append(part, Cr(balancer, amt(dr-cr)))
		case cr > dr:
			part = append(part, Dr(balancer, amt(cr-dr)))
		}
		out = append(out, part)
	}
	return out
}

// PlanYearEnd computes the year-end documents. It is pure: a zero balance is skipped, a balance on the unusual side
// (a debit-balance revenue account, a credit-balance asset such as accumulated depreciation) is closed on the side it
// has, and the books must be in one currency.
func PlanYearEnd(in PlanInput) (YearEndPlan, error) {
	p := YearEndPlan{Currency: in.Currency, RetainedAccount: in.Retained, SummaryAccount: in.Summary}
	cur := in.Currency
	amt := func(v int64) money.Money { return money.Must(v, cur) }
	size := in.ChunkSize
	if size <= 0 {
		size = DefaultYearEndChunk
	}

	var temp []Line
	var perm []TrialBalanceRow
	for _, r := range in.Rows {
		net := r.Debit - r.Credit
		switch r.Type {
		case Revenue, Expense:
			switch {
			case net > 0: // debit balance: credit it
				temp = append(temp, Cr(r.Code, amt(net)))
				p.TotalExpense += net
			case net < 0:
				temp = append(temp, Dr(r.Code, amt(-net)))
				p.TotalRevenue += -net
			}
		default:
			if r.Code != in.Summary {
				perm = append(perm, r)
			}
		}
	}
	p.NetProfit = p.TotalRevenue - p.TotalExpense
	if len(temp)+2 <= size {
		if p.TotalExpense > 0 {
			temp = append(temp, Dr(in.Summary, amt(p.TotalExpense)))
		}
		if p.TotalRevenue > 0 {
			temp = append(temp, Cr(in.Summary, amt(p.TotalRevenue)))
		}
		if len(temp) > 0 {
			p.Temporary = [][]Line{temp}
		}
	} else {
		p.Temporary = chunkLines(temp, size, in.Summary, amt)
	}
	switch {
	case p.NetProfit > 0:
		p.Transfer = []Line{Dr(in.Summary, amt(p.NetProfit)), Cr(in.Retained, amt(p.NetProfit))}
	case p.NetProfit < 0:
		p.Transfer = []Line{Dr(in.Retained, amt(-p.NetProfit)), Cr(in.Summary, amt(-p.NetProfit))}
	}

	// The transfer changes the retained-earnings balance that the closing document must carry.
	nets := map[string]int64{}
	var order []string
	for _, r := range perm {
		nets[r.Code] += r.Debit - r.Credit
		order = append(order, r.Code)
	}
	if p.NetProfit != 0 {
		if _, ok := nets[in.Retained]; !ok {
			order = append(order, in.Retained)
		}
		nets[in.Retained] -= p.NetProfit // a profit is a credit
	}
	slices.Sort(order)
	var closing, opening []Line
	for _, code := range order {
		switch net := nets[code]; {
		case net > 0:
			closing = append(closing, Cr(code, amt(net)))
			opening = append(opening, Dr(code, amt(net)))
		case net < 0:
			closing = append(closing, Dr(code, amt(-net)))
			opening = append(opening, Cr(code, amt(-net)))
		}
	}
	if len(closing) > 0 {
		if len(closing) <= size {
			p.Closing, p.Opening = [][]Line{closing}, [][]Line{opening}
		} else {
			p.Closing = chunkLines(closing, size, in.ClosingTransit, amt)
			p.Opening = chunkLines(opening, size, in.OpeningTransit, amt)
		}
	}
	if n := len(temp) + len(closing); n > MaxYearEndAccounts {
		p.Blockers = append(p.Blockers, fmt.Sprintf("too many accounts with a balance (%d, the limit is %d)", n, MaxYearEndAccounts))
	}
	return p, nil
}

// YearEndOptions selects the accounts a closing uses; empty values fall back to the service defaults.
type YearEndOptions struct {
	RetainedAccount string
}

// WithYearEnd sets the default accounts of the closing: the temporary profit-and-loss summary account and the
// retained-earnings account (both equity accounts that must exist).
func WithYearEnd(summary, retained string) Option {
	return func(s *Service) { s.summaryAcct, s.retainedAcct = summary, retained }
}

// WithYearEndTransit sets the closing and opening balance accounts (تراز اختتامیه و افتتاحیه, equity accounts) that
// balance the parts of a closing or opening document that had to be split.
func WithYearEndTransit(closing, opening string) Option {
	return func(s *Service) { s.closingTransit, s.openingTransit = closing, opening }
}

// WithYearEndChunk sets how many lines one year-end document may have before it is split (default DefaultYearEndChunk).
func WithYearEndChunk(n int) Option { return func(s *Service) { s.chunk = n } }

// WithTrail makes the closing and reopening of a fiscal year write an audit entry and an event in their transaction.
func WithTrail(rec trail.Recorder, em trail.Emitter) Option {
	return func(s *Service) { s.audit, s.events = rec, em }
}

func (s *Service) fiscalYear(ctx context.Context, id int64) (FiscalYear, []FiscalYear, error) {
	all, err := s.store.FiscalYears(ctx)
	if err != nil {
		return FiscalYear{}, nil, err
	}
	for _, f := range all {
		if f.ID == id {
			return f, all, nil
		}
	}
	return FiscalYear{}, nil, apperr.NotFound("fiscal year")
}

func lastDay(fy FiscalYear) time.Time { return fy.EndsBefore.AddDate(0, 0, -1) }

// blockers lists why the year cannot be closed now. Other contexts (unsettled payments, draft invoices) are not
// known here: closing them first is the operator's job.
func (s *Service) blockers(fy FiscalYear, all []FiscalYear) []string {
	var b []string
	if fy.Status == Closed {
		b = append(b, "the fiscal year is already closed")
	}
	if !s.Today().After(lastDay(fy)) {
		b = append(b, "the fiscal year is not over yet (it ends on "+lastDay(fy).Format("2006-01-02")+")")
	}
	for _, o := range all {
		if o.StartsOn.Before(fy.StartsOn) && o.Status != Closed {
			b = append(b, "the earlier fiscal year "+o.Name+" must be closed first")
		}
		if o.StartsOn.Equal(fy.EndsBefore) && o.Status == Closed {
			b = append(b, "the next fiscal year "+o.Name+" is closed")
		}
	}
	return b
}

func (s *Service) accountChecks(ctx context.Context, codes []string, retained, summary string) (money.Currency, []string, error) {
	accts, err := s.store.AccountsByCode(ctx, append([]string{retained, summary}, codes...))
	if err != nil {
		return "", nil, err
	}
	var b []string
	r, ok := accts[retained]
	switch {
	case retained == "":
		return "", []string{"no retained-earnings account is configured"}, nil
	case !ok:
		return "", []string{"the retained-earnings account " + retained + " does not exist"}, nil
	case r.Type != Equity:
		b = append(b, "the retained-earnings account "+retained+" must be an equity account")
	}
	if a, ok := accts[summary]; !ok || a.Type != Equity {
		b = append(b, "the profit-and-loss summary account "+summary+" must exist and be an equity account")
	}
	for _, c := range codes {
		a := accts[c]
		switch {
		case a.Currency != r.Currency:
			b = append(b, "account "+c+" is not in "+string(r.Currency)+": the books must use one currency to be closed")
		case !a.Active:
			b = append(b, "account "+c+" has a balance but is inactive: reactivate it before closing")
		}
	}
	if !r.Active {
		b = append(b, "the retained-earnings account "+retained+" is inactive")
	}
	return r.Currency, b, nil
}

// plan reads the books and computes the documents; it posts nothing.
func (s *Service) plan(ctx context.Context, fy FiscalYear, all []FiscalYear, opt YearEndOptions) (YearEndPlan, error) {
	retained := opt.RetainedAccount
	if retained == "" {
		retained = s.retainedAcct
	}
	end := lastDay(fy)
	start := fy.StartsOn
	scoped, err := s.store.TrialBalance(ctx, TrialBalanceQuery{From: &start, To: &end})
	if err != nil {
		return YearEndPlan{}, err
	}
	cumulative, err := s.store.TrialBalance(ctx, TrialBalanceQuery{To: &end})
	if err != nil {
		return YearEndPlan{}, err
	}
	var rows []TrialBalanceRow
	var codes []string
	for _, r := range scoped {
		if r.Type == Revenue || r.Type == Expense {
			rows = append(rows, r)
			codes = append(codes, r.Code)
		}
	}
	for _, r := range cumulative {
		if r.Type != Revenue && r.Type != Expense {
			rows = append(rows, r)
			if r.Debit != r.Credit {
				codes = append(codes, r.Code)
			}
		}
	}
	cur, accBlockers, err := s.accountChecks(ctx, codes, retained, s.summaryAcct)
	if err != nil {
		return YearEndPlan{}, err
	}
	p := YearEndPlan{FiscalYear: fy.Name, RetainedAccount: retained, SummaryAccount: s.summaryAcct, Currency: cur}
	if cur != "" {
		if p, err = PlanYearEnd(PlanInput{Rows: rows, Currency: cur, Summary: s.summaryAcct, Retained: retained,
			ClosingTransit: s.closingTransit, OpeningTransit: s.openingTransit, ChunkSize: s.chunk}); err != nil {
			return YearEndPlan{}, err
		}
		if p.NeedsTransit() {
			ta, err := s.store.AccountsByCode(ctx, []string{s.closingTransit, s.openingTransit})
			if err != nil {
				return YearEndPlan{}, err
			}
			for _, c := range []string{s.closingTransit, s.openingTransit} {
				if a, ok := ta[c]; !ok || a.Type != Equity || !a.Active || a.Currency != cur {
					p.Blockers = append(p.Blockers, "the closing needs several documents, so the active equity transit account "+c+" must exist")
				}
			}
		}
	}
	p.FiscalYear = fy.Name
	if jd, err := jalali.FromTime(fy.EndsBefore); err == nil {
		p.NextYear = strconv.Itoa(jd.Year)
	}
	p.Blockers = append(append(p.Blockers, s.blockers(fy, all)...), accBlockers...)
	return p, nil
}

// PreviewYearEnd shows what closing the fiscal year would post, and what stops it, without posting anything.
func (s *Service) PreviewYearEnd(ctx context.Context, fiscalYearID int64, opt YearEndOptions) (YearEndPlan, error) {
	fy, all, err := s.fiscalYear(ctx, fiscalYearID)
	if err != nil {
		return YearEndPlan{}, err
	}
	return s.plan(ctx, fy, all, opt)
}

// CloseRequest closes a fiscal year.
type CloseRequest struct {
	RetainedAccount string // default: the service's retained-earnings account
	Actor           string
}

// YearEndResult is a closed fiscal year's documents. A list is empty when there was nothing to post; each list has
// one document unless the accounts were split (see YearEndPlan).
type YearEndResult struct {
	FiscalYear    string
	Generation    int
	AlreadyClosed bool
	NetProfit     int64
	Temporary     []Journal
	Transfer      *Journal
	Closing       []Journal
	Opening       []Journal
}

// Journals lists every document of the closing in the order they were made.
func (r YearEndResult) Journals() []Journal {
	out := append([]Journal(nil), r.Temporary...)
	if r.Transfer != nil {
		out = append(out, *r.Transfer)
	}
	out = append(out, r.Closing...)
	return append(out, r.Opening...)
}

func closingKey(fyID int64, gen int, part string) string {
	return fmt.Sprintf("yearend:%d:%d:%s", fyID, gen, part)
}

func closingRef(fyID int64, gen int) Ref {
	return Ref{Type: RefYearClosing, ID: fmt.Sprintf("%d:%d", fyID, gen)}
}

// partOf extracts "temp-2" from a closing key.
func partOf(key string) string {
	i := strings.LastIndex(key, ":")
	return key[i+1:]
}

func (s *Service) loadResult(ctx context.Context, fy FiscalYear, gen int) (YearEndResult, error) {
	js, err := s.store.JournalsByRef(ctx, closingRef(fy.ID, gen))
	if err != nil {
		return YearEndResult{}, err
	}
	res := YearEndResult{FiscalYear: fy.Name, Generation: gen}
	type part struct {
		n int
		j Journal
	}
	lists := map[string][]part{}
	for _, j := range js {
		if j.Kind == KindReversal {
			continue
		}
		name, num, _ := strings.Cut(partOf(j.Key), "-")
		n, _ := strconv.Atoi(num)
		if name == "transfer" {
			j := j
			res.Transfer = &j
			continue
		}
		lists[name] = append(lists[name], part{n, j})
	}
	sorted := func(name string) []Journal {
		ps := lists[name]
		slices.SortFunc(ps, func(a, b part) int { return a.n - b.n })
		out := make([]Journal, len(ps))
		for i, p := range ps {
			out[i] = p.j
		}
		return out
	}
	res.Temporary, res.Closing, res.Opening = sorted("temp"), sorted("closing"), sorted("opening")
	return res, nil
}

// CloseYear closes a fiscal year: it posts the closing documents (kind closing) in the year, the opening document
// (kind opening) in the next year, creating that year when needed, closes the year's periods and the year itself.
// All of it is one transaction. It is idempotent per fiscal year: a year that is already closed returns its
// documents with AlreadyClosed set, so two concurrent requests produce exactly one set of documents.
func (s *Service) CloseYear(ctx context.Context, fiscalYearID int64, req CloseRequest) (YearEndResult, error) {
	var out YearEndResult
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.store.LockFiscalYear(ctx, fiscalYearID)
		if err != nil {
			return err
		}
		rec, found, err := s.store.YearClosing(ctx, locked.ID)
		if err != nil {
			return err
		}
		if found && rec.State == ClosingClosed {
			out, err = s.loadResult(ctx, locked, rec.Generation)
			out.AlreadyClosed = true
			return err
		}
		fy, all, err := s.fiscalYear(ctx, locked.ID)
		if err != nil {
			return err
		}
		p, err := s.plan(ctx, fy, all, YearEndOptions{RetainedAccount: req.RetainedAccount})
		if err != nil {
			return err
		}
		if len(p.Blockers) > 0 {
			return apperr.New(apperr.KindConflict, "year_not_closable", "the fiscal year cannot be closed: "+strings.Join(p.Blockers, "; "))
		}
		gen := 1
		if found {
			gen = rec.Generation + 1
		}

		next, err := s.nextYear(ctx, fy)
		if err != nil {
			return err
		}
		// A month that was closed by hand must take the closing documents of the last day.
		for _, per := range fy.Periods {
			if per.Number == len(fy.Periods) && per.Status == Closed {
				if err := s.store.SetPeriodStatus(ctx, per.ID, Open); err != nil {
					return err
				}
			}
		}

		ref, end, begin := closingRef(fy.ID, gen), lastDay(fy), fy.EndsBefore
		post := func(part string, date time.Time, kind Kind, desc string, lines []Line) (Journal, error) {
			return s.Post(ctx, Draft{Key: closingKey(fy.ID, gen, part), Date: date, Kind: kind, Ref: ref, Actor: req.Actor, Lines: lines,
				Description: fmt.Sprintf("Year-end %s, fiscal year %s", desc, fy.Name)})
		}
		postAll := func(name string, date time.Time, kind Kind, desc string, docs [][]Line) ([]Journal, error) {
			var out []Journal
			for i, lines := range docs {
				d := desc
				if len(docs) > 1 {
					d = fmt.Sprintf("%s (part %d of %d)", desc, i+1, len(docs))
				}
				j, err := post(fmt.Sprintf("%s-%d", name, i+1), date, kind, d, lines)
				if err != nil {
					return nil, err
				}
				out = append(out, j)
			}
			return out, nil
		}
		res := YearEndResult{FiscalYear: fy.Name, Generation: gen, NetProfit: p.NetProfit}
		if res.Temporary, err = postAll("temp", end, KindClosing, "closing of revenue and expense accounts into the profit and loss summary", p.Temporary); err != nil {
			return err
		}
		if len(p.Transfer) > 0 {
			j, err := post("transfer", end, KindClosing, "transfer of the profit or loss to retained earnings", p.Transfer)
			if err != nil {
				return err
			}
			res.Transfer = &j
		}
		if res.Closing, err = postAll("closing", end, KindClosing, "closing of balance-sheet accounts", p.Closing); err != nil {
			return err
		}
		if res.Opening, err = postAll("opening", begin, KindOpening, "opening balances carried forward to "+next.Name+" from", p.Opening); err != nil {
			return err
		}
		for _, per := range fy.Periods {
			if err := s.store.SetPeriodStatus(ctx, per.ID, Closed); err != nil {
				return err
			}
		}
		if err := s.store.SetFiscalYearStatus(ctx, fy.ID, Closed); err != nil {
			return err
		}
		if err := s.store.SaveYearClosing(ctx, YearClosing{FiscalYearID: fy.ID, Generation: gen, State: ClosingClosed, RetainedAccount: p.RetainedAccount, Actor: req.Actor}); err != nil {
			return err
		}
		out = res
		return s.announce(ctx, "ledger.fiscal_year_closed", "fiscal_year.close", fy, req.Actor, map[string]any{
			"fiscal_year": fy.Name, "next_fiscal_year": next.Name, "generation": gen, "net_profit": p.NetProfit,
			"currency": string(p.Currency), "retained_account": p.RetainedAccount, "documents": len(res.Journals())})
	})
	return out, err
}

// nextYear returns the fiscal year that starts right after fy, creating it when missing.
func (s *Service) nextYear(ctx context.Context, fy FiscalYear) (FiscalYear, error) {
	jd, err := jalali.FromTime(fy.EndsBefore)
	if err != nil {
		return FiscalYear{}, apperr.Invalid("the next fiscal year is out of the supported range")
	}
	next, err := s.OpenFiscalYear(ctx, jd.Year)
	if err != nil {
		return FiscalYear{}, err
	}
	if !next.StartsOn.Equal(fy.EndsBefore) {
		return FiscalYear{}, apperr.Conflict("the fiscal year " + next.Name + " does not start right after " + fy.Name)
	}
	if next.Status == Closed {
		return FiscalYear{}, apperr.Conflict("the next fiscal year " + next.Name + " is closed")
	}
	return next, nil
}

func (s *Service) announce(ctx context.Context, topic, action string, fy FiscalYear, actor string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: "fiscal_year", EntityID: strconv.FormatInt(fy.ID, 10), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: "fiscal_year", AggregateID: strconv.FormatInt(fy.ID, 10), Payload: details})
}

// ReopenRequest undoes the closing of a fiscal year.
type ReopenRequest struct {
	Reason string
	Actor  string
}

// ReopenYear undoes a closing with reversing journals only (the ledger stays append-only): the opening document
// and then the closing documents are reversed on their own dates, and the year is opened again. It is allowed only
// while the next fiscal year holds no postings except the opening document. Only the last month of the year is
// reopened; reopen earlier months with SetPeriodStatus when a correction needs them. Closing the year again later
// posts new documents.
func (s *Service) ReopenYear(ctx context.Context, fiscalYearID int64, req ReopenRequest) (YearEndResult, error) {
	var out YearEndResult
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		locked, err := s.store.LockFiscalYear(ctx, fiscalYearID)
		if err != nil {
			return err
		}
		rec, found, err := s.store.YearClosing(ctx, locked.ID)
		if err != nil {
			return err
		}
		if !found || rec.State != ClosingClosed {
			return apperr.New(apperr.KindConflict, "not_closed_by_year_end", "the fiscal year has no year-end closing to reverse")
		}
		fy, all, err := s.fiscalYear(ctx, locked.ID)
		if err != nil {
			return err
		}
		res, err := s.loadResult(ctx, fy, rec.Generation)
		if err != nil {
			return err
		}
		var except []uuid.UUID
		for _, j := range res.Opening {
			except = append(except, j.ID)
		}
		for _, o := range all {
			if !o.StartsOn.Equal(fy.EndsBefore) {
				continue
			}
			if _, err := s.store.LockFiscalYear(ctx, o.ID); err != nil {
				return err
			}
			if o.Status == Closed {
				return apperr.New(apperr.KindConflict, "next_year_closed", "the next fiscal year is closed: reopen it first")
			}
			n, err := s.store.CountJournals(ctx, o.ID, except)
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.New(apperr.KindConflict, "next_year_has_postings", "the next fiscal year already has postings besides the opening document, so the closing cannot be reversed")
			}
		}

		if err := s.store.SetFiscalYearStatus(ctx, fy.ID, Open); err != nil {
			return err
		}
		for _, per := range fy.Periods {
			if per.Number == len(fy.Periods) {
				if err := s.store.SetPeriodStatus(ctx, per.ID, Open); err != nil {
					return err
				}
			}
		}
		reason := "reopening of fiscal year " + fy.Name
		if req.Reason != "" {
			reason += ": " + req.Reason
		}
		// Opening first: it restores the balances the closing document zeroed, and a wallet balance cannot go below zero.
		docs := slices.Clone(res.Journals())
		slices.Reverse(docs)
		for _, j := range docs {
			if _, err := s.Reverse(ctx, j.ID, ReverseRequest{Key: closingKey(fy.ID, rec.Generation, "reopen:"+partOf(j.Key)), Date: j.Date, Reason: reason, Actor: req.Actor}); err != nil {
				return err
			}
		}
		rec.State, rec.Actor = ClosingReopened, req.Actor
		if err := s.store.SaveYearClosing(ctx, rec); err != nil {
			return err
		}
		out = res
		return s.announce(ctx, "ledger.fiscal_year_reopened", "fiscal_year.reopen", fy, req.Actor, map[string]any{
			"fiscal_year": fy.Name, "generation": rec.Generation, "reason": req.Reason})
	})
	return out, err
}

// IncomeStatement is the profit and loss of a date range (صورت سود و زیان).
type IncomeStatement struct {
	Revenue      []TrialBalanceRow
	Expense      []TrialBalanceRow
	TotalRevenue int64 // natural balances; a contra account (sales returns) reduces it
	TotalExpense int64
	Net          int64 // TotalRevenue-TotalExpense; negative is a loss
}

// IncomeStatement reports revenue and expense accounts for the range. Year-end closing journals are left out
// unless includeClosing is set, so a closed year still shows its real result.
func (s *Service) IncomeStatement(ctx context.Context, from, to *time.Time, includeClosing bool) (IncomeStatement, error) {
	rows, err := s.store.TrialBalance(ctx, TrialBalanceQuery{From: from, To: to, ExcludeClosing: !includeClosing})
	if err != nil {
		return IncomeStatement{}, err
	}
	var st IncomeStatement
	for _, r := range rows {
		switch r.Type {
		case Revenue:
			st.Revenue = append(st.Revenue, r)
			st.TotalRevenue += r.Balance()
		case Expense:
			st.Expense = append(st.Expense, r)
			st.TotalExpense += r.Balance()
		}
	}
	st.Net = st.TotalRevenue - st.TotalExpense
	return st, nil
}
