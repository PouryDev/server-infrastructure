package mizan

import (
	"context"
	"encoding/json"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/appropriation"
	"github.com/PouryDev/server-infrastructure/mizan/ledger"
)

// Kinds of action that can require two persons.
const (
	ApprovalYearClose     = "year_close"
	ApprovalAppropriation = "appropriation"
)

// YearClosePayload is the request body of a year-end closing approval.
type YearClosePayload struct {
	FiscalYearID    int64  `json:"fiscal_year_id"`
	RetainedAccount string `json:"retained_account,omitempty"`
}

// AppropriationPayload is the request body of a profit-appropriation approval.
type AppropriationPayload struct {
	FiscalYearID    int64  `json:"fiscal_year_id"`
	PublicCompany   bool   `json:"public_company"`
	DecidedOn       string `json:"decided_on,omitempty"`
	Dividend        int64  `json:"dividend"`
	OptionalReserve int64  `json:"optional_reserve"`
}

// ApprovalRequired reports whether the direct endpoints of a kind are switched off in favour of the request/approve workflow (Config.RequireApproval).
func (m *Mizan) ApprovalRequired() bool { return m.cfg.RequireApproval }

// RequireNoApproval is the guard of the direct endpoints: with Config.RequireApproval the action must go through POST /approvals.
func (m *Mizan) RequireNoApproval() error {
	if m.cfg.RequireApproval {
		return apperr.New(apperr.KindConflict, "approval_required", "this action needs a second person: file a request with POST /approvals and let another user approve it")
	}
	return nil
}

// registerApprovals wires the executors; called once from New.
func (m *Mizan) registerApprovals() {
	m.Approvals.Register(ApprovalYearClose, func(ctx context.Context, raw json.RawMessage, _, approver string) (any, error) {
		var p YearClosePayload
		if err := json.Unmarshal(raw, &p); err != nil || p.FiscalYearID == 0 {
			return nil, apperr.InvalidField("payload", "fiscal_year_id is required")
		}
		res, err := m.Ledger.CloseYear(ctx, p.FiscalYearID, ledger.CloseRequest{RetainedAccount: p.RetainedAccount, Actor: approver})
		if err != nil {
			return nil, err
		}
		return map[string]any{"fiscal_year": res.FiscalYear, "net_profit": res.NetProfit, "already_closed": res.AlreadyClosed}, nil
	})
	m.Approvals.Register(ApprovalAppropriation, func(ctx context.Context, raw json.RawMessage, key, approver string) (any, error) {
		var p AppropriationPayload
		if err := json.Unmarshal(raw, &p); err != nil || p.FiscalYearID == 0 {
			return nil, apperr.InvalidField("payload", "fiscal_year_id is required")
		}
		decided, err := parseDay(p.DecidedOn)
		if err != nil {
			return nil, err
		}
		rec, err := m.DeclareAppropriation(ctx, p.FiscalYearID, p.PublicCompany, decided, appropriation.Decision{Dividend: p.Dividend, OptionalReserve: p.OptionalReserve}, key, approver)
		if err != nil {
			return nil, err
		}
		return map[string]any{"appropriation_id": rec.ID, "legal_reserve": rec.LegalReserve, "dividend": rec.Dividend}, nil
	})
}

func parseDay(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, apperr.InvalidField("decided_on", "must be YYYY-MM-DD")
	}
	return t, nil
}
