package fixedasset

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// ComponentMinBP is the share of the total cost a separately depreciated part must reach (regulation art. 5: a part of at least 20% of the cost whose life differs).
// A configurable default: Config.ComponentMinBP overrides it.
const ComponentMinBP = 2000

// ExpenseFractionBP is the share of the small-transaction limit below which a whole asset may be expensed (10%, as told by the owner for art. 12); Config.ExpenseFractionBP overrides.
const ExpenseFractionBP = 1000

// Threshold is the small-transaction limit in force from a date (the cabinet decision on the yearly limits of government transactions), the base of the art. 12 expensing limit.
type Threshold struct {
	ID            uuid.UUID
	EffectiveFrom time.Time
	SmallLimit    int64 // rials
	Verified      bool  // true only when read from the official text
	Source        string
	CreatedAt     time.Time
}

// ---- units of production ----

// RecordUsage stores the units produced or used by an asset in a Jalali month (replacing an earlier figure). It must be recorded before that month is depreciated.
func (s *Service) RecordUsage(ctx context.Context, assetID uuid.UUID, year, month int, units int64, actor string) error {
	if units < 0 {
		return apperr.InvalidField("units", "must not be negative")
	}
	if month < 1 || month > 12 {
		return apperr.InvalidField("month", "must be 1 to 12")
	}
	period := PeriodIndex(year, month)
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Asset(ctx, assetID, true)
		if err != nil {
			return err
		}
		if a.Method != UnitsOfProduction {
			return apperr.New(apperr.KindConflict, "not_units_asset", "the asset does not depreciate by units of production")
		}
		if a.ThroughIndex >= period {
			return apperr.New(apperr.KindConflict, "month_depreciated", "that month is already depreciated: void its run first")
		}
		if err := s.repo.SetUsage(ctx, assetID, period, units, actor); err != nil {
			return err
		}
		return s.note(ctx, actor, "fixedasset.usage_recorded", "fixed_asset", assetID, "fixedasset.usage.recorded", map[string]any{"year": year, "month": month, "units": units})
	})
}

// ---- void a run ----

// VoidRun reverses the latest run of a Jalali month: a reversing journal, the assets' accumulated depreciation and counters go back, and the run is marked voided (history is kept).
// It is refused when any asset of the run was depreciated again for a later month, or disposed since; void the later runs first.
func (s *Service) VoidRun(ctx context.Context, year, month int, actor string) (Run, error) {
	if month < 1 || month > 12 {
		return Run{}, apperr.InvalidField("month", "must be 1 to 12")
	}
	period := PeriodIndex(year, month)
	var out Run
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		run, ok, err := s.repo.LatestRun(ctx, period)
		if err != nil {
			return err
		}
		if !ok || len(run.Items) == 0 {
			return apperr.New(apperr.KindConflict, "no_run", "there is no depreciation run to void for that month")
		}
		perAcc := map[string]int64{}
		var total int64
		for _, it := range run.Items {
			a, err := s.repo.Asset(ctx, it.AssetID, true)
			if err != nil {
				return err
			}
			if a.Status != Active || a.ThroughIndex != period {
				return apperr.New(apperr.KindConflict, "asset_moved_on", "asset "+a.Code+" was depreciated again or disposed after this run")
			}
			a.Accumulated = s.amt(a.Accumulated.Amount() - it.Amount.Amount())
			a.MonthsDone--
			a.ProgressMilli -= it.ProgressMilli
			a.ThroughIndex = period - 1
			if a.MonthsInYear > 0 {
				a.MonthsInYear--
			}
			if err := s.repo.UpdateAsset(ctx, &a); err != nil {
				return err
			}
			perAcc[a.AccDepAccount] += it.Amount.Amount()
			total += it.Amount.Amount()
		}
		lines := []books.Line{books.Cr(s.cfg.Accounts.DepreciationExpense, s.amt(total))}
		for acct, v := range perAcc {
			lines = append(lines, books.Dr(acct, s.amt(v)))
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: fmt.Sprintf("fixedasset:dep:%d:%d:void", period, run.Seq), Date: dateOnly(s.books.Today()),
			Description: fmt.Sprintf("Reversal of depreciation %d/%02d", year, month), RefType: "fixed_asset_run", RefID: fmt.Sprintf("%d:%d", period, run.Seq), Actor: actor, Lines: lines})
		if err != nil {
			return err
		}
		if err := s.repo.MarkRunVoided(ctx, period, run.Seq, jid, s.clock()); err != nil {
			return err
		}
		out = run
		return s.note(ctx, actor, "fixedasset.run_voided", "fixed_asset_run", uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%d:%d", period, run.Seq))), "fixedasset.depreciation.voided",
			map[string]any{"year": year, "month": month, "total": total, "journal_id": jid})
	})
	return out, err
}

// ---- partial disposal ----

// PartialDisposeInput sells or scraps a share of an asset.
type PartialDisposeInput struct {
	IdemKey         string
	PortionBP       int // share of the asset leaving, in basis points of its cost (1..9999)
	Date            time.Time
	Proceeds        money.Money
	ProceedsAccount string
	Actor           string
}

// DisposePartial retires a share of an asset: that share of the cost and of the accumulated depreciation leaves the books, the proceeds come in, the difference is the gain or loss; the
// asset stays active with the reduced cost, salvage and accumulated depreciation and keeps depreciating. The same preconditions as Dispose apply. Idempotent by key.
func (s *Service) DisposePartial(ctx context.Context, id uuid.UUID, in PartialDisposeInput) (Asset, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return Asset{}, apperr.InvalidField("idempotency_key", "required")
	}
	if in.PortionBP < 1 || in.PortionBP > 9999 {
		return Asset{}, apperr.InvalidField("portion_bp", "must be between 1 and 9999: use Dispose for the whole asset")
	}
	if in.Proceeds.Currency() != s.cfg.Currency || in.Proceeds.Amount() < 0 {
		return Asset{}, apperr.InvalidField("proceeds", "must be zero or a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date, "date")
	if err != nil {
		return Asset{}, err
	}
	idx, err := monthIndex(date)
	if err != nil {
		return Asset{}, err
	}
	into := in.ProceedsAccount
	if into == "" {
		into = s.cfg.Accounts.Bank
	}
	var out Asset
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Asset(ctx, id, true)
		if err != nil {
			return err
		}
		if prior, found, err := s.repo.PartialByKey(ctx, in.IdemKey); err != nil {
			return err
		} else if found {
			if prior != a.ID {
				return apperr.New(apperr.KindConflict, "idempotency_key_reused", "the idempotency key was used for a different asset")
			}
			out = a // a retry of a partial disposal that went through
			return nil
		}
		if a.Status != Active {
			return apperr.Conflict("the asset is disposed")
		}
		if a.ThroughIndex >= idx {
			return apperr.New(apperr.KindConflict, "depreciation_after_disposal", "depreciation is already booked for the disposal month or later")
		}
		if date.Before(a.InServiceOn) {
			return apperr.InvalidField("date", "must not be before the asset entered service")
		}
		if need := idx - 1; a.StartIndex <= need && a.ThroughIndex < need {
			return apperr.New(apperr.KindConflict, "depreciation_behind", "book depreciation up to the month before the disposal first")
		}
		bp := int64(in.PortionBP)
		cost, accum := a.Cost.Amount(), a.Accumulated.Amount()
		pc, pa := roundDiv(cost, bp, 10000), roundDiv(accum, bp, 10000)
		ps, py := roundDiv(a.Salvage.Amount(), bp, 10000), roundDiv(a.YearStartAccumulated.Amount(), bp, 10000)
		if pc <= 0 || pc >= cost {
			return apperr.InvalidField("portion_bp", "the share rounds to nothing or to the whole asset")
		}
		diff := in.Proceeds.Amount() + pa - pc
		lines := []books.Line{books.Cr(a.CostAccount, s.amt(pc))}
		if pa > 0 {
			lines = append(lines, books.Dr(a.AccDepAccount, s.amt(pa)))
		}
		if in.Proceeds.Amount() > 0 {
			lines = append(lines, books.Dr(into, in.Proceeds))
		}
		switch {
		case diff > 0:
			lines = append(lines, books.Cr(s.cfg.Accounts.DisposalGainLoss, s.amt(diff)))
		case diff < 0:
			lines = append(lines, books.Dr(s.cfg.Accounts.DisposalGainLoss, s.amt(-diff)))
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: "fixedasset:" + a.ID.String() + ":partial:" + in.IdemKey, Date: date, Description: "Partial disposal of fixed asset " + a.Code,
			RefType: "fixed_asset", RefID: a.ID.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		if _, _, err := s.repo.InsertPartial(ctx, in.IdemKey, a.ID, in.PortionBP, jid); err != nil {
			return err
		}
		a.Cost, a.Accumulated = s.amt(cost-pc), s.amt(accum-pa)
		a.Salvage, a.YearStartAccumulated = s.amt(a.Salvage.Amount()-ps), s.amt(a.YearStartAccumulated.Amount()-py)
		if err := s.repo.ReduceAsset(ctx, &a); err != nil {
			return err
		}
		out = a
		return s.note(ctx, in.Actor, "fixedasset.partially_disposed", "fixed_asset", a.ID, "fixedasset.asset.partially_disposed",
			map[string]any{"code": a.Code, "portion_bp": in.PortionBP, "cost_out": pc, "proceeds": in.Proceeds.Amount(), "gain_loss": diff})
	})
	return out, err
}

// ---- components (art. 5) ----

// Component is one separately depreciated part of a composite asset.
type Component struct {
	Suffix     string // added to the code: CODE-SUFFIX
	CategoryID uuid.UUID
	Cost       money.Money
}

// AcquireComponentsInput registers a composite asset as parts with their own categories. The FIRST part is the main body and takes whatever is not in the other parts.
type AcquireComponentsInput struct {
	IdemKey     string
	Code, Name  string
	AcquiredOn  time.Time
	InServiceOn time.Time
	VAT         money.Money // split by cost, the rest on the main body
	PaidFrom    string
	Components  []Component
	Actor       string
}

// AcquireComponents registers the parts of a composite asset (regulation art. 5): every part except the main body must reach Config.ComponentMinBP (default 20%) of the total cost and
// its depreciation terms must differ from the main body's, otherwise there is nothing to separate (409). Each part is a normal asset with its own category, acquired under the
// key IdemKey:n; all in one transaction.
func (s *Service) AcquireComponents(ctx context.Context, in AcquireComponentsInput) ([]Asset, error) {
	if len(in.Components) < 2 {
		return nil, apperr.InvalidField("components", "at least two parts: the main body and one separate part")
	}
	var total int64
	for _, c := range in.Components {
		if c.Cost.Currency() != s.cfg.Currency || !c.Cost.IsPositive() {
			return nil, apperr.InvalidField("components.cost", "must be positive amounts in "+string(s.cfg.Currency))
		}
		total += c.Cost.Amount()
	}
	minBP := s.cfg.ComponentMinBP
	if minBP == 0 {
		minBP = ComponentMinBP
	}
	var out []Asset
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		main, err := s.repo.Category(ctx, in.Components[0].CategoryID)
		if err != nil {
			return err
		}
		shares := make([]int64, len(in.Components))
		var others int64
		for i := 1; i < len(in.Components); i++ {
			shares[i] = in.VAT.Amount() * in.Components[i].Cost.Amount() / total
			others += shares[i]
		}
		shares[0] = in.VAT.Amount() - others
		for i, c := range in.Components {
			if i > 0 {
				cat, err := s.repo.Category(ctx, c.CategoryID)
				if err != nil {
					return err
				}
				if c.Cost.Amount()*10000 < total*int64(minBP) {
					return apperr.New(apperr.KindConflict, "component_too_small", fmt.Sprintf("part %q is below %d basis points of the total cost: keep it in the main body", c.Suffix, minBP))
				}
				if cat.Terms == main.Terms {
					return apperr.New(apperr.KindConflict, "component_same_life", fmt.Sprintf("part %q depreciates like the main body: nothing to separate", c.Suffix))
				}
			}
			ai := AcquireInput{IdemKey: fmt.Sprintf("%s:%d", in.IdemKey, i), Code: in.Code + "-" + c.Suffix, Name: in.Name + " / " + c.Suffix, CategoryID: c.CategoryID,
				AcquiredOn: in.AcquiredOn, InServiceOn: in.InServiceOn, Cost: c.Cost, PaidFrom: in.PaidFrom, Actor: in.Actor}
			if shares[i] > 0 {
				ai.VAT = s.amt(shares[i])
			}
			a, err := s.Acquire(ctx, ai)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return nil
	})
	return out, err
}

// ---- art. 12: small assets ----

// AddThreshold stores a small-transaction limit version (immutable).
func (s *Service) AddThreshold(ctx context.Context, from time.Time, smallLimit int64, verified bool, source, actor string) (Threshold, error) {
	if from.IsZero() || smallLimit <= 0 || strings.TrimSpace(source) == "" {
		return Threshold{}, apperr.Invalid("effective_from, a positive small_limit and a source are required")
	}
	t := Threshold{ID: uuid.New(), EffectiveFrom: dateOnly(from), SmallLimit: smallLimit, Verified: verified, Source: strings.TrimSpace(source), CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertThreshold(ctx, t); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "fixedasset.threshold_added", EntityType: "fixed_asset_threshold", EntityID: t.ID.String(),
			Details: map[string]any{"effective_from": t.EffectiveFrom.Format("2006-01-02"), "small_limit": smallLimit}})
	})
	return t, err
}

// Thresholds lists the versions.
func (s *Service) Thresholds(ctx context.Context) ([]Threshold, error) { return s.repo.Thresholds(ctx) }

// ExpenseLimit is the cost below which an asset bought on a day may be expensed: ExpenseFractionBP of the small-transaction limit in force.
func (s *Service) ExpenseLimit(ctx context.Context, day time.Time) (int64, Threshold, error) {
	ts, err := s.repo.Thresholds(ctx)
	if err != nil {
		return 0, Threshold{}, err
	}
	var best Threshold
	found := false
	for _, t := range ts {
		if !t.EffectiveFrom.After(day) && (!found || t.EffectiveFrom.After(best.EffectiveFrom)) {
			best, found = t, true
		}
	}
	if !found {
		return 0, Threshold{}, apperr.New(apperr.KindConflict, "no_threshold", "no small-transaction limit applies to that date: add one")
	}
	f := s.cfg.ExpenseFractionBP
	if f == 0 {
		f = ExpenseFractionBP
	}
	return best.SmallLimit * int64(f) / 10000, best, nil
}

// ExpenseSmallInput buys an asset that is expensed at once.
type ExpenseSmallInput struct {
	IdemKey  string
	Name     string
	Date     time.Time
	Cost     money.Money
	VAT      money.Money
	PaidFrom string
	Actor    string
}

// ExpenseSmall books the purchase of an asset whose whole cost is below the art. 12 limit straight to expense (Dr small-assets expense, Dr input VAT, Cr paid-from) instead of
// registering it for depreciation. At or above the limit it is refused (409 above_limit): register it as a normal asset. Idempotent by key (the books' key).
func (s *Service) ExpenseSmall(ctx context.Context, in ExpenseSmallInput) (uuid.UUID, error) {
	if strings.TrimSpace(in.IdemKey) == "" || strings.TrimSpace(in.Name) == "" {
		return uuid.Nil, apperr.Invalid("idempotency key and name are required")
	}
	if s.cfg.Accounts.SmallAssetExpense == "" {
		return uuid.Nil, apperr.New(apperr.KindConflict, "no_expense_account", "no small-assets expense account configured")
	}
	if in.Cost.Currency() != s.cfg.Currency || !in.Cost.IsPositive() {
		return uuid.Nil, apperr.InvalidField("cost", "must be a positive amount in "+string(s.cfg.Currency))
	}
	date, err := s.day(in.Date, "date")
	if err != nil {
		return uuid.Nil, err
	}
	var jid uuid.UUID
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		limit, th, err := s.ExpenseLimit(ctx, date)
		if err != nil {
			return err
		}
		if in.Cost.Amount() >= limit {
			return apperr.New(apperr.KindConflict, "above_limit", fmt.Sprintf("the cost %d is not below the expensing limit %d: register it as an asset", in.Cost.Amount(), limit))
		}
		paid := in.PaidFrom
		if paid == "" {
			paid = s.cfg.Accounts.Bank
		}
		gross := in.Cost.Amount() + in.VAT.Amount()
		lines := []books.Line{books.Dr(s.cfg.Accounts.SmallAssetExpense, in.Cost)}
		if in.VAT.Amount() > 0 {
			lines = append(lines, books.Dr(s.cfg.Accounts.VATInput, in.VAT))
		}
		lines = append(lines, books.Cr(paid, s.amt(gross)))
		jid, err = s.books.Post(ctx, books.Entry{Key: "fixedasset:small:" + in.IdemKey, Date: date, Description: "Small asset expensed: " + in.Name, RefType: "fixed_asset_small", RefID: in.IdemKey,
			Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: in.Actor, Action: "fixedasset.small_expensed", EntityType: "fixed_asset_small", EntityID: in.IdemKey,
			Details: map[string]any{"cost": in.Cost.Amount(), "limit": limit, "threshold_effective": th.EffectiveFrom.Format("2006-01-02")}})
	})
	return jid, err
}
