package mizan

import (
	"context"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

// AppropriationFacts reads the numbers the profit-appropriation rules need from the books for a CLOSED fiscal year: the year's net profit
// (income statement without the closing documents), the retained earnings carried into the year (balance on its first day, which
// includes the opening document), the capital and the legal reserve now. Net profit is whatever the books show: book the income tax
// expense before. Taking the retained earnings from the first day means any other posting dated that day also counts.
func (m *Mizan) AppropriationFacts(ctx context.Context, fiscalYearID int64, publicCompany bool) (appropriation.Facts, string, error) {
	years, err := m.Ledger.FiscalYears(ctx)
	if err != nil {
		return appropriation.Facts{}, "", err
	}
	for _, fy := range years {
		if fy.ID != fiscalYearID {
			continue
		}
		if fy.Status != ledger.Closed {
			return appropriation.Facts{}, "", apperr.New(apperr.KindConflict, "year_not_closed", "close the fiscal year first: profit is appropriated after the accounts are approved")
		}
		start, last := fy.StartsOn, fy.EndsBefore.AddDate(0, 0, -1)
		is, err := m.Ledger.IncomeStatement(ctx, &start, &last, false)
		if err != nil {
			return appropriation.Facts{}, "", err
		}
		retained, err := m.Ledger.Balance(ctx, AccRetainedEarnings, &start)
		if err != nil {
			return appropriation.Facts{}, "", err
		}
		capital, err := m.Ledger.Balance(ctx, AccCapital, nil)
		if err != nil {
			return appropriation.Facts{}, "", err
		}
		reserve, err := m.Ledger.Balance(ctx, AccLegalReserve, nil)
		if err != nil {
			return appropriation.Facts{}, "", err
		}
		return appropriation.Facts{NetProfit: is.Net, RetainedAtStart: retained.Amount(), Capital: capital.Amount(), LegalReserve: reserve.Amount(), PublicCompany: publicCompany}, fy.Name, nil
	}
	return appropriation.Facts{}, "", apperr.NotFound("fiscal year")
}

// DeclareAppropriation reads the facts of a closed fiscal year and books the general meeting's decision about its profit.
func (m *Mizan) DeclareAppropriation(ctx context.Context, fiscalYearID int64, publicCompany bool, decidedOn time.Time, d appropriation.Decision, idemKey, actor string) (appropriation.Record, error) {
	facts, name, err := m.AppropriationFacts(ctx, fiscalYearID, publicCompany)
	if err != nil {
		return appropriation.Record{}, err
	}
	return m.Appropriate.Declare(ctx, appropriation.DeclareInput{IdemKey: idemKey, FiscalYear: name, Facts: facts, DecidedOn: decidedOn, Decision: d, Actor: actor})
}
