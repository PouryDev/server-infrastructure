package incometax

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Item is one explicit tax adjustment with its reason (a label the accountant can defend).
type Item struct {
	Label  string `json:"label"`
	Amount int64  `json:"amount"` // positive rials
}

// Loss is a tax loss of an earlier assessment that can still be set against profit.
type Loss struct {
	AssessmentID uuid.UUID
	FiscalYear   string
	YearStart    time.Time
	Remaining    int64
}

// LossUse is the part of an earlier loss used by an assessment.
type LossUse struct {
	AssessmentID uuid.UUID
	FiscalYear   string
	Amount       int64
}

// Input of the pure computation.
type Input struct {
	Profit          int64  // accounting profit (loss negative) of the fiscal year, income-tax expense not yet booked
	AddBacks        []Item // accounting expenses the tax law does not accept (added to the profit)
	Deductions      []Item // income exempt from tax and other deductions (taken off the profit)
	DepreciationAdj int64  // accounting depreciation minus tax depreciation (art. 149 table): positive adds to the profit, negative takes off
	Rule            Rule
	Cooperative     bool // an ordinary cooperative: 25% discount on the rate (art. 105 note 6)
	RateReductionBP int  // further points off the rate that the taxpayer qualifies for (art. 105 note 7, budget-law reductions): explicit input
	YearStart       time.Time
	Losses          []Loss // open losses, oldest first
}

// Result of the computation, with the whole breakdown.
type Result struct {
	Profit            int64
	AddBackTotal      int64
	DeductionTotal    int64
	DepreciationAdj   int64
	TaxableBeforeLoss int64 // may be negative: a tax loss
	LossUsed          []LossUse
	LossUsedTotal     int64
	NewLoss           int64 // the tax loss created by this year (0 when profitable)
	Taxable           int64
	RateBP            int // the effective rate after discount and reductions
	Tax               int64
}

func sum(items []Item, field string) (int64, error) {
	var t int64
	for _, it := range items {
		if strings.TrimSpace(it.Label) == "" || it.Amount <= 0 {
			return 0, apperr.InvalidField(field, "every item needs a label and a positive amount")
		}
		t += it.Amount
	}
	return t, nil
}

// lossExpired reports whether a loss is older than the rule allows (nil limit: no limit; the text of art. 148 item 12 read gives none).
func lossExpired(r Rule, l Loss, yearStart time.Time) bool {
	if r.LossCarryYears == nil {
		return false
	}
	return yearStart.After(l.YearStart.AddDate(*r.LossCarryYears, 0, -1))
}

// Compute applies the rule to the books' profit and the explicit adjustments.
//
//	taxable before loss = profit + add-backs - deductions + depreciation difference
//	older tax losses are set off, oldest first (art. 148 item 12), within the rule's limit when it has one
//	tax = taxable x effective rate, rounded down to a rial
func Compute(in Input) (Result, error) {
	add, err := sum(in.AddBacks, "add_backs")
	if err != nil {
		return Result{}, err
	}
	ded, err := sum(in.Deductions, "deductions")
	if err != nil {
		return Result{}, err
	}
	r := Result{Profit: in.Profit, AddBackTotal: add, DeductionTotal: ded, DepreciationAdj: in.DepreciationAdj}
	r.TaxableBeforeLoss = in.Profit + add - ded + in.DepreciationAdj
	taxable := r.TaxableBeforeLoss
	if taxable < 0 {
		r.NewLoss = -taxable
		taxable = 0
	}
	for _, l := range in.Losses {
		if taxable == 0 {
			break
		}
		if l.Remaining <= 0 || lossExpired(in.Rule, l, in.YearStart) {
			continue
		}
		use := min(l.Remaining, taxable)
		r.LossUsed = append(r.LossUsed, LossUse{AssessmentID: l.AssessmentID, FiscalYear: l.FiscalYear, Amount: use})
		r.LossUsedTotal += use
		taxable -= use
	}
	r.Taxable = taxable
	rate := in.Rule.RateBP
	if in.Cooperative {
		if in.Rule.CooperativeDiscountBP == 0 {
			return Result{}, apperr.Invalid("the rule in force has no cooperative discount")
		}
		rate = rate * (10000 - in.Rule.CooperativeDiscountBP) / 10000
	}
	if in.RateReductionBP < 0 || in.RateReductionBP > rate {
		return Result{}, apperr.InvalidField("rate_reduction_bp", fmt.Sprintf("must be between 0 and the rate (%d)", rate))
	}
	r.RateBP = rate - in.RateReductionBP
	r.Tax = mulDiv(r.Taxable, int64(r.RateBP), 10000)
	return r, nil
}

// mulDiv computes a*b/c rounded down without overflow for the sizes involved.
func mulDiv(a, b, c int64) int64 {
	q, rem := a/c, a%c
	return q*b + rem*b/c
}
