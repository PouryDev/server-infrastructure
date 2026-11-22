package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/treasury"
)

func (a *API) mountTreasury(r *httpx.Router, p string) {
	r.Route("POST "+p+"/bank-accounts", a.openBankAccount)
	r.Route("GET "+p+"/bank-accounts", a.listBankAccounts)
	r.Route("POST "+p+"/bank-accounts/{id}/statements", a.importStatement)
	r.Route("POST "+p+"/bank-accounts/{id}/auto-match", a.autoMatch)
	r.Route("GET "+p+"/treasury/position", a.cashPosition)

	r.Route("POST "+p+"/cheques", a.registerCheque)
	r.Route("GET "+p+"/cheques", a.listCheques)
	r.Route("GET "+p+"/cheques/{id}", a.getCheque)
	r.Route("POST "+p+"/cheques/{id}/deposit", a.depositCheque)
	r.Route("POST "+p+"/cheques/{id}/clear", a.clearCheque)
	r.Route("POST "+p+"/cheques/{id}/bounce", a.bounceCheque)
	r.Route("POST "+p+"/cheques/{id}/return", a.returnCheque)
	r.Route("POST "+p+"/cheques/{id}/endorse", a.endorseCheque)
	r.Route("POST "+p+"/cheques/{id}/take-back", a.takeBackCheque)

	r.Route("GET "+p+"/statement-lines", a.listLines)
	r.Route("GET "+p+"/statement-lines/{id}/suggestions", a.suggestions)
	r.Route("POST "+p+"/statement-lines/{id}/match", a.matchLine)

	r.Route("POST "+p+"/gateway-settlements", a.recordSettlement)
	r.Route("GET "+p+"/gateway-settlements", a.listSettlements)
}

// ---- bank accounts ----

type bankAccountDTO struct {
	ID         uuid.UUID      `json:"id"`
	Name       string         `json:"name"`
	BankName   string         `json:"bank_name,omitempty"`
	IBAN       string         `json:"iban,omitempty"`
	AccountNo  string         `json:"account_no,omitempty"`
	Currency   money.Currency `json:"currency"`
	LedgerCode string         `json:"ledger_code"`
	Active     bool           `json:"active"`
	Balance    *money.Money   `json:"balance,omitempty"`
}

func toBankAccount(b treasury.BankAccount, bal *money.Money) bankAccountDTO {
	return bankAccountDTO{b.ID, b.Name, b.BankName, b.IBAN, b.AccountNo, b.Currency, b.LedgerCode, b.Active, bal}
}

func (a *API) openBankAccount(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Name       string         `json:"name"`
		BankName   string         `json:"bank_name"`
		IBAN       string         `json:"iban"`
		AccountNo  string         `json:"account_no"`
		Currency   money.Currency `json:"currency"`
		LedgerCode string         `json:"ledger_code"`
	}](r)
	if err != nil {
		return err
	}
	acc, err := a.m.Treasury.OpenBankAccount(r.Context(), treasury.OpenAccountInput{Name: req.Name, BankName: req.BankName, IBAN: req.IBAN, AccountNo: req.AccountNo,
		Currency: req.Currency, LedgerCode: req.LedgerCode, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toBankAccount(acc, nil))
}

func (a *API) listBankAccounts(w http.ResponseWriter, r *http.Request) error {
	pos, err := a.m.Treasury.Position(r.Context())
	if err != nil {
		return err
	}
	out := make([]bankAccountDTO, len(pos.Accounts))
	for i, x := range pos.Accounts {
		bal := x.Balance
		out[i] = toBankAccount(x.Account, &bal)
	}
	return ok(w, out)
}

func (a *API) cashPosition(w http.ResponseWriter, r *http.Request) error {
	pos, err := a.m.Treasury.Position(r.Context())
	if err != nil {
		return err
	}
	type sum struct {
		Direction treasury.Direction    `json:"direction"`
		Status    treasury.ChequeStatus `json:"status"`
		Count     int                   `json:"count"`
		Total     money.Money           `json:"total"`
	}
	sums := make([]sum, len(pos.Cheques))
	for i, c := range pos.Cheques {
		sums[i] = sum{c.Direction, c.Status, c.Count, c.Total}
	}
	accs := make([]bankAccountDTO, len(pos.Accounts))
	for i, x := range pos.Accounts {
		bal := x.Balance
		accs[i] = toBankAccount(x.Account, &bal)
	}
	return ok(w, map[string]any{"accounts": accs, "banks": pos.Banks, "cheques": sums, "cheques_in": pos.ChequesIn, "cheques_out": pos.ChequesOut,
		"gateway_pending": pos.GatewayPending, "projected": pos.Projected})
}

// ---- cheques ----

type chequeDTO struct {
	ID              uuid.UUID             `json:"id"`
	Direction       treasury.Direction    `json:"direction"`
	Status          treasury.ChequeStatus `json:"status"`
	SayadID         string                `json:"sayad_id,omitempty"`
	Number          string                `json:"number"`
	Amount          money.Money           `json:"amount"`
	DueDate         string                `json:"due_date"`
	DueDateJalali   string                `json:"due_date_jalali"`
	CounterpartyRef string                `json:"counterparty_ref"`
	DrawerBank      string                `json:"drawer_bank,omitempty"`
	Note            string                `json:"note,omitempty"`
	BankAccountID   *uuid.UUID            `json:"bank_account_id,omitempty"`
	Reason          string                `json:"reason,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
}

func toCheque(c treasury.Cheque) chequeDTO {
	return chequeDTO{c.ID, c.Direction, c.Status, c.SayadID, c.Number, c.Amount, c.DueDate.Format("2006-01-02"), jdate(c.DueDate), c.CounterpartyRef,
		c.DrawerBank, c.Note, c.BankAccountID, c.Reason, c.CreatedAt}
}

// registerCheque records a received (direction=receivable) or issued (payable) cheque. due_date is Gregorian or Jalali.
func (a *API) registerCheque(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Direction       treasury.Direction `json:"direction"`
		SayadID         string             `json:"sayad_id"`
		Number          string             `json:"number"`
		Amount          money.Money        `json:"amount"`
		DueDate         string             `json:"due_date"`
		CounterpartyRef string             `json:"counterparty_ref"`
		DrawerBank      string             `json:"drawer_bank"`
		Note            string             `json:"note"`
		BankAccountID   *uuid.UUID         `json:"bank_account_id"`
		CounterAccount  string             `json:"counter_account"`
	}](r)
	if err != nil {
		return err
	}
	due, err := parseDate(req.DueDate)
	if err != nil {
		return apperr.InvalidField("due_date", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	c, err := a.m.Treasury.RegisterCheque(r.Context(), treasury.ChequeInput{IdemKey: "cheque:" + key, Direction: req.Direction, SayadID: req.SayadID,
		Number: req.Number, Amount: req.Amount, DueDate: due, CounterpartyRef: req.CounterpartyRef, DrawerBank: req.DrawerBank, Note: req.Note,
		BankAccountID: req.BankAccountID, CounterAccount: req.CounterAccount, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toCheque(c))
}

func (a *API) listCheques(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Treasury.ListCheques(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toCheque))
}

func (a *API) getCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := a.m.Treasury.Cheque(r.Context(), id)
	if err != nil {
		return err
	}
	hist, err := a.m.Treasury.ChequeHistory(r.Context(), id)
	if err != nil {
		return err
	}
	type ev struct {
		From  treasury.ChequeStatus `json:"from,omitempty"`
		To    treasury.ChequeStatus `json:"to"`
		Actor string                `json:"actor"`
		Note  string                `json:"note,omitempty"`
		At    time.Time             `json:"at"`
	}
	evs := make([]ev, len(hist))
	for i, h := range hist {
		evs[i] = ev{h.From, h.To, h.Actor, h.Note, h.At}
	}
	return ok(w, map[string]any{"cheque": toCheque(c), "history": evs})
}

// optDateBody parses an optional date of a request body; empty means today.
func optDateBody(s, field string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	t, err := parseDate(s)
	if err != nil {
		return time.Time{}, apperr.InvalidField(field, "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	return t, nil
}

func (a *API) depositCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		BankAccountID uuid.UUID `json:"bank_account_id"`
		Date          string    `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	c, err := a.m.Treasury.DepositCheque(r.Context(), id, req.BankAccountID, d, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

func (a *API) clearCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Date string `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	c, err := a.m.Treasury.ClearCheque(r.Context(), id, d, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

func (a *API) bounceCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string       `json:"reason"`
		Fee    *money.Money `json:"fee"`
		Date   string       `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	in := treasury.BounceInput{Reason: req.Reason, Date: d, Actor: actorOf(r.Context())}
	if req.Fee != nil {
		in.Fee = *req.Fee
	}
	c, err := a.m.Treasury.BounceCheque(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

func (a *API) returnCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Reason string `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	c, err := a.m.Treasury.ReturnCheque(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

// endorseCheque pays a supplier with a received cheque in hand.
func (a *API) endorseCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		To             string `json:"to"`
		CounterAccount string `json:"counter_account"`
		Date           string `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := treasury.EndorseInput{To: req.To, CounterAccount: req.CounterAccount, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	c, err := a.m.Treasury.EndorseCheque(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

// takeBackCheque records that the supplier returned an endorsed cheque.
func (a *API) takeBackCheque(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, err := a.m.Treasury.TakeBackCheque(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toCheque(c))
}

// ---- bank statements ----

type stmtLineDTO struct {
	ID          uuid.UUID           `json:"id"`
	AccountID   uuid.UUID           `json:"bank_account_id"`
	Date        string              `json:"date"`
	DateJalali  string              `json:"date_jalali"`
	Amount      int64               `json:"amount"`
	Description string              `json:"description,omitempty"`
	Reference   string              `json:"reference,omitempty"`
	Balance     *int64              `json:"balance,omitempty"`
	Status      treasury.LineStatus `json:"status"`
	MatchKind   treasury.MatchKind  `json:"match_kind,omitempty"`
	MatchID     string              `json:"match_id,omitempty"`
	MatchedBy   string              `json:"matched_by,omitempty"`
	JournalID   *uuid.UUID          `json:"journal_id,omitempty"`
}

func toStmtLine(l treasury.Line) stmtLineDTO {
	return stmtLineDTO{l.ID, l.AccountID, l.Date.Format("2006-01-02"), jdate(l.Date), l.Amount, l.Description, l.Reference, l.Balance, l.Status, l.MatchKind, l.MatchID, l.MatchedBy, l.JournalID}
}

// importStatement stores a bank statement of one account: CSV (Content-Type: text/csv, columns date, amount or
// credit and debit, description, reference, balance) or JSON {"lines": [...]}. Importing again adds only new lines.
func (a *API) importStatement(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var rows []treasury.StatementRow
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
		if rows, err = treasury.ParseStatementCSV(io.LimitReader(r.Body, 8<<20)); err != nil {
			return err
		}
	} else {
		req, err := httpx.Decode[struct {
			Lines []struct {
				Date        string `json:"date"`
				Amount      int64  `json:"amount"`
				Description string `json:"description"`
				Reference   string `json:"reference"`
				Balance     *int64 `json:"balance"`
			} `json:"lines"`
		}](r)
		if err != nil {
			return err
		}
		for i, l := range req.Lines {
			d, err := parseDate(l.Date)
			if err != nil {
				return apperr.InvalidField("lines", "line "+itoa(i+1)+": dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
			}
			rows = append(rows, treasury.StatementRow{Date: d, Amount: l.Amount, Description: l.Description, Reference: l.Reference, Balance: l.Balance})
		}
	}
	res, err := a.m.Treasury.ImportStatement(r.Context(), id, rows, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, map[string]int{"read": res.Read, "inserted": res.Inserted, "skipped": res.Skipped})
}

func (a *API) listLines(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Treasury.ListLines(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toStmtLine))
}

func (a *API) suggestions(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	cands, err := a.m.Treasury.Suggest(r.Context(), id)
	if err != nil {
		return err
	}
	type cand struct {
		Kind   treasury.MatchKind `json:"kind"`
		ID     uuid.UUID          `json:"item_id"`
		High   bool               `json:"high_confidence"`
		Reason string             `json:"reason"`
	}
	out := make([]cand, len(cands))
	for i, c := range cands {
		out[i] = cand{c.Kind, c.ID, c.High, c.Reason}
	}
	return ok(w, out)
}

func (a *API) matchLine(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Kind   treasury.MatchKind `json:"kind"`
		ItemID uuid.UUID          `json:"item_id"`
		Note   string             `json:"note"`
	}](r)
	if err != nil {
		return err
	}
	l, err := a.m.Treasury.MatchLine(r.Context(), treasury.MatchInput{LineID: id, Kind: req.Kind, ItemID: req.ItemID, Note: req.Note, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return ok(w, toStmtLine(l))
}

func (a *API) autoMatch(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := a.m.Treasury.AutoMatch(r.Context(), id, 1000, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"checked": res.Checked, "matched": res.Matched, "ambiguous": res.Ambiguous, "failed": res.Failed})
}

// ---- gateway settlements ----

type settlementDTO struct {
	ID        uuid.UUID   `json:"id"`
	Gateway   string      `json:"gateway"`
	Ref       string      `json:"ref"`
	AccountID uuid.UUID   `json:"bank_account_id"`
	Gross     money.Money `json:"gross"`
	Fee       money.Money `json:"fee"`
	Net       money.Money `json:"net"`
	SettledOn string      `json:"settled_on"`
	LineID    *uuid.UUID  `json:"line_id,omitempty"`
	Note      string      `json:"note,omitempty"`
	JournalID uuid.UUID   `json:"journal_id"`
}

func toSettlement(s treasury.Settlement) settlementDTO {
	return settlementDTO{s.ID, s.Gateway, s.Ref, s.AccountID, s.Gross, s.Fee, s.Net, s.SettledOn.Format("2006-01-02"), s.LineID, s.Note, s.JournalID}
}

// recordSettlement books a gateway payout to the bank. The gateway's reference is its idempotency key; the header
// is required like on every money-moving call.
func (a *API) recordSettlement(w http.ResponseWriter, r *http.Request) error {
	if _, err := idemKey(r); err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Gateway       string       `json:"gateway"`
		Ref           string       `json:"ref"`
		BankAccountID uuid.UUID    `json:"bank_account_id"`
		Gross         money.Money  `json:"gross"`
		Fee           *money.Money `json:"fee"`
		FeeVAT        *money.Money `json:"fee_vat"` // the part of the fee that is VAT (deductible input tax)
		SettledOn     string       `json:"settled_on"`
		Note          string       `json:"note"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.SettledOn, "settled_on")
	if err != nil {
		return err
	}
	in := treasury.SettlementInput{Gateway: req.Gateway, Ref: req.Ref, AccountID: req.BankAccountID, Gross: req.Gross, SettledOn: d, Note: req.Note, Actor: actorOf(r.Context())}
	if req.Fee != nil {
		in.Fee = *req.Fee
	}
	if req.FeeVAT != nil {
		in.FeeVAT = *req.FeeVAT
	}
	s, err := a.m.Treasury.RecordSettlement(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toSettlement(s))
}

func (a *API) listSettlements(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Treasury.ListSettlements(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toSettlement))
}
