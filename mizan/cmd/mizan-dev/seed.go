package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/gokit/auth"
)

// seed fills an empty book with a little sample data, through the same HTTP API the UI uses (in process, so every
// rule of the module applies). It runs only with SEED=true and does nothing when journals already exist. A step that
// fails is logged and skipped: the data is a convenience for clicking through the UI, never a requirement.
func seed(ctx context.Context, h http.Handler, jwt *auth.JWT) error {
	pair, err := jwt.IssuePair("seed", []string{"finance", "admin"}, nil)
	if err != nil {
		return err
	}
	s := &seeder{ctx: ctx, h: h, token: pair.AccessToken}

	var existing struct {
		Items []any `json:"items"`
	}
	if code, body := s.do("GET", "/v1/accounting/journals", nil, ""); code != 200 || json.Unmarshal(body, &existing) != nil {
		return fmt.Errorf("cannot list journals: %d %s", code, body)
	}
	if len(existing.Items) > 0 {
		slog.Info("seed skipped: the books already have documents")
		return nil
	}

	today := time.Now().UTC()
	jal := func(daysAgo int) string { return gregorian(today.AddDate(0, 0, -daysAgo)) }
	post := func(daysAgo int, desc string, lines ...[3]any) {
		ls := make([]map[string]any, len(lines))
		for i, l := range lines {
			ls[i] = map[string]any{"account": l[0], "side": l[1], "amount": map[string]any{"amount": l[2], "currency": "IRR"}}
		}
		s.must("POST", "/v1/accounting/journals", map[string]any{"date": jal(daysAgo), "description": desc, "lines": ls}, "seed-j-"+desc)
	}
	// opening capital, sales, an expense, a payment received
	post(40, "افتتاح: سرمایهٔ اولیه", [3]any{"bank.main", "debit", 500_000_000}, [3]any{"equity.capital", "credit", 500_000_000})
	post(30, "فروش نقدی مهر", [3]any{"bank.main", "debit", 38_000_000}, [3]any{"revenue.sales", "credit", 38_000_000})
	post(21, "فروش اعتباری به مشتری", [3]any{"receivable.customers", "debit", 12_000_000}, [3]any{"revenue.sales", "credit", 12_000_000})
	post(14, "هزینهٔ کارمزد بانکی", [3]any{"expense.bank_fees", "debit", 210_000}, [3]any{"bank.main", "credit", 210_000})
	post(7, "دریافت وجه از مشتری", [3]any{"bank.main", "debit", 5_800_000}, [3]any{"receivable.customers", "credit", 5_800_000})
	post(2, "هزینهٔ اجارهٔ دفتر", [3]any{"expense.adjustments", "debit", 12_000_000}, [3]any{"bank.main", "credit", 12_000_000})

	// invoices (one issued, one draft)
	s.must("POST", "/v1/invoices", map[string]any{"customer_ref": "علی رضایی", "buyer": map[string]any{"name": "علی رضایی"}, "issue": true,
		"items": []map[string]any{{"description": "اشتراک سالانه", "quantity": 1, "unit_price": 3_818_182}}}, "seed-inv-1")
	s.must("POST", "/v1/invoices", map[string]any{"customer_ref": "فروشگاه نیلوفر", "buyer": map[string]any{"name": "فروشگاه نیلوفر"}, "issue": false,
		"items": []map[string]any{{"description": "خدمات راه\u200cاندازی", "quantity": 2, "unit_price": 500_000}}}, "seed-inv-2")

	// wallet, coupon, plan
	if code, body := s.do("POST", "/v1/wallets", map[string]any{"owner_ref": "user-4821", "currency": "IRR"}, ""); code == 201 {
		var w struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &w)
		s.must("POST", "/v1/wallets/"+w.ID+"/credit", map[string]any{"amount": map[string]any{"amount": 3_500_000, "currency": "IRR"}, "description": "شارژ اولیه"}, "seed-wallet-credit")
	}
	s.must("POST", "/v1/coupons", map[string]any{"code": "WELCOME10", "name": "خوش\u200cآمدگویی", "kind": "percent", "percent_bp": 1000, "max_redemptions": 500}, "")
	s.must("POST", "/v1/subscription-plans", map[string]any{"code": "pro-monthly", "name": "حرفه\u200cای – ماهانه", "price": map[string]any{"amount": 4_200_000, "currency": "IRR"}, "unit": "month", "count": 1}, "")

	// treasury: a second bank account and a received cheque
	s.must("POST", "/v1/bank-accounts", map[string]any{"name": "بانک پاسارگاد", "bank_name": "پاسارگاد", "ledger_code": "bank.pasargad"}, "")
	s.must("POST", "/v1/cheques", map[string]any{"direction": "receivable", "number": "882141", "amount": map[string]any{"amount": 8_200_000, "currency": "IRR"},
		"due_date": gregorian(today.AddDate(0, 0, 20)), "counterparty_ref": "شرکت آرمان تجارت"}, "seed-cheque-1")

	// payroll: an employee with a contract
	if code, body := s.do("POST", "/v1/payroll/employees", map[string]any{"code": "E-001", "name": "احمد توکلی", "national_id": "0012345678"}, ""); code == 201 {
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &e)
		s.must("POST", "/v1/payroll/employees/"+e.ID+"/contracts", map[string]any{"base_salary": map[string]any{"amount": 28_000_000, "currency": "IRR"}, "insured": true, "start": gregorian(today.AddDate(0, -6, 0)),
			"allowances": []map[string]any{{"code": "housing", "name": "حق مسکن", "amount": map[string]any{"amount": 3_000_000, "currency": "IRR"}, "taxable": true, "insurable": true}}}, "")
	}

	// inventory
	s.must("POST", "/v1/inventory/warehouses", map[string]any{"code": "W1", "name": "انبار مرکزی"}, "")
	s.must("POST", "/v1/inventory/items", map[string]any{"code": "P-101", "name": "تونر چاپگر", "unit": "عدد", "method": "weighted_average"}, "")
	slog.Info("seed finished", "failed_steps", s.failed)
	return nil
}

type seeder struct {
	ctx    context.Context
	h      http.Handler
	token  string
	failed int
}

func (s *seeder) do(method, path string, body any, key string) (int, []byte) {
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(s.ctx, method, path, rd)
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	if key == "" && method == http.MethodPost {
		key = fmt.Sprintf("seed-%s-%d", strings.ReplaceAll(path, "/", "-"), time.Now().UnixNano())
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (s *seeder) must(method, path string, body any, key string) {
	if code, resp := s.do(method, path, body, key); code >= 300 {
		s.failed++
		slog.Warn("seed step failed", "path", path, "status", code, "body", strings.TrimSpace(string(resp)))
	}
}

// gregorian formats a day the way the API takes dates (YYYY-MM-DD).
func gregorian(t time.Time) string { return t.Format("2006-01-02") }
