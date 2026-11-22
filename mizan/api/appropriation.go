package api

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountAppropriation(r *httpx.Router, p string) {
	r.Route("GET "+p+"/accounting/profit-appropriation/preview/{fiscal_year_id}", a.appropriationPreview)
	r.Route("POST "+p+"/accounting/profit-appropriation", a.declareAppropriation)
	r.Route("GET "+p+"/accounting/profit-appropriation", a.listAppropriations)
	r.Route("GET "+p+"/accounting/profit-appropriation/{id}", a.getAppropriation)
	r.Route("POST "+p+"/accounting/profit-appropriation/{id}/payments", a.payDividend)
}

func yearIDParam(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("fiscal_year_id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, apperr.Invalid("invalid fiscal_year_id")
	}
	return id, nil
}

// appropriationPreview applies articles 140, 238, 239, 90 and 241 of the Commercial Code to the books of a closed year.
func (a *API) appropriationPreview(w http.ResponseWriter, r *http.Request) error {
	id, err := yearIDParam(r)
	if err != nil {
		return err
	}
	facts, name, err := a.m.AppropriationFacts(r.Context(), id, r.URL.Query().Get("public_company") == "true")
	if err != nil {
		return err
	}
	pl := a.m.Appropriate.Preview(facts)
	return ok(w, map[string]any{"fiscal_year": name, "net_profit": facts.NetProfit, "retained_at_start": facts.RetainedAtStart, "capital": facts.Capital,
		"legal_reserve_balance": facts.LegalReserve, "legal_reserve_required": pl.LegalReserve, "distributable": pl.Distributable,
		"mandatory_minimum_dividend": pl.MinDividend, "max_directors_bonus_basis_points_of_dividend": pl.MaxBonusBP})
}

type appropriationDTO struct {
	ID              uuid.UUID  `json:"id"`
	FiscalYear      string     `json:"fiscal_year"`
	DecidedOn       string     `json:"decided_on"`
	PayBy           string     `json:"pay_by"`
	NetProfit       int64      `json:"net_profit"`
	LegalReserve    int64      `json:"legal_reserve"`
	OptionalReserve int64      `json:"optional_reserve"`
	Dividend        int64      `json:"dividend"`
	Paid            int64      `json:"paid"`
	Outstanding     int64      `json:"outstanding"`
	Overdue         bool       `json:"overdue"`
	JournalID       *uuid.UUID `json:"journal_id,omitempty"`
}

func (a *API) toAppropriation(x appropriation.Record) appropriationDTO {
	return appropriationDTO{x.ID, x.FiscalYear, x.DecidedOn.Format("2006-01-02"), x.PayBy.Format("2006-01-02"), x.Facts.NetProfit, x.LegalReserve, x.OptionalReserve,
		x.Dividend, x.Paid, x.Outstanding(), a.m.Appropriate.Overdue(x), x.Journal}
}

func (a *API) declareAppropriation(w http.ResponseWriter, r *http.Request) error {
	if err := a.m.RequireNoApproval(); err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		FiscalYearID    int64       `json:"fiscal_year_id"`
		PublicCompany   bool        `json:"public_company"`
		DecidedOn       string      `json:"decided_on"`
		Dividend        money.Money `json:"dividend"`
		OptionalReserve money.Money `json:"optional_reserve"`
	}](r)
	if err != nil {
		return err
	}
	decided, err := optDay(req.DecidedOn)
	if err != nil {
		return err
	}
	div, opt := int64(0), int64(0)
	if req.Dividend.Currency() != "" {
		div = req.Dividend.Amount()
	}
	if req.OptionalReserve.Currency() != "" {
		opt = req.OptionalReserve.Amount()
	}
	rec, err := a.m.DeclareAppropriation(r.Context(), req.FiscalYearID, req.PublicCompany, decided, appropriation.Decision{Dividend: div, OptionalReserve: opt}, key, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, a.toAppropriation(rec))
}

func (a *API) listAppropriations(w http.ResponseWriter, r *http.Request) error {
	rs, err := a.m.Appropriate.Records(r.Context())
	if err != nil {
		return err
	}
	out := make([]appropriationDTO, len(rs))
	for i, x := range rs {
		out[i] = a.toAppropriation(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getAppropriation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.Appropriate.Record(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, a.toAppropriation(x))
}

func (a *API) payDividend(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount      money.Money `json:"amount"`
		FromAccount string      `json:"from_account"`
		Date        string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := appropriation.PayInput{IdemKey: key, Amount: req.Amount, FromAccount: req.FromAccount, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	x, err := a.m.Appropriate.Pay(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, a.toAppropriation(x))
}
