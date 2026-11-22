// Package appropriation is the appropriation of a fiscal year's profit by the general meeting (تخصیص و تقسیم سود): the legal reserve,
// optional reserves and dividends, with the limits of the Iranian Commercial Code (قانون تجارت، مصوب ۱۳۴۷/۱۲/۲۴) enforced.
//
// The rules below are taken from the full texts of the articles (read on https://vakilik.com/laws/book/MzM0):
//
//   - Art. 238 and 140: one twentieth of the net profit, after deducting losses of earlier years, must be set aside as the legal
//     reserve (اندوخته قانونی) every year; once the reserve reaches one tenth of the capital setting it aside becomes optional, and
//     the deduction resumes if the capital is increased, until the reserve again reaches one tenth of the capital.
//   - Art. 239: distributable profit = net profit of the year - losses of earlier years - the legal reserve - other optional reserves
//   - distributable profit of earlier years that was not distributed.
//   - Art. 240: after approving the accounts, the general meeting fixes the amount to distribute; payment must be made within eight
//     months of its decision; profit distributed against the law counts as fictitious.
//   - Art. 90: profit is distributed only after the general meeting approves it, and when profit exists distributing ten percent of the
//     annual net profit is mandatory.
//   - Art. 241 (as amended 1395/02/20): the share of the net profit set aside for the directors' bonus may never exceed 3% (public joint-stock
//     company) or 6% (private) of the profit payable to the shareholders that year (and per member one year's base salary): reported as a
//     limit here, not booked.
//
// Not covered: the dividend withholding tax, bonus booking, reserves other than the legal one prescribed by a company's articles of association,
// and the special rules for companies other than joint-stock companies.
package appropriation

import (
	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Facts are the numbers the rules need, read from the books by the caller. Amounts are rials.
type Facts struct {
	// NetProfit of the fiscal year after tax (negative = loss).
	NetProfit int64
	// RetainedAtStart is the retained earnings balance at the start of the year in its natural (credit) direction: positive is
	// undistributed profit of earlier years, negative is losses of earlier years.
	RetainedAtStart int64
	Capital         int64
	// LegalReserve is the legal reserve balance now.
	LegalReserve int64
	// PublicCompany selects the 3% limit on the directors' bonus (public joint-stock) instead of 6% (private).
	PublicCompany bool
}

// Plan is the legal result of Facts.
type Plan struct {
	// LegalReserve is the amount that must be set aside now.
	LegalReserve int64
	// Distributable is the profit that may be distributed after the legal reserve, before optional reserves (article 239).
	Distributable int64
	// MinDividend is the mandatory distribution (10% of the net profit, article 90), never above what is distributable.
	MinDividend int64
	// MaxBonusRate is 300 or 600 basis points of the dividend (article 241).
	MaxBonusBP int
}

// Compute applies articles 140, 238, 239 and 90.
func Compute(f Facts) Plan {
	var p Plan
	priorLoss := int64(0)
	if f.RetainedAtStart < 0 {
		priorLoss = -f.RetainedAtStart
	}
	if base := f.NetProfit - priorLoss; base > 0 {
		p.LegalReserve = (base + 10) / 20 // one twentieth, rounded half up
		if room := f.Capital/10 - f.LegalReserve; p.LegalReserve > room {
			p.LegalReserve = max(room, 0) // the reserve stops at one tenth of the capital
		}
	}
	p.Distributable = max(f.NetProfit+f.RetainedAtStart-p.LegalReserve, 0)
	if f.NetProfit > 0 {
		p.MinDividend = min((f.NetProfit+5)/10, p.Distributable)
	}
	p.MaxBonusBP = 600
	if f.PublicCompany {
		p.MaxBonusBP = 300
	}
	return p
}

// Decision is what the general meeting decides.
type Decision struct {
	Dividend        int64
	OptionalReserve int64
}

// Validate checks a decision against the plan: optional reserves and dividends together must fit in the distributable profit, and the
// dividend must reach the mandatory minimum (a smaller one is possible only when nothing at all is distributable).
func (p Plan) Validate(d Decision) error {
	switch {
	case d.Dividend < 0 || d.OptionalReserve < 0:
		return apperr.Invalid("amounts must not be negative")
	case d.Dividend+d.OptionalReserve > p.Distributable:
		return apperr.New(apperr.KindConflict, "above_distributable", "dividends and optional reserves exceed the distributable profit (article 239)")
	case d.Dividend < min(p.MinDividend, max(p.Distributable-d.OptionalReserve, 0)):
		return apperr.New(apperr.KindConflict, "below_mandatory_dividend", "when profit exists at least ten percent of the annual net profit must be distributed (article 90)")
	}
	return nil
}
