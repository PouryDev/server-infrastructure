//nolint:staticcheck // the Persian text uses the zero-width non-joiner (U+200C), which is correct orthography
package fixedasset

import (
	"context"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// IdleRuleSource cites the rule: the remark of article 6 of «مقررات و ضوابط اجرایی هزینه استهلاک دارایی‌ها و هزینه‌های تأسیس» (approved 1394/04/31, circular 200/95/78 of
// 1395/11/04, read from the official text): a firm MAY, when an asset stays idle or temporarily unused for more than six consecutive months in one financial period, take
// 30% of the depreciation for the idle time; for a time-based method 70% of the idle time is added to the remaining depreciation period of the table; it does not apply to
// buildings, building installations and intangibles with a definite life. The text says nothing on reverting: the asset simply depreciates normally after the idle span.
const IdleRuleSource = "ماده ۶ تبصره مقررات و ضوابط اجرایی هزینه استهلاک (بخشنامه ۲۰۰/۹۵/۷۸ مورخ ۱۳۹۵/۱۱/۰۴)"

// Default idle-rule parameters, overridden by Config.
const (
	IdleMinMonths = 6    // more than six consecutive months in a financial period
	IdleRateBP    = 3000 // 30%
)

func (s *Service) idleMin() int {
	if s.cfg.IdleMinMonths > 0 {
		return s.cfg.IdleMinMonths
	}
	return IdleMinMonths
}

func (s *Service) idleBP() int {
	if s.cfg.IdleRateBP > 0 {
		return s.cfg.IdleRateBP
	}
	return IdleRateBP
}

// IdleSpan is a closed range of Jalali months (period indexes, inclusive) in which an asset was idle.
type IdleSpan struct {
	ID        uuid.UUID
	AssetID   uuid.UUID
	From, To  int // PeriodIndex of the first and last idle month
	Note      string
	CreatedAt time.Time
}

// IdleApplies reports whether the month period is depreciated at the idle share: the month lies in a run of consecutive idle months (touching spans are merged) whose part inside
// the month's Jalali year is longer than minMonths.
func IdleApplies(spans []IdleSpan, period, minMonths int) bool {
	year, _ := PeriodOf(period)
	lo, hi := year*12, year*12+11
	type iv struct{ a, b int }
	var ivs []iv
	for _, sp := range spans {
		ivs = append(ivs, iv{max(sp.From, lo), min(sp.To, hi)})
	}
	slices.SortFunc(ivs, func(x, y iv) int { return x.a - y.a })
	var merged []iv
	for _, v := range ivs {
		if v.a > v.b {
			continue
		}
		if n := len(merged); n > 0 && v.a <= merged[n-1].b+1 {
			merged[n-1].b = max(merged[n-1].b, v.b)
			continue
		}
		merged = append(merged, v)
	}
	for _, m := range merged {
		if period >= m.a && period <= m.b {
			return m.b-m.a+1 > minMonths
		}
	}
	return false
}

// AddIdleSpan records that an asset was idle from a Jalali month to another (inclusive). It must be recorded before those months are depreciated (the run reads it): if depreciation
// already covers any of them, void the runs first (409). Not for assets of an exempt category or depreciated by units of production.
func (s *Service) AddIdleSpan(ctx context.Context, assetID uuid.UUID, fromYear, fromMonth, toYear, toMonth int, note, actor string) (IdleSpan, error) {
	if fromMonth < 1 || fromMonth > 12 || toMonth < 1 || toMonth > 12 {
		return IdleSpan{}, apperr.InvalidField("month", "must be 1 to 12")
	}
	from, to := PeriodIndex(fromYear, fromMonth), PeriodIndex(toYear, toMonth)
	if to < from {
		return IdleSpan{}, apperr.InvalidField("to", "must not be before from")
	}
	if to/12 != from/12 {
		return IdleSpan{}, apperr.InvalidField("to", "a span stays inside one Jalali year: split it at the year end (the six-month test is per fiscal year)")
	}
	sp := IdleSpan{ID: uuid.New(), AssetID: assetID, From: from, To: to, Note: note, CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Asset(ctx, assetID, true)
		if err != nil {
			return err
		}
		if a.IdleExempt {
			return apperr.New(apperr.KindConflict, "idle_exempt", "the idle rule does not apply to buildings, building installations and intangibles")
		}
		if a.Method == UnitsOfProduction {
			return apperr.New(apperr.KindConflict, "idle_units", "an asset depreciated by units of production has no idle months: record zero units")
		}
		if a.ThroughIndex >= from {
			return apperr.New(apperr.KindConflict, "month_depreciated", "depreciation is already booked for part of that span: void the runs first")
		}
		if err := s.repo.InsertIdleSpan(ctx, sp); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "fixedasset.idle_span_added", EntityType: "fixed_asset", EntityID: assetID.String(),
			Details: map[string]any{"from": from, "to": to, "source": IdleRuleSource}})
	})
	return sp, err
}

// IdleSpans lists an asset's idle spans.
func (s *Service) IdleSpans(ctx context.Context, assetID uuid.UUID) ([]IdleSpan, error) {
	if _, err := s.repo.Asset(ctx, assetID, false); err != nil {
		return nil, err
	}
	return s.repo.IdleSpans(ctx, assetID)
}

// RemoveIdleSpan deletes a span whose months are not depreciated yet.
func (s *Service) RemoveIdleSpan(ctx context.Context, assetID, spanID uuid.UUID, actor string) error {
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		a, err := s.repo.Asset(ctx, assetID, true)
		if err != nil {
			return err
		}
		spans, err := s.repo.IdleSpans(ctx, assetID)
		if err != nil {
			return err
		}
		for _, sp := range spans {
			if sp.ID != spanID {
				continue
			}
			if a.ThroughIndex >= sp.From {
				return apperr.New(apperr.KindConflict, "month_depreciated", "depreciation is already booked for part of that span: void the runs first")
			}
			if err := s.repo.DeleteIdleSpan(ctx, spanID); err != nil {
				return err
			}
			return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "fixedasset.idle_span_removed", EntityType: "fixed_asset", EntityID: assetID.String(), Details: map[string]any{"span": spanID.String()}})
		}
		return apperr.NotFound("idle span")
	})
}
