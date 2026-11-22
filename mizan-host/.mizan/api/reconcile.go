package api

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/reconcile"
)

func (a *API) mountReconcile(r *httpx.Router, p string) {
	r.Route("POST "+p+"/reconciliations", a.reconcileRun)
	r.Route("GET "+p+"/reconciliations/{id}", a.getReconciliation)
	r.Route("GET "+p+"/disputes", a.listDisputes)
	r.Route("POST "+p+"/disputes", a.openDispute)
	r.Route("POST "+p+"/disputes/{id}/notes", a.disputeNote)
	r.Route("POST "+p+"/disputes/{id}/close", a.closeDispute)
	r.Route("GET "+p+"/audit/{type}/{id}", a.auditTrail)
}

// reconcileRun compares our payments of a period with the gateway's statement. The statement is either a JSON
// list of items or a CSV (Content-Type: text/csv) with the header ref,amount,status,txn_ref,time; the
// gateway and period come from the query string: ?gateway=zarinpal&from=2026-11-01&to=2026-11-08 (to is exclusive).
func (a *API) reconcileRun(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	from, err := parseDate(q.Get("from"))
	if err != nil {
		return apperr.InvalidField("from", "required date")
	}
	to, err := parseDate(q.Get("to"))
	if err != nil {
		return apperr.InvalidField("to", "required date")
	}
	var items []reconcile.Remote
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
		if items, err = reconcile.ParseCSV(io.LimitReader(r.Body, 8<<20)); err != nil {
			return err
		}
	} else {
		req, err := httpx.Decode[struct {
			Items []struct {
				Ref    string `json:"ref"`
				TxnRef string `json:"txn_ref"`
				Amount int64  `json:"amount"`
				Paid   bool   `json:"paid"`
			} `json:"items"`
		}](r)
		if err != nil {
			return err
		}
		for _, it := range req.Items {
			items = append(items, reconcile.Remote{GatewayRef: it.Ref, TxnRef: it.TxnRef, Amount: it.Amount, Paid: it.Paid})
		}
	}
	run, found, err := a.m.Reconcile.Reconcile(r.Context(), q.Get("gateway"), from, to, items, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, runDTO(run, found))
}

type itemOut struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	PaymentID     *uuid.UUID `json:"payment_id,omitempty"`
	GatewayRef    string     `json:"gateway_ref"`
	OurAmount     *int64     `json:"our_amount,omitempty"`
	GatewayAmount *int64     `json:"gateway_amount,omitempty"`
	Details       string     `json:"details"`
}

func runDTO(run reconcile.Run, items []reconcile.Item) map[string]any {
	out := make([]itemOut, len(items))
	for i, it := range items {
		out[i] = itemOut{it.ID, string(it.Kind), it.PaymentID, it.GatewayRef, it.OurAmount, it.GatewayAmount, it.Details}
	}
	return map[string]any{"id": run.ID, "gateway": run.Gateway, "from": run.From, "to": run.To, "summary": run.Summary, "created_at": run.CreatedAt, "items": out}
}

func (a *API) getReconciliation(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	run, items, err := a.m.Reconcile.Run(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, runDTO(run, items))
}

type disputeOut struct {
	ID         uuid.UUID               `json:"id"`
	PaymentID  *uuid.UUID              `json:"payment_id,omitempty"`
	ReconItem  *int64                  `json:"recon_item,omitempty"`
	Kind       string                  `json:"kind"`
	Status     reconcile.DisputeStatus `json:"status"`
	OpenedBy   string                  `json:"opened_by"`
	Notes      []reconcile.Note        `json:"notes"`
	Resolution string                  `json:"resolution,omitempty"`
	CreatedAt  time.Time               `json:"created_at"`
	UpdatedAt  time.Time               `json:"updated_at"`
}

func toDispute(d reconcile.Dispute) disputeOut {
	if d.Notes == nil {
		d.Notes = []reconcile.Note{}
	}
	return disputeOut{d.ID, d.PaymentID, d.ReconItem, d.Kind, d.Status, d.OpenedBy, d.Notes, d.Resolution, d.CreatedAt, d.UpdatedAt}
}

func (a *API) openDispute(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		PaymentID *uuid.UUID `json:"payment_id"`
		ReconItem *int64     `json:"recon_item"`
		Kind      string     `json:"kind"`
		Note      string     `json:"note"`
	}](r)
	if err != nil {
		return err
	}
	d, err := a.m.Reconcile.OpenDispute(r.Context(), req.PaymentID, req.ReconItem, req.Kind, req.Note, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, toDispute(d))
}

func (a *API) listDisputes(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Reconcile.Disputes(r.Context(), r.URL.Query().Get("status"), 100)
	if err != nil {
		return err
	}
	out := make([]disputeOut, len(list))
	for i, d := range list {
		out[i] = toDispute(d)
	}
	return ok(w, out)
}

func (a *API) disputeNote(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Text string `json:"text"`
	}](r)
	if err != nil {
		return err
	}
	d, err := a.m.Reconcile.AddNote(r.Context(), id, req.Text, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toDispute(d))
}

func (a *API) closeDispute(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Status     reconcile.DisputeStatus `json:"status"`
		Resolution string                  `json:"resolution"`
	}](r)
	if err != nil {
		return err
	}
	d, err := a.m.Reconcile.Close(r.Context(), id, req.Status, req.Resolution, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toDispute(d))
}

func (a *API) auditTrail(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.m.Audit.For(r.Context(), r.PathValue("type"), r.PathValue("id"), 200)
	if err != nil {
		return err
	}
	return ok(w, rows)
}
