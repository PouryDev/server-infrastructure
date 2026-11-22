package fixedasset

import (
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// Status of an asset.
type Status string

const (
	Active   Status = "active"
	Disposed Status = "disposed"
)

// Category is a class of assets with its depreciation terms and ledger accounts (طبقه دارایی).
type Category struct {
	ID   uuid.UUID
	Code string
	Name string
	Terms
	SalvageBP int    // salvage value as basis points of the cost (0 = none)
	Source    string // where the rate or life comes from (law article, table row, accountant's decision)
	// Ledger accounts, created with the category.
	CostAccount   string
	AccDepAccount string
	// IdleExempt marks categories the art. 6 idle-asset remark does not apply to: buildings, building installations and intangibles with a definite life.
	IdleExempt bool
	Active     bool
	CreatedAt  time.Time
}

// CategoryInput creates a category.
type CategoryInput struct {
	Code      string
	Name      string
	Terms     Terms
	SalvageBP int
	Source    string
	// CostCoding and AccDepCoding are optional chart-of-accounts codes of the two accounts (for example the general or
	// subsidiary accounts of fixed assets and of accumulated depreciation in your chart).
	CostCoding, AccDepCoding string
	// IdleExempt: the art. 6 remark on idle assets does not apply (buildings, building installations, intangibles with a definite life).
	IdleExempt bool
}

func (in CategoryInput) validate() (CategoryInput, error) {
	in.Code, in.Name, in.Source = strings.TrimSpace(in.Code), strings.TrimSpace(in.Name), strings.TrimSpace(in.Source)
	var fe []apperr.FieldError
	if in.Code == "" || len(in.Code) > 40 || strings.ContainsAny(in.Code, " :") {
		fe = append(fe, apperr.FieldError{Field: "code", Message: "required, at most 40 characters, no spaces or colons"})
	}
	if in.Name == "" {
		fe = append(fe, apperr.FieldError{Field: "name", Message: "required"})
	}
	if in.Source == "" {
		fe = append(fe, apperr.FieldError{Field: "source", Message: "required: say where the rate or life comes from"})
	}
	if in.SalvageBP < 0 || in.SalvageBP > 9000 {
		fe = append(fe, apperr.FieldError{Field: "salvage_bp", Message: "must be between 0 and 9000"})
	}
	if err := in.Terms.Validate(); err != nil {
		return in, err
	}
	if len(fe) > 0 {
		return in, apperr.Invalid("invalid category", fe...)
	}
	return in, nil
}

// Asset is a fixed asset in the register.
type Asset struct {
	ID            uuid.UUID
	Code          string
	Name          string
	CategoryID    uuid.UUID
	AcquiredOn    time.Time
	InServiceOn   time.Time
	Cost          money.Money
	Salvage       money.Money
	Terms         // snapshot of the category's terms
	CostAccount   string
	AccDepAccount string
	IdleExempt    bool // snapshot of the category's flag
	// ProgressMilli is the life progress in thousandths of a month (see State.ProgressMilli).
	ProgressMilli int

	// StartIndex is the period index of the first month to depreciate: the month after the one the asset entered service
	// (the usual rule of the direct-tax regulations; from a secondary source, see the README).
	StartIndex           int
	Accumulated          money.Money
	MonthsDone           int
	ThroughIndex         int // period index of the last depreciated month; 0 = none yet
	DepYear              int // Jalali year the yearly figures below belong to
	YearStartAccumulated money.Money
	MonthsInYear         int

	Status             Status
	DisposedOn         *time.Time
	AcquisitionJournal *uuid.UUID
	DisposalJournal    *uuid.UUID
	DisposalKey        string
	IdemKey            string
	CreatedAt          time.Time
}

// BookValue is cost minus accumulated depreciation.
func (a Asset) BookValue() int64 { return a.Cost.Amount() - a.Accumulated.Amount() }

// state converts the asset to the calculator's input.
func (a Asset) state() State {
	return State{Cost: a.Cost.Amount(), Salvage: a.Salvage.Amount(), Accumulated: a.Accumulated.Amount(), Terms: a.Terms,
		MonthsDone: a.MonthsDone, YearStartAccumulated: a.YearStartAccumulated.Amount(), MonthsInYear: a.MonthsInYear, ProgressMilli: a.ProgressMilli}
}

// AcquireInput registers an asset.
type AcquireInput struct {
	IdemKey     string
	Code        string
	Name        string
	CategoryID  uuid.UUID
	AcquiredOn  time.Time // default today
	InServiceOn time.Time // default AcquiredOn
	Cost        money.Money
	Salvage     *money.Money // default: the category's salvage share of the cost
	// VAT is the deductible input tax of the purchase (optional): booked to the input-VAT account, NOT part of the asset's cost. PaidFrom is
	// credited with the cost plus the tax. Ignored when AlreadyBooked.
	VAT money.Money
	// PaidFrom is the ledger account credited by the purchase (default the bank account). Ignored when AlreadyBooked.
	PaidFrom string
	// AlreadyBooked registers an asset whose cost is already in the books (an opening balance): no purchase journal is made.
	AlreadyBooked bool
	// OpeningAccumulated is the depreciation already taken before registering (only with AlreadyBooked). It is not booked
	// here: it must already be in the accumulated depreciation account. MonthsDone says how many months it stands for.
	OpeningAccumulated *money.Money
	MonthsDone         int
	Actor              string
}

// Run is one depreciation run of a Jalali month.
type Run struct {
	PeriodIndex int
	Seq         int
	Year, Month int
	Journal     *uuid.UUID
	Total       money.Money
	Items       []Item
	CreatedAt   time.Time
}

// Item is the depreciation of one asset in a run.
type Item struct {
	AssetID     uuid.UUID
	AssetCode   string
	PeriodIndex int
	Amount      money.Money
	Idle        bool // taken at the idle share (art. 6 remark)
	// ProgressMilli is how much life progress the month added (1000 normal, IdleBP/10 idle); voiding takes it back.
	ProgressMilli int
}

// DisposeInput retires an asset.
type DisposeInput struct {
	IdemKey         string
	Date            time.Time // default today
	Proceeds        money.Money
	ProceedsAccount string // where the proceeds land (default the bank account)
	Actor           string
}

// ScheduleRow is one month of the projected schedule.
type ScheduleRow struct {
	Year, Month int
	Amount      int64
	Accumulated int64
	BookValue   int64
}
