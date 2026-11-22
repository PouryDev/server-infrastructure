package fixedasset

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/books"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Accounts are the ledger accounts the service posts to. The composition root creates the shared ones.
type Accounts struct {
	DepreciationExpense string // expense: depreciation of the period
	DisposalGainLoss    string // expense account: debit = loss on disposal, credit = gain
	Bank                string // default account of purchases and disposal proceeds
	VATInput            string // asset: deductible input tax of purchases (required only when an acquisition carries VAT)
	SmallAssetExpense   string // expense: small assets expensed at purchase (art. 12); required only for ExpenseSmall
}

// Config of the service.
type Config struct {
	Accounts Accounts
	Currency money.Currency
	// ComponentMinBP is the least share of the total cost of a separately depreciated part (default 2000 = 20%, regulation art. 5).
	ComponentMinBP int
	// ExpenseFractionBP is the share of the small-transaction limit under which an asset may be expensed (default 1000 = 10%, art. 12).
	ExpenseFractionBP int
	// IdleMinMonths: an asset idle for MORE than this many consecutive months in one fiscal year gets the idle treatment (default 6, regulation art. 6 remark).
	IdleMinMonths int
	// IdleRateBP: share of the normal depreciation taken for the idle months (default 3000 = 30%, art. 6 remark).
	IdleRateBP int
}

// Service is the fixed-asset application layer.
type Service struct {
	repo   Repository
	tx     Transactor
	books  books.Poster
	events trail.Emitter
	audit  trail.Recorder
	cfg    Config
	clock  func() time.Time
}

// Option customises the Service.
type Option func(*Service)

// WithClock replaces time.Now (tests).
func WithClock(f func() time.Time) Option { return func(s *Service) { s.clock = f } }

// NewService creates the service.
func NewService(repo Repository, tx Transactor, b books.Poster, events trail.Emitter, audit trail.Recorder, cfg Config, opts ...Option) *Service {
	if cfg.Currency == "" {
		cfg.Currency = money.IRR
	}
	s := &Service{repo: repo, tx: tx, books: b, events: events, audit: audit, cfg: cfg, clock: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *Service) note(ctx context.Context, actor, action, entity string, id uuid.UUID, topic string, details map[string]any) error {
	if err := s.audit.Record(ctx, trail.Entry{Actor: actor, Action: action, EntityType: entity, EntityID: id.String(), Details: details}); err != nil {
		return err
	}
	return s.events.Emit(ctx, trail.Event{Topic: topic, AggregateType: entity, AggregateID: id.String(), Payload: details})
}

func (s *Service) amt(v int64) money.Money { return money.Must(v, s.cfg.Currency) }

// day validates a date: today when empty, never in the future.
func (s *Service) day(t time.Time, field string) (time.Time, error) {
	today := dateOnly(s.books.Today())
	if t.IsZero() {
		return today, nil
	}
	if t = dateOnly(t); t.After(today) {
		return time.Time{}, apperr.InvalidField(field, "must not be in the future")
	}
	return t, nil
}

func monthIndex(t time.Time) (int, error) {
	d, err := jalali.FromTime(t)
	if err != nil {
		return 0, apperr.Invalid("date out of the supported range")
	}
	return PeriodIndex(d.Year, d.Month), nil
}

// ---- categories ----

// CreateCategory adds a category and opens its two ledger accounts (cost and accumulated depreciation).
func (s *Service) CreateCategory(ctx context.Context, in CategoryInput, actor string) (Category, error) {
	in, err := in.validate()
	if err != nil {
		return Category{}, err
	}
	c := Category{ID: uuid.New(), Code: in.Code, Name: in.Name, Terms: in.Terms, SalvageBP: in.SalvageBP, Source: in.Source,
		CostAccount: "fixedasset." + in.Code + ".cost", AccDepAccount: "fixedasset." + in.Code + ".accdep", IdleExempt: in.IdleExempt, Active: true, CreatedAt: s.clock()}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertCategory(ctx, c); err != nil {
			return err
		}
		for _, a := range []books.Account{
			{Code: c.CostAccount, Name: "Fixed assets: " + c.Name, Type: books.Asset, Currency: s.cfg.Currency, Coding: in.CostCoding},
			{Code: c.AccDepAccount, Name: "Accumulated depreciation: " + c.Name, Type: books.Asset, Currency: s.cfg.Currency, Coding: in.AccDepCoding},
		} {
			if err := s.books.EnsureAccount(ctx, a); err != nil {
				return err
			}
		}
		return s.note(ctx, actor, "fixedasset.category_created", "fixed_asset_category", c.ID, "fixedasset.category.created", map[string]any{"code": c.Code, "source": c.Source})
	})
	return c, err
}

// CreateCategoryFromLegalTable adds a category whose method and rate or life come from a row of the legal depreciation table
// (see LegalRow); the citation is stored as its source. code and name default to the row's id and description. The tax law ignores
// salvage value, so none is set.
func (s *Service) CreateCategoryFromLegalTable(ctx context.Context, rowID, code, name string, in CategoryInput, actor string) (Category, error) {
	row, ok := LegalRowByID(rowID)
	if !ok {
		return Category{}, apperr.NotFound("legal table row")
	}
	if !row.Selectable {
		return Category{}, apperr.New(apperr.KindInvalid, "row_not_selectable", "this row needs a decision the table does not make ("+row.Note+"): create the category by hand")
	}
	in.Terms, in.Source, in.SalvageBP = row.Terms(), row.Source(), 0
	in.IdleExempt = row.IdleExempt()
	in.Code, in.Name = strings.TrimSpace(code), strings.TrimSpace(name)
	if in.Code == "" {
		in.Code = row.ID
	}
	if in.Name == "" {
		in.Name = row.Description
	}
	return s.CreateCategory(ctx, in, actor)
}

// Categories lists the categories.
func (s *Service) Categories(ctx context.Context) ([]Category, error) { return s.repo.Categories(ctx) }

// SetCategoryActive retires or restores a category for new assets; assets already registered keep their terms.
func (s *Service) SetCategoryActive(ctx context.Context, id uuid.UUID, active bool) error {
	return s.repo.SetCategoryActive(ctx, id, active)
}

// ---- assets ----

// Acquire registers an asset and books its purchase. It is idempotent by IdemKey.
func (s *Service) Acquire(ctx context.Context, in AcquireInput) (Asset, error) {
	in.Code, in.Name, in.IdemKey = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.IdemKey)
	var fe []apperr.FieldError
	if in.IdemKey == "" || len(in.IdemKey) > 120 {
		fe = append(fe, apperr.FieldError{Field: "idempotency_key", Message: "required, at most 120 characters"})
	}
	if in.Code == "" || len(in.Code) > 60 {
		fe = append(fe, apperr.FieldError{Field: "code", Message: "required, at most 60 characters"})
	}
	if in.Name == "" {
		fe = append(fe, apperr.FieldError{Field: "name", Message: "required"})
	}
	if in.Cost.Currency() != s.cfg.Currency || !in.Cost.IsPositive() {
		fe = append(fe, apperr.FieldError{Field: "cost", Message: "must be a positive amount in " + string(s.cfg.Currency)})
	}
	if len(fe) > 0 {
		return Asset{}, apperr.Invalid("invalid asset", fe...)
	}
	acquired, err := s.day(in.AcquiredOn, "acquired_on")
	if err != nil {
		return Asset{}, err
	}
	service := acquired
	if !in.InServiceOn.IsZero() {
		if service = dateOnly(in.InServiceOn); service.Before(acquired) {
			return Asset{}, apperr.InvalidField("in_service_on", "must not be before the acquisition date")
		}
	}
	idx, err := monthIndex(service)
	if err != nil {
		return Asset{}, err
	}
	cat, err := s.repo.Category(ctx, in.CategoryID)
	if err != nil {
		return Asset{}, err
	}
	if !cat.Active {
		return Asset{}, apperr.Conflict("the category is not active")
	}
	salvage := in.Cost.Amount() * int64(cat.SalvageBP) / 10000
	if in.Salvage != nil {
		if in.Salvage.Currency() != s.cfg.Currency || in.Salvage.Amount() < 0 || in.Salvage.Amount() >= in.Cost.Amount() {
			return Asset{}, apperr.InvalidField("salvage", "must be at least zero and below the cost")
		}
		salvage = in.Salvage.Amount()
	}
	var opening int64
	if in.OpeningAccumulated != nil || in.MonthsDone != 0 {
		if !in.AlreadyBooked {
			return Asset{}, apperr.Invalid("opening depreciation is only for assets that are already booked")
		}
		if in.OpeningAccumulated == nil || in.OpeningAccumulated.Amount() < 0 || in.OpeningAccumulated.Amount() > in.Cost.Amount()-salvage || in.MonthsDone < 0 {
			return Asset{}, apperr.Invalid("opening depreciation must be between zero and cost minus salvage, with a non-negative month count")
		}
		opening = in.OpeningAccumulated.Amount()
	}
	vat := int64(0)
	if in.VAT.Currency() != "" {
		if in.VAT.Currency() != s.cfg.Currency || in.VAT.Amount() < 0 {
			return Asset{}, apperr.InvalidField("vat", "must be zero or a positive amount in "+string(s.cfg.Currency))
		}
		vat = in.VAT.Amount()
	}
	if vat > 0 && (in.AlreadyBooked || s.cfg.Accounts.VATInput == "") {
		return Asset{}, apperr.Invalid("VAT is booked with the purchase: not for an asset that is already booked, and an input-VAT account must be configured")
	}
	paidFrom := in.PaidFrom
	if paidFrom == "" {
		paidFrom = s.cfg.Accounts.Bank
	}

	a := Asset{ID: uuid.New(), Code: in.Code, Name: in.Name, CategoryID: cat.ID, AcquiredOn: acquired, InServiceOn: service, Cost: in.Cost, Salvage: s.amt(salvage),
		Terms: cat.Terms, CostAccount: cat.CostAccount, AccDepAccount: cat.AccDepAccount, IdleExempt: cat.IdleExempt, ProgressMilli: in.MonthsDone * 1000, StartIndex: idx + 1,
		Accumulated: s.amt(opening), MonthsDone: in.MonthsDone, YearStartAccumulated: s.amt(opening), Status: Active, IdemKey: in.IdemKey, CreatedAt: s.clock()}
	// An asset registered with opening depreciation resumes after the months it already stands for.
	if in.MonthsDone > 0 {
		a.StartIndex = idx + 1 + in.MonthsDone
		a.ThroughIndex = a.StartIndex - 1
		a.DepYear, _ = PeriodOf(a.ThroughIndex)
		a.MonthsInYear = 0
		a.YearStartAccumulated = a.Accumulated
	}
	var out Asset
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		stored, created, err := s.repo.InsertAsset(ctx, a)
		if err != nil {
			return err
		}
		if !created {
			out = stored
			return nil
		}
		if !in.AlreadyBooked {
			jid, err := s.books.Post(ctx, books.Entry{Key: "fixedasset:" + a.ID.String() + ":acquire", Date: acquired, Description: "Acquisition of fixed asset " + a.Code,
				RefType: "fixed_asset", RefID: a.ID.String(), Actor: in.Actor, Lines: purchaseLines(a, paidFrom, vat, s.cfg.Accounts.VATInput, s.amt(a.Cost.Amount()+vat))})
			if err != nil {
				return err
			}
			a.AcquisitionJournal = &jid
			if err := s.repo.UpdateAsset(ctx, &a); err != nil {
				return err
			}
		}
		out = a
		return s.note(ctx, in.Actor, "fixedasset.acquired", "fixed_asset", a.ID, "fixedasset.asset.acquired", map[string]any{"code": a.Code, "cost": a.Cost.Amount(), "already_booked": in.AlreadyBooked})
	})
	return out, err
}

// Asset returns an asset.
func (s *Service) Asset(ctx context.Context, id uuid.UUID) (Asset, error) {
	return s.repo.Asset(ctx, id, false)
}

// Assets lists assets, optionally of one status.
func (s *Service) Assets(ctx context.Context, status Status) ([]Asset, error) {
	return s.repo.Assets(ctx, status)
}

// History lists the depreciation booked for an asset.
func (s *Service) History(ctx context.Context, id uuid.UUID) ([]Item, error) {
	if _, err := s.repo.Asset(ctx, id, false); err != nil {
		return nil, err
	}
	return s.repo.Depreciations(ctx, id)
}

// Schedule projects the remaining depreciation of an asset month by month until it reaches its salvage value.
func (s *Service) Schedule(ctx context.Context, id uuid.UUID) ([]ScheduleRow, error) {
	a, err := s.repo.Asset(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if a.Status != Active {
		return nil, nil
	}
	if a.Method == UnitsOfProduction {
		return nil, nil // depends on the units produced: nothing to project
	}
	st := a.state()
	idx := a.StartIndex
	if a.ThroughIndex > 0 {
		idx = a.ThroughIndex + 1
	}
	year := a.DepYear
	var rows []ScheduleRow
	for i := 0; i < 1300; i++ {
		y, m := PeriodOf(idx)
		st = st.ForYear(y != year)
		amount := NextAmount(st)
		if amount == 0 {
			break
		}
		st = st.Advance(amount)
		year = y
		rows = append(rows, ScheduleRow{Year: y, Month: m, Amount: amount, Accumulated: st.Accumulated, BookValue: st.Cost - st.Accumulated})
		idx++
	}
	return rows, nil
}

// ---- depreciation ----

// RunDepreciation books the depreciation of a finished Jalali month for every asset that is due: one journal, debit the
// depreciation expense, credit each category's accumulated depreciation. Months must be run in order: an asset that is
// behind blocks the run and is named in the error. A month that has nothing left to do returns its last run.
func (s *Service) RunDepreciation(ctx context.Context, year, month int, actor string) (Run, error) {
	if month < 1 || month > 12 {
		return Run{}, apperr.InvalidField("month", "must be 1 to 12")
	}
	_, next, err := jalali.MonthBounds(year, month, time.UTC)
	if err != nil {
		return Run{}, apperr.Invalid("month out of the supported range")
	}
	if !dateOnly(s.books.Today()).After(next.AddDate(0, 0, -1)) {
		return Run{}, apperr.New(apperr.KindConflict, "month_not_over", "depreciation can only be booked for a finished month")
	}
	period := PeriodIndex(year, month)
	var out Run
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		assets, err := s.repo.DepreciableAssets(ctx)
		if err != nil {
			return err
		}
		var items []Item
		var behind []string
		perCat := map[string]int64{}
		var total int64
		for _, a := range assets {
			first := a.StartIndex
			if a.ThroughIndex > 0 {
				first = a.ThroughIndex + 1
			}
			switch {
			case first > period:
				continue
			case first < period:
				behind = append(behind, a.Code)
				continue
			}
			st := a.state().ForYear(year != a.DepYear)
			idle := false
			if a.Method != UnitsOfProduction && !a.IdleExempt {
				spans, err := s.repo.IdleSpans(ctx, a.ID)
				if err != nil {
					return err
				}
				if idle = IdleApplies(spans, period, s.idleMin()); idle {
					st.Idle, st.IdleBP = true, s.idleBP()
				}
			}
			var amount int64
			if a.Method == UnitsOfProduction {
				cum, err := s.repo.UnitsUpTo(ctx, a.ID, period)
				if err != nil {
					return err
				}
				amount = UnitsAmount(st.Cost, st.Salvage, st.Accumulated, cum, int64(a.LifeMonths))
			} else {
				amount = NextAmount(st)
			}
			progress := st.delta()
			st = st.Advance(amount)
			a.ProgressMilli = st.ProgressMilli
			a.Accumulated, a.MonthsDone, a.YearStartAccumulated, a.MonthsInYear = s.amt(st.Accumulated), st.MonthsDone, s.amt(st.YearStartAccumulated), st.MonthsInYear
			a.ThroughIndex, a.DepYear = period, year
			if err := s.repo.UpdateAsset(ctx, &a); err != nil {
				return err
			}
			if amount == 0 {
				continue
			}
			items = append(items, Item{AssetID: a.ID, AssetCode: a.Code, PeriodIndex: period, Amount: s.amt(amount), Idle: idle, ProgressMilli: progress})
			perCat[a.AccDepAccount] += amount
			total += amount
		}
		if len(behind) > 0 {
			return apperr.New(apperr.KindConflict, "depreciation_behind", "run the earlier months first; behind: "+strings.Join(behind, ", "))
		}
		if len(items) == 0 {
			r, ok, err := s.repo.LatestRun(ctx, period)
			if err != nil {
				return err
			}
			if ok {
				out = r
			} else {
				out = Run{PeriodIndex: period, Year: year, Month: month, Total: s.amt(0)}
			}
			return nil
		}
		n, err := s.repo.RunCount(ctx, period)
		if err != nil {
			return err
		}
		accts := make([]string, 0, len(perCat))
		for a := range perCat {
			accts = append(accts, a)
		}
		slices.Sort(accts)
		lines := []books.Line{books.Dr(s.cfg.Accounts.DepreciationExpense, s.amt(total))}
		for _, a := range accts {
			lines = append(lines, books.Cr(a, s.amt(perCat[a])))
		}
		jid, err := s.books.Post(ctx, books.Entry{Key: fmt.Sprintf("fixedasset:dep:%d:%d", period, n+1), Date: next.AddDate(0, 0, -1),
			Description: fmt.Sprintf("Depreciation %d/%02d", year, month), RefType: "fixed_asset_run", RefID: fmt.Sprintf("%d:%d", period, n+1), Actor: actor, Lines: lines})
		if err != nil {
			return err
		}
		run := Run{PeriodIndex: period, Seq: n + 1, Year: year, Month: month, Journal: &jid, Total: s.amt(total), Items: items, CreatedAt: s.clock()}
		if err := s.repo.InsertRun(ctx, run); err != nil {
			return err
		}
		if err := s.repo.InsertItems(ctx, period, n+1, items); err != nil {
			return err
		}
		out = run
		return s.note(ctx, actor, "fixedasset.depreciated", "fixed_asset_run", uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%d:%d", period, n+1))), "fixedasset.depreciation.booked",
			map[string]any{"year": year, "month": month, "total": total, "assets": len(items), "journal_id": jid})
	})
	return out, err
}

// Dispose retires an asset: its cost and accumulated depreciation leave the books, the proceeds come in and the difference is
// the gain or loss. Depreciation must be booked up to the month before the disposal; the disposal month itself is not
// depreciated (an assumption, flagged in the README). It is idempotent by IdemKey.
func (s *Service) Dispose(ctx context.Context, id uuid.UUID, in DisposeInput) (Asset, error) {
	if strings.TrimSpace(in.IdemKey) == "" {
		return Asset{}, apperr.InvalidField("idempotency_key", "required")
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
		if a.Status == Disposed {
			if a.DisposalKey == in.IdemKey {
				out = a // a retry of the same request
				return nil
			}
			return apperr.Conflict("the asset is already disposed")
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
		cost, accum := a.Cost.Amount(), a.Accumulated.Amount()
		diff := in.Proceeds.Amount() + accum - cost // positive = gain
		lines := []books.Line{books.Cr(a.CostAccount, a.Cost)}
		if accum > 0 {
			lines = append(lines, books.Dr(a.AccDepAccount, a.Accumulated))
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
		jid, err := s.books.Post(ctx, books.Entry{Key: "fixedasset:" + a.ID.String() + ":dispose:" + in.IdemKey, Date: date, Description: "Disposal of fixed asset " + a.Code,
			RefType: "fixed_asset", RefID: a.ID.String(), Actor: in.Actor, Lines: lines})
		if err != nil {
			return err
		}
		a.Status, a.DisposedOn, a.DisposalJournal, a.DisposalKey = Disposed, &date, &jid, in.IdemKey
		if err := s.repo.UpdateAsset(ctx, &a); err != nil {
			return err
		}
		out = a
		return s.note(ctx, in.Actor, "fixedasset.disposed", "fixed_asset", a.ID, "fixedasset.asset.disposed", map[string]any{"code": a.Code, "proceeds": in.Proceeds.Amount(), "gain_loss": diff})
	})
	return out, err
}

// purchaseLines is the purchase journal: cost, the deductible input tax if any, and what was paid.
func purchaseLines(a Asset, paidFrom string, vat int64, vatAcct string, gross money.Money) []books.Line {
	lines := []books.Line{books.Dr(a.CostAccount, a.Cost)}
	if vat > 0 {
		lines = append(lines, books.Dr(vatAcct, money.Must(vat, a.Cost.Currency())))
	}
	return append(lines, books.Cr(paidFrom, gross))
}
