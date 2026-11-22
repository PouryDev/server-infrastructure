package api

import (
	"encoding/json"
	"net/http"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan"
	"github.com/PouryDev/server-infrastructure/mizan/approval"
)

func (a *API) mountApprovals(r *httpx.Router, p string) {
	r.Route("POST "+p+"/approvals", a.requestApproval)
	r.Route("GET "+p+"/approvals", a.listApprovals)
	r.Route("GET "+p+"/approvals/{id}", a.getApproval)
	r.Route("POST "+p+"/approvals/{id}/approve", a.approve)
	r.Route("POST "+p+"/approvals/{id}/reject", a.rejectApproval)
}

func approvalDTO(x approval.Approval) map[string]any {
	m := map[string]any{"id": x.ID, "kind": x.Kind, "subject": x.Subject, "status": string(x.Status), "requested_by": x.RequestedBy, "payload": x.Payload, "created_at": x.CreatedAt}
	if x.DecidedBy != "" {
		m["decided_by"], m["decided_at"] = x.DecidedBy, x.DecidedAt
	}
	if x.Reason != "" {
		m["reason"] = x.Reason
	}
	if len(x.Result) > 0 {
		m["result"] = x.Result
	}
	return m
}

// requestApproval files a request: {"kind":"year_close","payload":{"fiscal_year_id":1}} or {"kind":"appropriation","payload":{...}}.
func (a *API) requestApproval(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}](r)
	if err != nil {
		return err
	}
	var fy int64
	switch req.Kind {
	case mizan.ApprovalYearClose:
		var p mizan.YearClosePayload
		if json.Unmarshal(req.Payload, &p) != nil || p.FiscalYearID == 0 {
			return apperr.InvalidField("payload", "fiscal_year_id is required")
		}
		fy = p.FiscalYearID
	case mizan.ApprovalAppropriation:
		var p mizan.AppropriationPayload
		if json.Unmarshal(req.Payload, &p) != nil || p.FiscalYearID == 0 {
			return apperr.InvalidField("payload", "fiscal_year_id is required")
		}
		fy = p.FiscalYearID
	default:
		return apperr.InvalidField("kind", "must be year_close or appropriation")
	}
	x, err := a.m.Approvals.Request(r.Context(), req.Kind, "fiscal_year:"+itoa(int(fy)), req.Payload, key, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, approvalDTO(x))
}

func (a *API) listApprovals(w http.ResponseWriter, r *http.Request) error {
	xs, err := a.m.Approvals.List(r.Context(), approval.Status(r.URL.Query().Get("status")))
	if err != nil {
		return err
	}
	out := make([]map[string]any, len(xs))
	for i, x := range xs {
		out[i] = approvalDTO(x)
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getApproval(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.Approvals.Get(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, approvalDTO(x))
}

// approve runs the action as the caller, who must differ from the preparer.
func (a *API) approve(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.m.Approvals.Approve(r.Context(), id, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, approvalDTO(x))
}

func (a *API) rejectApproval(w http.ResponseWriter, r *http.Request) error {
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
	x, err := a.m.Approvals.Reject(r.Context(), id, actorOf(r.Context()), req.Reason)
	if err != nil {
		return err
	}
	return ok(w, approvalDTO(x))
}
