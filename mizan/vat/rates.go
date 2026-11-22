package vat

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// CategoryGeneral is the standard rate of art. 7; other categories are the special rates of art. 26 (petrol, gold fee, drinks...) or any the operator adds.
const CategoryGeneral = "general"

// RateVersion is the rate of a category from EffectiveFrom until the next version of the same category.
type RateVersion struct {
	ID            uuid.UUID
	Category      string
	EffectiveFrom time.Time
	RateBP        int  // total tax and levies, in basis points (1000 = 10%)
	LevyBP        *int // the municipal-levy part of RateBP; nil when not known
	Description   string
	Verified      bool // true only when the number was read from the law text itself
	Source        string
	CreatedAt     time.Time
}

// RateInput adds a rate version.
type RateInput struct {
	Category      string
	EffectiveFrom time.Time
	RateBP        int
	LevyBP        *int
	Description   string
	Verified      bool
	Source        string
	Actor         string
}

func (in RateInput) validate() error {
	var fe []apperr.FieldError
	if strings.TrimSpace(in.Category) == "" {
		fe = append(fe, apperr.FieldError{Field: "category", Message: "required"})
	}
	if in.EffectiveFrom.IsZero() {
		fe = append(fe, apperr.FieldError{Field: "effective_from", Message: "required"})
	}
	if in.RateBP < 0 || in.RateBP > 20000 {
		fe = append(fe, apperr.FieldError{Field: "rate_bp", Message: "must be between 0 and 20000"})
	}
	if in.LevyBP != nil && (*in.LevyBP < 0 || *in.LevyBP > in.RateBP) {
		fe = append(fe, apperr.FieldError{Field: "levy_bp", Message: "must be between 0 and rate_bp"})
	}
	if strings.TrimSpace(in.Source) == "" {
		fe = append(fe, apperr.FieldError{Field: "source", Message: "required: the law or decision this rate comes from"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid rate", fe...)
	}
	return nil
}

// Exemption is an exempt supply (art. 9) as versioned data: EffectiveTo nil means still in force.
type Exemption struct {
	ID            uuid.UUID
	Code          string
	Kind          string // goods | services
	Description   string
	Article       string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Source        string
	CreatedAt     time.Time
}

// ExemptionInput adds an exemption version.
type ExemptionInput struct {
	Code          string
	Kind          string
	Description   string
	Article       string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Source        string
	Actor         string
}

func (in ExemptionInput) validate() error {
	var fe []apperr.FieldError
	if strings.TrimSpace(in.Code) == "" {
		fe = append(fe, apperr.FieldError{Field: "code", Message: "required"})
	}
	if in.Kind != "goods" && in.Kind != "services" {
		fe = append(fe, apperr.FieldError{Field: "kind", Message: "must be goods or services"})
	}
	if strings.TrimSpace(in.Description) == "" || strings.TrimSpace(in.Article) == "" || strings.TrimSpace(in.Source) == "" {
		fe = append(fe, apperr.FieldError{Field: "description", Message: "description, article and source are required"})
	}
	if in.EffectiveFrom.IsZero() || (in.EffectiveTo != nil && in.EffectiveTo.Before(in.EffectiveFrom)) {
		fe = append(fe, apperr.FieldError{Field: "effective_from", Message: "required, and not after effective_to"})
	}
	if len(fe) > 0 {
		return apperr.Invalid("invalid exemption", fe...)
	}
	return nil
}

// AddRate stores a new rate version. Versions are immutable.
func (s *Service) AddRate(ctx context.Context, in RateInput) (RateVersion, error) {
	if err := in.validate(); err != nil {
		return RateVersion{}, err
	}
	r := RateVersion{ID: uuid.New(), Category: strings.TrimSpace(in.Category), EffectiveFrom: dateOnly(in.EffectiveFrom), RateBP: in.RateBP, LevyBP: in.LevyBP,
		Description: in.Description, Verified: in.Verified, Source: strings.TrimSpace(in.Source), CreatedAt: s.clock()}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertRate(ctx, r); err != nil {
			return err
		}
		return s.audit.Record(ctx, trailEntry(in.Actor, "vat.rate_added", "vat_rate", r.ID.String(), map[string]any{"category": r.Category, "rate_bp": r.RateBP, "effective_from": r.EffectiveFrom.Format("2006-01-02")}))
	})
	return r, err
}

// Rates lists every version.
func (s *Service) Rates(ctx context.Context) ([]RateVersion, error) { return s.repo.Rates(ctx) }

// RateAt returns the version of a category in force on a day; ok is false when none applies.
func (s *Service) RateAt(ctx context.Context, category string, day time.Time) (RateVersion, bool, error) {
	rs, err := s.repo.Rates(ctx)
	if err != nil {
		return RateVersion{}, false, err
	}
	var best RateVersion
	found := false
	for _, r := range rs {
		if r.Category == category && !r.EffectiveFrom.After(day) && (!found || r.EffectiveFrom.After(best.EffectiveFrom)) {
			best, found = r, true
		}
	}
	return best, found, nil
}

// Split is a tax amount cut into the state share and the municipal levy.
type Split struct {
	Total, State, Levy int64
	RateBP             int
	LevyKnown          bool
}

// Compute returns the tax on a base (rials, before tax) at the rate of a category on a day, rounded down to a whole rial, and its split when the levy part is
// known. Unknown category or date is a not-found error.
func (s *Service) Compute(ctx context.Context, category string, base int64, day time.Time) (Split, error) {
	r, ok, err := s.RateAt(ctx, category, dateOnly(day))
	if err != nil {
		return Split{}, err
	}
	if !ok {
		return Split{}, apperr.NotFound("rate")
	}
	sp := Split{RateBP: r.RateBP, Total: base * int64(r.RateBP) / 10000}
	if r.LevyBP != nil {
		sp.LevyKnown = true
		sp.Levy = base * int64(*r.LevyBP) / 10000
		sp.State = sp.Total - sp.Levy
	}
	return sp, nil
}

// AddExemption stores an exemption version.
func (s *Service) AddExemption(ctx context.Context, in ExemptionInput) (Exemption, error) {
	if err := in.validate(); err != nil {
		return Exemption{}, err
	}
	e := Exemption{ID: uuid.New(), Code: strings.TrimSpace(in.Code), Kind: in.Kind, Description: in.Description, Article: in.Article, EffectiveFrom: dateOnly(in.EffectiveFrom),
		Source: strings.TrimSpace(in.Source), CreatedAt: s.clock()}
	if in.EffectiveTo != nil {
		t := dateOnly(*in.EffectiveTo)
		e.EffectiveTo = &t
	}
	err := s.tx.WithTx(ctx, func(ctx context.Context) error {
		if err := s.repo.InsertExemption(ctx, e); err != nil {
			return err
		}
		return s.audit.Record(ctx, trailEntry(in.Actor, "vat.exemption_added", "vat_exemption", e.ID.String(), map[string]any{"code": e.Code}))
	})
	return e, err
}

// Exemptions lists the versions; on is the day they must be in force (zero = all).
func (s *Service) Exemptions(ctx context.Context, on time.Time) ([]Exemption, error) {
	all, err := s.repo.Exemptions(ctx)
	if err != nil || on.IsZero() {
		return all, err
	}
	day := dateOnly(on)
	var out []Exemption
	for _, e := range all {
		if !e.EffectiveFrom.After(day) && (e.EffectiveTo == nil || !e.EffectiveTo.Before(day)) {
			out = append(out, e)
		}
	}
	return out, nil
}
