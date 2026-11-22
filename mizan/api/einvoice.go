package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/einvoice"
)

func (a *API) mountEInvoice(r *httpx.Router, p string) {
	r.Route("POST "+p+"/einvoice-requests/invoice/{id}", a.enqueueEInvoice)
	r.Route("GET "+p+"/einvoices", a.listEInvoices)
	r.Route("GET "+p+"/einvoices/{id}", a.getEInvoice)
	r.Route("POST "+p+"/einvoices/{id}/requeue", a.requeueEInvoice)
	r.Route("POST "+p+"/einvoices/send", a.sendEInvoices)
	r.Route("POST "+p+"/einvoices/refresh", a.refreshEInvoices)
}

type einvoiceDTO struct {
	ID              uuid.UUID        `json:"id"`
	SourceType      string           `json:"source_type"`
	SourceID        string           `json:"source_id"`
	Status          string           `json:"status"`
	TraceID         uuid.UUID        `json:"trace_id"`
	ReferenceNumber string           `json:"reference_number,omitempty"`
	Errors          []einvoice.Issue `json:"errors"`
	Warnings        []einvoice.Issue `json:"warnings"`
	Attempts        int              `json:"attempts"`
	LastError       string           `json:"last_error,omitempty"`
	SentAt          *time.Time       `json:"sent_at,omitempty"`
}

func toEInvoice(d einvoice.Document) einvoiceDTO {
	errs, warns := d.Errors, d.Warnings
	if errs == nil {
		errs = []einvoice.Issue{}
	}
	if warns == nil {
		warns = []einvoice.Issue{}
	}
	return einvoiceDTO{d.ID, d.SourceType, d.SourceID, string(d.Status), d.TraceID, d.ReferenceNumber, errs, warns, d.Attempts, d.LastError, d.SentAt}
}

// enqueueEInvoice queues an issued invoice for the tax authority; repeating it returns the same e-invoice.
func (a *API) enqueueEInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var opt mizan.EInvoiceOptions
	if r.ContentLength != 0 {
		req, err := httpx.Decode[struct {
			Pattern    int    `json:"pattern"`
			Subject    int    `json:"subject"`
			RefTaxID   string `json:"ref_taxid"`
			Settlement int    `json:"settlement"`
			CashAmount int64  `json:"cash_amount"`
			ContractNo string `json:"contract_no"`
			// Details: the provider details (bill_id, flight_type, policy_no, exchange_notice_no/date, registered_at, payments, customs, buyer {kind, passport_no}) and per-line
			// extras in "lines" (wage_fee, seller_profit, commission, export, fx, other_tax_*, other_levy_*, petro_base_e4), in the order of the invoice items.
			Details *einvoice.Invoice `json:"details"`
		}](r)
		if err != nil {
			return err
		}
		opt = mizan.EInvoiceOptions{Pattern: req.Pattern, Subject: req.Subject, RefTaxID: req.RefTaxID, Settlement: req.Settlement, CashAmount: req.CashAmount, ContractNo: req.ContractNo, Details: req.Details}
	}
	d, err := a.m.EnqueueInvoiceWith(r.Context(), id, actorOf(r.Context()), opt)
	if err != nil {
		return err
	}
	return created(w, toEInvoice(d))
}

func (a *API) listEInvoices(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.EInvoice.List(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toEInvoice))
}

func (a *API) getEInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := a.m.EInvoice.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toEInvoice(d))
}

// requeueEInvoice sends a rejected e-invoice again under a new trace id; the invoice content is re-read from the source invoice.
func (a *API) requeueEInvoice(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := a.m.RequeueEInvoice(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toEInvoice(d))
}

// sendEInvoices and refreshEInvoices run one round of what the background runner does, on demand.
func (a *API) sendEInvoices(w http.ResponseWriter, r *http.Request) error {
	n, err := a.m.EInvoice.SendQueued(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"sent": n})
}

func (a *API) refreshEInvoices(w http.ResponseWriter, r *http.Request) error {
	n, err := a.m.EInvoice.RefreshSent(r.Context())
	if err != nil {
		return err
	}
	return ok(w, map[string]int{"settled": n})
}
