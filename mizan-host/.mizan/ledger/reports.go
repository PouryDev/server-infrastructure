package ledger

import (
	"context"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// BalanceLine is one account on a balance sheet. Amount is in the account's natural direction, so a contra account such as
// accumulated depreciation is negative inside the assets.
type BalanceLine struct {
	Code       string
	Name       string
	CodingCode string
	Amount     int64
}

// BalanceSection is a group of lines with its total.
type BalanceSection struct {
	Lines []BalanceLine
	Total int64
}

func (s *BalanceSection) add(l BalanceLine) {
	s.Lines = append(s.Lines, l)
	s.Total += l.Amount
}

// BalanceSheet (ترازنامه) as of a date.
//
// Accounts are split into current and non-current (جاری و غیرجاری) by the chart of accounts: the title of an account's top
// group says whether it is non-current. The criteria are the accounting standard's (settled or turned into cash within the
// operating cycle or twelve months = current), but the split itself is only as good as the mapping of accounts to the chart;
// accounts without a coding code are treated as current and listed in Unclassified so nothing is hidden.
type BalanceSheet struct {
	AsOf                time.Time
	FiscalYear          string
	CurrentAssets       BalanceSection
	NonCurrentAssets    BalanceSection
	CurrentLiabilities  BalanceSection
	NonCurrentLiability BalanceSection
	Equity              BalanceSection
	// CurrentResult is the profit (loss) of revenue and expense accounts that no closing document has moved to retained
	// earnings yet (سود (زیان) جاری): the year to date, or older years that were never closed.
	CurrentResult int64
	TotalAssets   int64
	TotalClaims   int64 // liabilities + equity + current result
	Balanced      bool
	Unclassified  []string
}

// BalanceSheet builds the balance sheet as of date (nil = today). A date inside a fiscal year shows that year's real
// position even after the year was closed: its own closing documents are left out, those of earlier years stay in.
func (s *Service) BalanceSheet(ctx context.Context, asOf *time.Time) (BalanceSheet, error) {
	d := s.Today()
	if asOf != nil {
		d = s.DateOf(*asOf)
	}
	period, err := s.store.PeriodFor(ctx, d)
	if err != nil && !apperr.Is(err, apperr.KindNotFound) {
		return BalanceSheet{}, err
	}
	rows, err := s.store.TrialBalance(ctx, TrialBalanceQuery{To: &d, ExcludeClosingYear: period.FiscalYearID})
	if err != nil {
		return BalanceSheet{}, err
	}
	chart, err := s.store.Coding(ctx)
	if err != nil {
		return BalanceSheet{}, err
	}
	nonCurrent := nonCurrentGroups(chart)

	bs := BalanceSheet{AsOf: d}
	if period.FiscalYearID != 0 {
		if _, all, err := s.fiscalYear(ctx, period.FiscalYearID); err == nil {
			for _, f := range all {
				if f.ID == period.FiscalYearID {
					bs.FiscalYear = f.Name
				}
			}
		}
	}
	for _, r := range rows {
		bal := r.Balance()
		if bal == 0 && r.Debit == r.Credit {
			continue
		}
		line := BalanceLine{Code: r.Code, Name: r.Name, CodingCode: r.CodingCode, Amount: bal}
		nc := nonCurrent[r.CodingCode]
		switch r.Type {
		case Revenue:
			bs.CurrentResult += bal
		case Expense:
			bs.CurrentResult -= bal
		case Asset:
			if r.CodingCode == "" {
				bs.Unclassified = append(bs.Unclassified, r.Code)
			}
			if nc {
				bs.NonCurrentAssets.add(line)
			} else {
				bs.CurrentAssets.add(line)
			}
		case Liability:
			if r.CodingCode == "" {
				bs.Unclassified = append(bs.Unclassified, r.Code)
			}
			if nc {
				bs.NonCurrentLiability.add(line)
			} else {
				bs.CurrentLiabilities.add(line)
			}
		case Equity:
			bs.Equity.add(line)
		}
	}
	bs.TotalAssets = bs.CurrentAssets.Total + bs.NonCurrentAssets.Total
	bs.TotalClaims = bs.CurrentLiabilities.Total + bs.NonCurrentLiability.Total + bs.Equity.Total + bs.CurrentResult
	bs.Balanced = bs.TotalAssets == bs.TotalClaims
	return bs, nil
}

// nonCurrentGroups returns, for every chart code whose top group is titled non-current (غیرجاری), true.
func nonCurrentGroups(chart []CodingNode) map[string]bool {
	byCode := make(map[string]CodingNode, len(chart))
	for _, n := range chart {
		byCode[n.Code] = n
	}
	out := map[string]bool{}
	for _, n := range chart {
		top := n
		for guard := 0; top.Parent != "" && guard < 8; guard++ {
			top = byCode[top.Parent]
		}
		t := strings.NewReplacer(" ", "", "\u200c", "").Replace(top.Title)
		out[n.Code] = strings.Contains(t, "غیرجاری")
	}
	return out
}
