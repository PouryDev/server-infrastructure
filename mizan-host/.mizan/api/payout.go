package api

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payout"
)

func (a *API) mountPayout(r *httpx.Router, p string) {
	r.Route("POST "+p+"/payout/sellers", a.addSeller)
	r.Route("GET "+p+"/payout/sellers", a.listSellers)
	r.Route("GET "+p+"/payout/sellers/{id}", a.getSeller)
	r.Route("PUT "+p+"/payout/sellers/{id}", a.updateSeller)
	r.Route("POST "+p+"/payout/sellers/{id}/block", a.blockSeller)
	r.Route("POST "+p+"/payout/sellers/{id}/unblock", a.unblockSeller)
	r.Route("GET "+p+"/payout/sellers/{id}/balance", a.sellerBalance)

	r.Route("POST "+p+"/payout/terms", a.addTerms)
	r.Route("GET "+p+"/payout/terms", a.listTerms)
	r.Route("GET "+p+"/payout/terms/at", a.termsAt)
	r.Route("DELETE "+p+"/payout/terms/{id}", a.deleteTerms)

	r.Route("POST "+p+"/payout/sales", a.recordSale)
	r.Route("GET "+p+"/payout/sales", a.listSales)
	r.Route("GET "+p+"/payout/sales/{id}", a.getSale)
	r.Route("POST "+p+"/payout/sales/{id}/deliver", a.deliverSale)
	r.Route("POST "+p+"/payout/sales/{id}/release", a.releaseSale)
	r.Route("POST "+p+"/payout/sales/{id}/return", a.returnSale)
	r.Route("POST "+p+"/payout/sales/{id}/partial-return", a.partialReturnSale)
	r.Route("POST "+p+"/payout/sales/{id}/return-paid", a.returnPaidSale)
	r.Route("GET "+p+"/payout/sellers/{id}/clawbacks", a.listClawbacks)
	r.Route("POST "+p+"/payout/clawbacks/{id}/recover", a.recoverClawback)
	r.Route("POST "+p+"/payout/batches/{id}/reject-item", a.rejectPayoutItem)

	r.Route("POST "+p+"/payout/batches", a.createBatch)
	r.Route("GET "+p+"/payout/batches", a.listBatches)
	r.Route("GET "+p+"/payout/batches/{id}", a.getBatch)
	r.Route("GET "+p+"/payout/batches/{id}/csv", a.batchCSV)
}

// ---- sellers ----

type sellerDTO struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Sheba       string    `json:"sheba,omitempty"`
	Status      string    `json:"status"`
	BlockReason string    `json:"block_reason,omitempty"`
	AccountCode string    `json:"account_code"`
}

func toSeller(s payout.Seller) sellerDTO {
	return sellerDTO{s.ID, s.Code, s.Name, s.Sheba, string(s.Status), s.BlockReason, s.AccountCode}
}

type sellerBody struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Sheba string `json:"sheba"`
}

func (a *API) addSeller(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[sellerBody](r)
	if err != nil {
		return err
	}
	s, err := a.m.Payout.AddSeller(r.Context(), payout.SellerInput{Code: req.Code, Name: req.Name, Sheba: req.Sheba, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toSeller(s))
}

func (a *API) listSellers(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payout.Sellers(r.Context())
	if err != nil {
		return err
	}
	out := make([]sellerDTO, len(list))
	for i, s := range list {
		out[i] = toSeller(s)
	}
	return ok(w, out)
}

func (a *API) getSeller(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Payout.Seller(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toSeller(s))
}

func (a *API) updateSeller(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[sellerBody](r)
	if err != nil {
		return err
	}
	s, err := a.m.Payout.UpdateSeller(r.Context(), id, req.Name, req.Sheba, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSeller(s))
}

func (a *API) blockSeller(w http.ResponseWriter, r *http.Request) error {
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
	s, err := a.m.Payout.Block(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSeller(s))
}

func (a *API) unblockSeller(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Payout.Unblock(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toSeller(s))
}

func (a *API) sellerBalance(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	b, err := a.m.Payout.Balance(r.Context(), id)
	if err != nil {
		return err
	}
	cur := b.Owed.Currency()
	m := func(v int64) money.Money { return money.Must(v, cur) }
	return ok(w, map[string]any{"owed": b.Owed, "awaiting_delivery": m(b.Totals.AwaitingDelivery), "held": m(b.Totals.Held),
		"available": m(b.Totals.Available), "paid_out": m(b.Totals.Paid)})
}

// ---- terms ----

type termsDTO struct {
	ID                  uuid.UUID `json:"id"`
	Name                string    `json:"name"`
	Scope               string    `json:"scope"`
	ScopeKey            string    `json:"scope_key,omitempty"`
	EffectiveFrom       string    `json:"effective_from"`
	EffectiveFromJalali string    `json:"effective_from_jalali"`
	RateBP              int       `json:"rate_bp"`
	HoldDays            int       `json:"hold_days"`
	VATBP               *int      `json:"vat_bp"` // null: follows the accounting VAT setting
	Source              string    `json:"source,omitempty"`
}

func toTerms(t payout.Terms) termsDTO {
	return termsDTO{t.ID, t.Name, string(t.Scope), t.ScopeKey, t.EffectiveFrom.Format("2006-01-02"), jdate(t.EffectiveFrom), t.RateBP, t.HoldDays, t.VATBP, t.Source}
}

// addTerms stores a new version of the commission rate and hold days. effective_from is Gregorian or Jalali; scope is
// default (no scope_key), category (scope_key = the category) or seller (scope_key = the seller id).
func (a *API) addTerms(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ScopeKey      string `json:"scope_key"`
		EffectiveFrom string `json:"effective_from"`
		RateBP        int    `json:"rate_bp"`
		HoldDays      int    `json:"hold_days"`
		VATBP         *int   `json:"vat_bp"`
		Source        string `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	from, err := parseDate(req.EffectiveFrom)
	if err != nil {
		return apperr.InvalidField("effective_from", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	t, err := a.m.Payout.AddTerms(r.Context(), payout.TermsInput{Name: req.Name, Scope: payout.ScopeKind(req.Scope), ScopeKey: req.ScopeKey, EffectiveFrom: from,
		RateBP: req.RateBP, HoldDays: req.HoldDays, VATBP: req.VATBP, Source: req.Source, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toTerms(t))
}

func (a *API) listTerms(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payout.Terms(r.Context())
	if err != nil {
		return err
	}
	out := make([]termsDTO, len(list))
	for i, t := range list {
		out[i] = toTerms(t)
	}
	return ok(w, out)
}

// termsAt shows the terms a sale of ?seller_id and ?category would get on ?date (default today).
func (a *API) termsAt(w http.ResponseWriter, r *http.Request) error {
	seller, err := uuid.Parse(r.URL.Query().Get("seller_id"))
	if err != nil {
		return apperr.InvalidField("seller_id", "required")
	}
	day, err := optDate(r, "date")
	if err != nil {
		return err
	}
	d := time.Now()
	if day != nil {
		d = *day
	}
	t, err := a.m.Payout.TermsFor(r.Context(), seller, r.URL.Query().Get("category"), d)
	if err != nil {
		return err
	}
	return ok(w, toTerms(t))
}

func (a *API) deleteTerms(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.m.Payout.DeleteTerms(r.Context(), id, actorOf(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- sales ----

type saleDTO struct {
	ID          uuid.UUID   `json:"id"`
	SellerID    uuid.UUID   `json:"seller_id"`
	Reference   string      `json:"reference,omitempty"`
	Category    string      `json:"category,omitempty"`
	Gross       money.Money `json:"gross"`
	Commission  money.Money `json:"commission"`
	VAT         money.Money `json:"vat"`
	Net         money.Money `json:"net"`
	RateBP      int         `json:"rate_bp"`
	HoldDays    int         `json:"hold_days"`
	Status      string      `json:"status"`
	Phase       string      `json:"phase"`
	SaleDate    string      `json:"sale_date"`
	DeliveredAt string      `json:"delivered_at,omitempty"`
	ReleaseAt   string      `json:"release_at,omitempty"`
	BatchID     *uuid.UUID  `json:"batch_id,omitempty"`
	ReturnWhy   string      `json:"return_reason,omitempty"`
}

func dayOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format("2006-01-02")
}

func (a *API) toSale(s payout.Sale) saleDTO {
	return saleDTO{s.ID, s.SellerID, s.Reference, s.Category, s.Gross, s.Commission, s.VAT, s.Net, s.RateBP, s.HoldDays, string(s.Status), s.Phase(a.m.Ledger.Today()),
		s.SaleDate.Format("2006-01-02"), dayOrEmpty(s.Delivered), dayOrEmpty(s.ReleaseAt), s.BatchID, s.ReturnWhy}
}

func (a *API) recordSale(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		SellerID    uuid.UUID   `json:"seller_id"`
		Gross       money.Money `json:"gross"`
		Reference   string      `json:"reference"`
		Category    string      `json:"category"`
		Date        string      `json:"date"`
		DeliveredAt string      `json:"delivered_at"`
	}](r)
	if err != nil {
		return err
	}
	date, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	delivered, err := optDateBody(req.DeliveredAt, "delivered_at")
	if err != nil {
		return err
	}
	in := payout.SaleInput{IdemKey: "sale:" + key, SellerID: req.SellerID, Gross: req.Gross, Reference: req.Reference, Category: req.Category, Date: date,
		Actor: actorOf(r.Context())}
	if !delivered.IsZero() {
		in.Delivered = &delivered
	}
	s, err := a.m.Payout.RecordSale(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, a.toSale(s))
}

func (a *API) listSales(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Payout.ListSales(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, a.toSale))
}

func (a *API) getSale(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Payout.Sale(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, a.toSale(s))
}

func (a *API) deliverSale(w http.ResponseWriter, r *http.Request) error {
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
	s, err := a.m.Payout.MarkDelivered(r.Context(), id, d, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, a.toSale(s))
}

func (a *API) releaseSale(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	s, err := a.m.Payout.ReleaseNow(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, a.toSale(s))
}

func (a *API) returnSale(w http.ResponseWriter, r *http.Request) error {
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
	s, err := a.m.Payout.ReturnSale(r.Context(), id, strings.TrimSpace(req.Reason), actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, a.toSale(s))
}

type clawbackDTO struct {
	ID          uuid.UUID  `json:"id"`
	SaleID      uuid.UUID  `json:"sale_id"`
	SellerID    uuid.UUID  `json:"seller_id"`
	BatchID     *uuid.UUID `json:"batch_id,omitempty"`
	Amount      int64      `json:"amount"`
	Recovered   int64      `json:"recovered"`
	Outstanding int64      `json:"outstanding"`
	Currency    string     `json:"currency"`
	Reason      string     `json:"reason,omitempty"`
}

func toClawback(c payout.Clawback) clawbackDTO {
	return clawbackDTO{c.ID, c.SaleID, c.SellerID, c.BatchID, c.Amount.Amount(), c.Recovered.Amount(), c.Outstanding(), string(c.Amount.Currency()), c.Reason}
}

// partialReturnSale gives the customer back part of a sale (Idempotency-Key); after the payout the seller's share of it becomes a clawback.
func (a *API) partialReturnSale(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Gross  money.Money `json:"gross"`
		Reason string      `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	s, err := a.m.Payout.PartialReturn(r.Context(), id, payout.ReturnInput{IdemKey: key, Gross: req.Gross, Reason: req.Reason, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return ok(w, a.toSale(s))
}

// returnPaidSale returns a sale whose seller was already paid: the seller's share becomes a clawback.
func (a *API) returnPaidSale(w http.ResponseWriter, r *http.Request) error {
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
	c, err := a.m.Payout.ReturnPaidSale(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toClawback(c))
}

func (a *API) listClawbacks(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	cs, err := a.m.Payout.Clawbacks(r.Context(), id, r.URL.Query().Get("open") == "true")
	if err != nil {
		return err
	}
	out := make([]clawbackDTO, len(cs))
	for i, c := range cs {
		out[i] = toClawback(c)
	}
	return ok(w, map[string]any{"items": out})
}

// recoverClawback takes money back from a seller (Idempotency-Key): cash in, or an offset against what the platform owes the seller.
func (a *API) recoverClawback(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Amount money.Money `json:"amount"`
		Offset bool        `json:"offset"`
		Into   string      `json:"into"`
		Date   string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	in := payout.RecoverInput{IdemKey: key, Amount: req.Amount, Offset: req.Offset, Into: req.Into, Actor: actorOf(r.Context())}
	if in.Date, err = optDay(req.Date); err != nil {
		return err
	}
	c, err := a.m.Payout.RecoverClawback(r.Context(), id, in)
	if err != nil {
		return err
	}
	return ok(w, toClawback(c))
}

// rejectPayoutItem records that the bank rejected the transfer to one seller of a batch.
func (a *API) rejectPayoutItem(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		SellerID uuid.UUID `json:"seller_id"`
		Reason   string    `json:"reason"`
	}](r)
	if err != nil {
		return err
	}
	b, err := a.m.Payout.RejectPayoutItem(r.Context(), id, req.SellerID, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toBatch(b))
}

// ---- batches ----

type batchItemDTO struct {
	SellerID  uuid.UUID   `json:"seller_id"`
	Sheba     string      `json:"sheba,omitempty"`
	Amount    money.Money `json:"amount"`
	SaleCount int         `json:"sale_count"`
	Rejected  bool        `json:"rejected,omitempty"`
	Reason    string      `json:"reject_reason,omitempty"`
}

type batchDTO struct {
	ID        uuid.UUID      `json:"id"`
	Date      string         `json:"date"`
	DateJal   string         `json:"date_jalali"`
	Total     money.Money    `json:"total"`
	SaleCount int            `json:"sale_count"`
	PayFrom   string         `json:"pay_from"`
	Items     []batchItemDTO `json:"items"`
}

func toBatch(b payout.Batch) batchDTO {
	items := make([]batchItemDTO, len(b.Items))
	for i, it := range b.Items {
		items[i] = batchItemDTO{it.SellerID, it.Sheba, it.Amount, it.SaleCount, it.Rejected, it.RejectReason}
	}
	return batchDTO{b.ID, b.Date.Format("2006-01-02"), jdate(b.Date), b.Total, b.SaleCount, b.PayFrom, items}
}

// createBatch pays every active seller whose released sales are due; the response lists who gets how much and where.
func (a *API) createBatch(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		BankAccountID *uuid.UUID  `json:"bank_account_id"`
		Date          string      `json:"date"`
		SellerIDs     []uuid.UUID `json:"seller_ids"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	from, err := a.payFromOf(r, req.BankAccountID)
	if err != nil {
		return err
	}
	b, err := a.m.Payout.CreatePayout(r.Context(), payout.PayoutInput{IdemKey: "payout:" + key, PayFrom: from, Date: d, SellerIDs: req.SellerIDs, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toBatch(b))
}

func (a *API) listBatches(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payout.Payouts(r.Context())
	if err != nil {
		return err
	}
	out := make([]batchDTO, len(list))
	for i, b := range list {
		out[i] = toBatch(b)
	}
	return ok(w, out)
}

func (a *API) getBatch(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	b, err := a.m.Payout.Payout(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toBatch(b))
}

// batchCSV exports a batch as plain CSV (seller, IBAN, amount) that any bank's bulk-transfer tool or spreadsheet can start from.
// Banks differ in their own file formats; none is assumed here.
func (a *API) batchCSV(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	b, err := a.m.Payout.Payout(r.Context(), id)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="payout-`+b.ID.String()+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"seller_code", "seller_name", "sheba", "amount", "currency", "sale_count"})
	for _, it := range b.Items {
		s, err := a.m.Payout.Seller(r.Context(), it.SellerID)
		if err != nil {
			return err
		}
		_ = cw.Write([]string{csvSafe(s.Code), csvSafe(s.Name), it.Sheba, strconv.FormatInt(it.Amount.Amount(), 10), string(it.Amount.Currency()), itoa(it.SaleCount)})
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe stops a seller-chosen text from being run as a formula when the file is opened in a spreadsheet.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
