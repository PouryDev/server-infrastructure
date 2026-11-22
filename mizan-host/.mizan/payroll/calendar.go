package payroll

import (
	"context"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
	"github.com/PouryDev/server-infrastructure/mizan/trail"
)

// Holiday is an official holiday by Jalali date. The lunar ones move every year: the calendar is data, a new year needs a new list (the Council of Public Culture publishes it).
type Holiday struct {
	Year, Month, Day int
	Name             string
	Kind             string // "official" or "workers" (a holiday for workers only, Labour Law art. 63)
	Verified         bool
	Source           string
}

// HolidayRepository is the persistence port of the calendar (implemented by the same adapter as Repository).
type HolidayRepository interface {
	InsertHoliday(ctx context.Context, h Holiday) error
	Holidays(ctx context.Context, year int) ([]Holiday, error)
	DeleteHoliday(ctx context.Context, year, month, day int) error
}

func (s *Service) cal() (HolidayRepository, error) {
	h, ok := s.repo.(HolidayRepository)
	if !ok {
		return nil, apperr.New(apperr.KindConflict, "no_calendar", "the holiday calendar is not available with this repository")
	}
	return h, nil
}

// AddHoliday adds a holiday (a taken date is a conflict).
func (s *Service) AddHoliday(ctx context.Context, h Holiday, actor string) (Holiday, error) {
	hr, err := s.cal()
	if err != nil {
		return Holiday{}, err
	}
	h.Name, h.Source = strings.TrimSpace(h.Name), strings.TrimSpace(h.Source)
	if h.Kind == "" {
		h.Kind = "official"
	}
	if (h.Kind != "official" && h.Kind != "workers") || h.Name == "" || h.Source == "" {
		return Holiday{}, apperr.Invalid("a name, a source and a kind of official or workers are required")
	}
	if !(jalali.Date{Year: h.Year, Month: h.Month, Day: h.Day}).Valid() {
		return Holiday{}, apperr.Invalid("not a valid Jalali date")
	}
	err = s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := hr.InsertHoliday(ctx, h); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "payroll.holiday_added", EntityType: "payroll_holiday", EntityID: jalali.Date{Year: h.Year, Month: h.Month, Day: h.Day}.String(), Details: map[string]any{"name": h.Name}})
	})
	return h, err
}

// Holidays lists a Jalali year's holidays.
func (s *Service) Holidays(ctx context.Context, year int) ([]Holiday, error) {
	hr, err := s.cal()
	if err != nil {
		return nil, err
	}
	return hr.Holidays(ctx, year)
}

// DeleteHoliday removes a holiday added by mistake.
func (s *Service) DeleteHoliday(ctx context.Context, year, month, day int, actor string) error {
	hr, err := s.cal()
	if err != nil {
		return err
	}
	return s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := hr.DeleteHoliday(ctx, year, month, day); err != nil {
			return err
		}
		return s.audit.Record(ctx, trail.Entry{Actor: actor, Action: "payroll.holiday_deleted", EntityType: "payroll_holiday", EntityID: jalali.Date{Year: year, Month: month, Day: day}.String()})
	})
}
