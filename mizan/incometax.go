package mizan

import (
	"context"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/incometax"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

// IncomeTaxFacts reads the accounting profit of a fiscal year from the books: the income statement of the year without the closing documents. The tax
// expense must not be booked yet (declare it through DeclareIncomeTax). The expense is dated the year's last day while the year is open; once it is closed
// no posting may go into it, so the expense is dated today then (an assessment of an earlier year booked in the current one).
func (m *Mizan) IncomeTaxFacts(ctx context.Context, fiscalYearID int64) (incometax.Facts, error) {
	years, err := m.Ledger.FiscalYears(ctx)
	if err != nil {
		return incometax.Facts{}, err
	}
	for _, fy := range years {
		if fy.ID != fiscalYearID {
			continue
		}
		start, last := fy.StartsOn, fy.EndsBefore.AddDate(0, 0, -1)
		is, err := m.Ledger.IncomeStatement(ctx, &start, &last, false)
		if err != nil {
			return incometax.Facts{}, err
		}
		f := incometax.Facts{FiscalYear: fy.Name, From: start, To: last, Profit: is.Net, BookOn: last}
		if fy.Status == ledger.Closed {
			f.BookOn = m.Ledger.Today()
		}
		return f, nil
	}
	return incometax.Facts{}, apperr.NotFound("fiscal year")
}

// DeclareIncomeTax reads the facts of the fiscal year and declares its income tax with the accountant's explicit adjustments.
func (m *Mizan) DeclareIncomeTax(ctx context.Context, fiscalYearID int64, adj incometax.Adjustments, idemKey, actor string) (incometax.Assessment, error) {
	f, err := m.IncomeTaxFacts(ctx, fiscalYearID)
	if err != nil {
		return incometax.Assessment{}, err
	}
	return m.IncomeTax.Declare(ctx, incometax.DeclareInput{IdemKey: idemKey, Facts: f, Adj: adj, Actor: actor})
}
