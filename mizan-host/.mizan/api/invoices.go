package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func (a *API) mountInvoices(r *httpx.Router, p string) {
	r.Route("POST "+p+"/invoices", func(w http.ResponseWriter, r *http.Request) error { return a.createInvoice(w, r, "") })
	r.Route("GET "+p+"/invoices", a.listInvoices)
	r.Route("GET "+p+"/invoices/{id}", func(w http.ResponseWriter, r *http.Request) error { return a.getInvoice(w, r, "") })
	r.Route("POST "+p+"/invoices/{id}/issue", a.issueInvoice)
	r.Route("POST "+p+"/invoices/{id}/void", a.voidInvoice)
	r.Route("POST "+p+"/invoices/{id}/payments", func(w http.ResponseWriter, r *http.Request) error { return a.payInvoice(w, r, "") })
	r.Route("POST "+p+"/invoices/{id}/pay-from-wallet", a.payInvoiceFromWalletStaff)
}

type itemReq struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
	Discount    int64  `json:"discount"`
	DiscountBP  int64  `json:"discount_bp"`
	TaxBP       *int   `json:"tax_bp"`
}

type createInvoiceReq struct {
	CustomerRef string         `json:"customer_ref"`
	Buyer       invoice.Buyer  `json:"buyer"`
	Currency    money.Currency `json:"currency"`
	Items       []itemReq      `json:"items"`
	DueDate     string         `json:"due_date"`
	Memo        string         `json:"memo"`
	Issue       bool           `json:"issue"` // issue right away
}

// createInvoice makes a draft (and issues it with "issue": true). owner restricts the customer for /me routes.
func (a *API) createInvoice(w http.ResponseWriter, r *http.Request, owner string) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[createInvoiceReq](r)
	if err != nil {
		return err
	}
	if req.Currency == "" {
		req.Currency = money.IRR
	}
	in := invoice.CreateInput{IdemKey: "api:" + key, CustomerRef: req.CustomerRef, Buyer: req.Buyer, Currency: req.Currency, Memo: req.Memo, Actor: actorOf(r.Context())}
	if owner != "" {
		in.CustomerRef = owner
	}
	for _, it := range req.Items {
		in.Items = append(in.Items, invoice.ItemInput{Description: it.Description, Quantity: it.Quantity, UnitPrice: it.UnitPrice, Discount: it.Discount, DiscountBP: it.DiscountBP, TaxBP: it.TaxBP})
	}
	if req.DueDate != "" {
		d, err := parseDate(req.DueDate)
		if err != nil {
			return err
		}
		in.DueDate = &d
	}
	inv, err := a.m.Invoices.Create(r.Context(), in)
	if err != nil {
		return err
	}
	if req.Issue {
		if inv, err = a.m.Invoices.Issue(r.Context(), inv.ID, in.Actor); err != nil {
			return err
		}
	}
	return created(w, toInvoice(inv))
}

func (a *API) listInvoices(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Invoices.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, page)
}

// load returns the invoice; owner, when set, must be its customer (404 otherwise, so ids do not leak).
func (a *API) load(r *http.Request, owner string) (invoice.Invoice, error) {
	id, err := pathID(r, "id")
	if err != nil {
		return invoice.Invoice{}, err
	}
	inv, err := a.m.Invoices.Get(r.Context(), id)
	if err != nil {
		return inv, err
	}
	if owner != "" && inv.CustomerRef != owner {
		return invoice.Invoice{}, notFound("invoice")
	}
	return inv, nil
}

func (a *API) getInvoice(w http.ResponseWriter, r *http.Request, owner string) error {
	inv, err := a.load(r, owner)
	if err != nil {
		return err
	}
	pays, err := a.m.Invoices.Payments(r.Context(), inv.ID)
	if err != nil {
		return err
	}
	type pay struct {
		Source    string     `json:"source"`
		Amount    int64      `json:"amount"`
		JournalID *uuid.UUID `json:"journal_id,omitempty"`
		At        time.Time  `json:"at"`
	}
	ps := make([]pay, len(pays))
	for i, x := range pays {
		ps[i] = pay{x.Source, x.Amount, x.JournalID, x.CreatedAt}
	}
	return ok(w, map[string]any{"invoice": toInvoice(inv), "payments": ps})
}

func (a *API) issueInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	inv, err := a.m.Invoices.Issue(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toInvoice(inv))
}

func (a *API) voidInvoice(w http.ResponseWriter, r *http.Request) error {
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
	inv, err := a.m.Invoices.Void(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toInvoice(inv))
}

// payInvoice starts a gateway payment for what is owed; the response carries the pay_url to redirect the payer to.
func (a *API) payInvoice(w http.ResponseWriter, r *http.Request, owner string) error {
	inv, err := a.load(r, owner)
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Gateway   string `json:"gateway"`
		ReturnURL string `json:"return_url"`
	}](r)
	if err != nil {
		return err
	}
	p, err := a.m.StartInvoicePayment(r.Context(), mizan.StartInvoicePaymentInput{InvoiceID: inv.ID, Gateway: req.Gateway, ReturnURL: req.ReturnURL, IdemKey: key, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toPayment(p))
}

func (a *API) payInvoiceFromWalletStaff(w http.ResponseWriter, r *http.Request) error {
	return a.payFromWallet(w, r, "")
}

func (a *API) payFromWallet(w http.ResponseWriter, r *http.Request, owner string) error {
	inv, err := a.load(r, owner)
	if err != nil {
		return err
	}
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		WalletID uuid.UUID `json:"wallet_id"`
	}](r)
	if err != nil {
		return err
	}
	if owner != "" { // a customer can only spend their own wallet
		wl, err := a.m.Wallets.Get(r.Context(), req.WalletID)
		if err != nil || wl.OwnerRef != owner {
			return notFound("wallet")
		}
	}
	out, err := a.m.PayInvoiceFromWallet(r.Context(), inv.ID, req.WalletID, key, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toInvoice(out))
}
