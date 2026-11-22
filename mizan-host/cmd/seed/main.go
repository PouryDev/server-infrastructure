// Command seed fills an empty book with sample data so the API and the UI can be tried out: an extra fiscal year,
// about twenty journals over the months of the current year, invoices, a wallet, a coupon, a plan, bank accounts, cheques,
// an employee and inventory. It talks to the RUNNING host over HTTP as a real user (the same path the UI takes), so every
// rule of mizan applies; a step that fails is logged and skipped. It does nothing when journals already exist.
//
//	SEED_PASSWORD=... seed            (SEED_USERNAME defaults to admin, API_URL to http://api:8080)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type client struct {
	base, token string
	failed      int
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(ctx context.Context) error {
	pw := os.Getenv("SEED_PASSWORD")
	if pw == "" {
		return errors.New("SEED_PASSWORD is required (the password of a finance or admin user)")
	}
	c := &client{base: strings.TrimRight(env("API_URL", "http://api:8080"), "/")}
	code, body := c.do(ctx, "POST", "/auth/token", map[string]string{"username": env("SEED_USERNAME", "admin"), "password": pw}, "")
	var tk struct {
		Tokens struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	if code != 200 || json.Unmarshal(body, &tk) != nil || tk.Tokens.Access == "" {
		return fmt.Errorf("sign-in failed: %d %s", code, strings.TrimSpace(string(body)))
	}
	c.token = tk.Tokens.Access

	var existing struct {
		Items []any `json:"items"`
	}
	if code, body := c.do(ctx, "GET", "/accounting/journals", nil, ""); code != 200 || json.Unmarshal(body, &existing) != nil {
		return fmt.Errorf("cannot list journals: %d %s", code, body)
	}
	if len(existing.Items) > 0 {
		fmt.Println("seed skipped: the books already have documents")
		return nil
	}

	today := time.Now().UTC()
	day := func(ago int) string { return today.AddDate(0, 0, -ago).Format("2006-01-02") }
	irr := func(n int) map[string]any { return map[string]any{"amount": n, "currency": "IRR"} }

	// the previous fiscal year (Jalali 1404 while the current one is 1405), so the year selector has something to select
	c.must(ctx, "POST", "/accounting/fiscal-years", map[string]any{"jalali_year": currentJalaliYear(today) - 1}, "seed-fy-prev")

	type line = [3]any
	post := func(ago int, desc string, lines ...line) {
		ls := make([]map[string]any, len(lines))
		for i, l := range lines {
			ls[i] = map[string]any{"account": l[0], "side": l[1], "amount": irr(l[2].(int))}
		}
		c.must(ctx, "POST", "/accounting/journals", map[string]any{"date": day(ago), "description": desc, "lines": ls}, fmt.Sprintf("seed-j-%d-%s", ago, desc))
	}
	// previous year
	post(330, "افتتاح سال قبل: سرمایه", line{"bank.main", "debit", 300_000_000}, line{"equity.capital", "credit", 300_000_000})
	post(300, "فروش نقدی دی", line{"bank.main", "debit", 41_000_000}, line{"revenue.sales", "credit", 41_000_000})
	post(280, "هزینهٔ اجارهٔ بهمن", line{"expense.adjustments", "debit", 10_000_000}, line{"bank.main", "credit", 10_000_000})
	// current year
	post(225, "افتتاح: سرمایهٔ اولیه", line{"bank.main", "debit", 500_000_000}, line{"equity.capital", "credit", 500_000_000})
	post(205, "فروش نقدی فروردین", line{"bank.main", "debit", 28_000_000}, line{"revenue.sales", "credit", 28_000_000})
	post(190, "فروش اعتباری اردیبهشت", line{"receivable.customers", "debit", 15_500_000}, line{"revenue.sales", "credit", 15_500_000})
	post(170, "هزینهٔ اجارهٔ دفتر خرداد", line{"expense.adjustments", "debit", 12_000_000}, line{"bank.main", "credit", 12_000_000})
	post(150, "دریافت وجه از مشتری", line{"bank.main", "debit", 9_000_000}, line{"receivable.customers", "credit", 9_000_000})
	post(130, "فروش نقدی تیر", line{"bank.main", "debit", 33_500_000}, line{"revenue.sales", "credit", 33_500_000})
	post(110, "کارمزد بانکی", line{"expense.bank_fees", "debit", 185_000}, line{"bank.main", "credit", 185_000})
	post(95, "هزینهٔ اجارهٔ دفتر مرداد", line{"expense.adjustments", "debit", 12_000_000}, line{"bank.main", "credit", 12_000_000})
	post(80, "فروش اعتباری شهریور", line{"receivable.customers", "debit", 22_000_000}, line{"revenue.sales", "credit", 22_000_000})
	post(65, "فروش نقدی مهر", line{"bank.main", "debit", 38_000_000}, line{"revenue.sales", "credit", 38_000_000})
	post(50, "دریافت وجه از مشتری", line{"bank.main", "debit", 14_000_000}, line{"receivable.customers", "credit", 14_000_000})
	post(40, "کارمزد بانکی", line{"expense.bank_fees", "debit", 210_000}, line{"bank.main", "credit", 210_000})
	post(30, "هزینهٔ اجارهٔ دفتر آبان", line{"expense.adjustments", "debit", 12_000_000}, line{"bank.main", "credit", 12_000_000})
	post(21, "فروش اعتباری به مشتری", line{"receivable.customers", "debit", 12_000_000}, line{"revenue.sales", "credit", 12_000_000})
	post(7, "دریافت وجه از مشتری", line{"bank.main", "debit", 5_800_000}, line{"receivable.customers", "credit", 5_800_000})
	post(2, "فروش نقدی", line{"bank.main", "debit", 6_400_000}, line{"revenue.sales", "credit", 6_400_000})

	// invoices (two issued, one draft)
	inv := func(who string, issue bool, key string, items ...map[string]any) {
		c.must(ctx, "POST", "/v1/invoices", map[string]any{"customer_ref": who, "buyer": map[string]any{"name": who}, "issue": issue, "items": items}, key)
	}
	inv("علی رضایی", true, "seed-inv-1", map[string]any{"description": "اشتراک سالانه", "quantity": 1, "unit_price": 3_818_182})
	inv("شرکت آرمان تجارت", true, "seed-inv-2", map[string]any{"description": "خدمات پشتیبانی", "quantity": 3, "unit_price": 1_200_000})
	inv("فروشگاه نیلوفر", false, "seed-inv-3", map[string]any{"description": "خدمات راه\u200cاندازی", "quantity": 2, "unit_price": 500_000})

	// wallet, coupon, plan
	if code, body := c.do(ctx, "POST", "/v1/wallets", map[string]any{"owner_ref": "user-4821", "currency": "IRR"}, "seed-wallet"); code == 201 {
		var w struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &w)
		c.must(ctx, "POST", "/v1/wallets/"+w.ID+"/credit", map[string]any{"amount": irr(3_500_000), "description": "شارژ اولیه"}, "seed-wallet-credit")
	}
	c.must(ctx, "POST", "/v1/coupons", map[string]any{"code": "WELCOME10", "name": "خوش\u200cآمدگویی", "kind": "percent", "percent_bp": 1000, "max_redemptions": 500}, "seed-coupon")
	c.must(ctx, "POST", "/v1/subscription-plans", map[string]any{"code": "pro-monthly", "name": "حرفه\u200cای – ماهانه", "price": irr(4_200_000), "unit": "month", "count": 1}, "seed-plan")

	// treasury
	bankID := ""
	if code, body := c.do(ctx, "POST", "/v1/bank-accounts", map[string]any{"name": "بانک پاسارگاد", "bank_name": "پاسارگاد", "ledger_code": "bank.pasargad"}, "seed-bank-2"); code < 300 {
		var b struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &b)
		bankID = b.ID
	}
	c.must(ctx, "POST", "/v1/cheques", map[string]any{"direction": "receivable", "number": "882141", "amount": irr(8_200_000), "due_date": day(-20), "counterparty_ref": "شرکت آرمان تجارت"}, "seed-cheque-1")
	c.must(ctx, "POST", "/v1/cheques", map[string]any{"direction": "payable", "bank_account_id": bankID, "number": "551907", "amount": irr(4_500_000), "due_date": day(-35), "counterparty_ref": "تأمین\u200cکنندهٔ تجهیزات"}, "seed-cheque-2")

	// payroll
	if code, body := c.do(ctx, "POST", "/v1/payroll/employees", map[string]any{"code": "E-001", "name": "احمد توکلی", "national_id": "0012345678"}, "seed-emp-1"); code == 201 {
		var e struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &e)
		c.must(ctx, "POST", "/v1/payroll/employees/"+e.ID+"/contracts", map[string]any{"base_salary": irr(28_000_000), "insured": true, "start": today.AddDate(0, -6, 0).Format("2006-01-02"),
			"allowances": []map[string]any{{"code": "housing", "name": "حق مسکن", "amount": irr(3_000_000), "taxable": true, "insurable": true}}}, "seed-contract-1")
	}

	// inventory
	c.must(ctx, "POST", "/v1/inventory/warehouses", map[string]any{"code": "W1", "name": "انبار مرکزی"}, "seed-wh")
	c.must(ctx, "POST", "/v1/inventory/items", map[string]any{"code": "P-101", "name": "تونر چاپگر", "unit": "عدد", "method": "weighted_average"}, "seed-item")

	fmt.Printf("seed finished (%d steps failed; see the warnings above)\n", c.failed)
	return nil
}

// currentJalaliYear is good enough for picking the previous year to open: the Jalali year starts on 21 March.
func currentJalaliYear(t time.Time) int {
	if t.Month() > time.March || (t.Month() == time.March && t.Day() >= 21) {
		return t.Year() - 621
	}
	return t.Year() - 622
}

func (c *client) do(ctx context.Context, method, path string, body any, key string) (int, []byte) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return 0, []byte(err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func (c *client) must(ctx context.Context, method, path string, body any, key string) {
	if code, resp := c.do(ctx, method, path, body, key); code >= 300 || code == 0 {
		c.failed++
		slog.Warn("seed step failed", "path", path, "status", code, "body", strings.TrimSpace(string(resp)))
	}
}
