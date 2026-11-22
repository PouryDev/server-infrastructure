package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/gokit/httpx"
	"github.com/PouryDev/server-infrastructure/mizan/money"
	"github.com/PouryDev/server-infrastructure/mizan/payroll"
)

func (a *API) mountPayroll(r *httpx.Router, p string) {
	r.Route("POST "+p+"/payroll/employees", a.addEmployee)
	r.Route("GET "+p+"/payroll/employees", a.listEmployees)
	r.Route("POST "+p+"/payroll/employees/{id}/contracts", a.addContract)
	r.Route("GET "+p+"/payroll/employees/{id}/contracts", a.listContracts)
	r.Route("POST "+p+"/payroll/contracts/{id}/end", a.endContract)

	r.Route("POST "+p+"/payroll/rule-versions", a.addRule)
	r.Route("GET "+p+"/payroll/rule-versions", a.listRules)
	r.Route("GET "+p+"/payroll/rule-versions/at", a.ruleAt)
	r.Route("DELETE "+p+"/payroll/rule-versions/{id}", a.deleteRule)

	r.Route("POST "+p+"/payroll/payslips/preview", a.previewPayslip)
	r.Route("POST "+p+"/payroll/payslips", a.issuePayslip)
	r.Route("GET "+p+"/payroll/payslips", a.listPayslips)
	r.Route("GET "+p+"/payroll/payslips/{id}", a.getPayslip)
	r.Route("POST "+p+"/payroll/payslips/{id}/void", a.voidPayslip)
	r.Route("POST "+p+"/payroll/runs", a.payRun)

	r.Route("POST "+p+"/payroll/employees/{id}/loans", a.grantLoan)
	r.Route("GET "+p+"/payroll/employees/{id}/loans", a.listLoans)
	r.Route("POST "+p+"/payroll/loans/{id}/write-off", a.writeOffLoan)
	r.Route("POST "+p+"/payroll/holidays", a.addHoliday)
	r.Route("GET "+p+"/payroll/holidays", a.listHolidays)
	r.Route("DELETE "+p+"/payroll/holidays", a.deleteHoliday)
	r.Route("POST "+p+"/payroll/remittances", a.remit)
	r.Route("GET "+p+"/payroll/remittances", a.listRemittances)
}

// ---- employees and contracts ----

type employeeDTO struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	NationalID  string    `json:"national_id,omitempty"`
	InsuranceNo string    `json:"insurance_no,omitempty"`
	Active      bool      `json:"active"`
}

func toEmployee(e payroll.Employee) employeeDTO {
	return employeeDTO{e.ID, e.Code, e.Name, e.NationalID, e.InsuranceNo, e.Active}
}

func (a *API) addEmployee(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		NationalID  string `json:"national_id"`
		InsuranceNo string `json:"insurance_no"`
	}](r)
	if err != nil {
		return err
	}
	e, err := a.m.Payroll.AddEmployee(r.Context(), payroll.EmployeeInput{Code: req.Code, Name: req.Name, NationalID: req.NationalID, InsuranceNo: req.InsuranceNo, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toEmployee(e))
}

func (a *API) listEmployees(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payroll.Employees(r.Context())
	if err != nil {
		return err
	}
	out := make([]employeeDTO, len(list))
	for i, e := range list {
		out[i] = toEmployee(e)
	}
	return ok(w, out)
}

type contractDTO struct {
	ID         uuid.UUID           `json:"id"`
	EmployeeID uuid.UUID           `json:"employee_id"`
	BaseSalary money.Money         `json:"base_salary"`
	Allowances []payroll.Allowance `json:"allowances"`
	Insured    bool                `json:"insured"`
	Start      string              `json:"start"`
	StartJ     string              `json:"start_jalali"`
	End        string              `json:"end,omitempty"`
	EndJ       string              `json:"end_jalali,omitempty"`
}

func toContract(c payroll.Contract) contractDTO {
	d := contractDTO{ID: c.ID, EmployeeID: c.EmployeeID, BaseSalary: c.BaseSalary, Allowances: c.Allowances, Insured: c.Insured,
		Start: c.Start.Format("2006-01-02"), StartJ: jdate(c.Start)}
	if d.Allowances == nil {
		d.Allowances = []payroll.Allowance{}
	}
	if c.End != nil {
		d.End, d.EndJ = c.End.Format("2006-01-02"), jdate(*c.End)
	}
	return d
}

func (a *API) addContract(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		BaseSalary money.Money         `json:"base_salary"`
		Allowances []payroll.Allowance `json:"allowances"`
		Insured    bool                `json:"insured"`
		Start      string              `json:"start"`
		End        string              `json:"end"`
	}](r)
	if err != nil {
		return err
	}
	start, err := parseDate(req.Start)
	if err != nil {
		return apperr.InvalidField("start", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	in := payroll.ContractInput{EmployeeID: id, BaseSalary: req.BaseSalary, Allowances: req.Allowances, Insured: req.Insured, Start: start, Actor: actorOf(r.Context())}
	if strings.TrimSpace(req.End) != "" {
		end, err := parseDate(req.End)
		if err != nil {
			return apperr.InvalidField("end", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
		}
		in.End = &end
	}
	c, err := a.m.Payroll.AddContract(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, toContract(c))
}

func (a *API) listContracts(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := a.m.Payroll.Contracts(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]contractDTO, len(list))
	for i, c := range list {
		out[i] = toContract(c)
	}
	return ok(w, out)
}

func (a *API) endContract(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		End string `json:"end"`
	}](r)
	if err != nil {
		return err
	}
	end, err := parseDate(req.End)
	if err != nil {
		return apperr.InvalidField("end", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	c, err := a.m.Payroll.EndContract(r.Context(), id, end, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toContract(c))
}

// ---- rule versions ----

type ruleDTO struct {
	ID                      uuid.UUID         `json:"id"`
	Name                    string            `json:"name"`
	EffectiveFrom           string            `json:"effective_from"`
	EffectiveFromJalali     string            `json:"effective_from_jalali"`
	Currency                money.Currency    `json:"currency"`
	EmployeeBP              int               `json:"employee_bp"`
	EmployerBP              int               `json:"employer_bp"`
	UnemploymentBP          int               `json:"unemployment_bp"`
	CeilingDaily            int64             `json:"ceiling_daily"`
	FloorDaily              int64             `json:"floor_daily"`
	DeductEmployeeInsurance bool              `json:"deduct_employee_insurance"`
	Brackets                []payroll.Bracket `json:"brackets"`
	Terms                   payroll.Terms     `json:"terms"`
	Source                  string            `json:"source,omitempty"`
}

func toRule(v payroll.RuleVersion) ruleDTO {
	return ruleDTO{v.ID, v.Name, v.EffectiveFrom.Format("2006-01-02"), jdate(v.EffectiveFrom), v.Currency, v.EmployeeBP, v.EmployerBP, v.UnemploymentBP,
		v.CeilingDaily, v.FloorDaily, v.DeductEmployeeInsurance, v.Brackets, v.Terms, v.Source}
}

// addRule stores a new version of the social-security and income-tax rules. effective_from is Gregorian or Jalali.
func (a *API) addRule(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[struct {
		Name                    string            `json:"name"`
		EffectiveFrom           string            `json:"effective_from"`
		Currency                money.Currency    `json:"currency"`
		EmployeeBP              int               `json:"employee_bp"`
		EmployerBP              int               `json:"employer_bp"`
		UnemploymentBP          int               `json:"unemployment_bp"`
		CeilingDaily            int64             `json:"ceiling_daily"`
		FloorDaily              int64             `json:"floor_daily"`
		DeductEmployeeInsurance bool              `json:"deduct_employee_insurance"`
		Brackets                []payroll.Bracket `json:"brackets"`
		Terms                   payroll.Terms     `json:"terms"`
		Source                  string            `json:"source"`
	}](r)
	if err != nil {
		return err
	}
	from, err := parseDate(req.EffectiveFrom)
	if err != nil {
		return apperr.InvalidField("effective_from", "dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
	}
	if req.Currency == "" {
		req.Currency = money.IRR
	}
	v, err := a.m.Payroll.AddRule(r.Context(), payroll.RuleInput{Name: req.Name, EffectiveFrom: from, Currency: req.Currency, EmployeeBP: req.EmployeeBP,
		EmployerBP: req.EmployerBP, UnemploymentBP: req.UnemploymentBP, CeilingDaily: req.CeilingDaily, FloorDaily: req.FloorDaily,
		DeductEmployeeInsurance: req.DeductEmployeeInsurance, Brackets: req.Brackets, Terms: req.Terms, Source: req.Source, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toRule(v))
}

func (a *API) listRules(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payroll.Rules(r.Context())
	if err != nil {
		return err
	}
	out := make([]ruleDTO, len(list))
	for i, v := range list {
		out[i] = toRule(v)
	}
	return ok(w, out)
}

func (a *API) ruleAt(w http.ResponseWriter, r *http.Request) error {
	day, err := optDate(r, "date")
	if err != nil {
		return err
	}
	d := time.Now()
	if day != nil {
		d = *day
	}
	v, err := a.m.Payroll.RuleAt(r.Context(), d)
	if err != nil {
		return err
	}
	return ok(w, toRule(v))
}

func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.m.Payroll.DeleteRule(r.Context(), id, actorOf(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- payslips ----

type payslipDTO struct {
	ID            uuid.UUID             `json:"id"`
	EmployeeID    uuid.UUID             `json:"employee_id"`
	Year          int                   `json:"year"`
	Month         int                   `json:"month"`
	Status        payroll.PayslipStatus `json:"status"`
	RuleVersionID uuid.UUID             `json:"rule_version_id"`
	RunID         *uuid.UUID            `json:"run_id,omitempty"`
	VoidReason    string                `json:"void_reason,omitempty"`
	breakdownDTO
}

type breakdownDTO struct {
	Days          int            `json:"days"`
	MonthDays     int            `json:"month_days"`
	Lines         []payroll.Line `json:"lines"`
	Gross         money.Money    `json:"gross"`
	Insurable     money.Money    `json:"insurable"`
	EmployeeIns   money.Money    `json:"employee_insurance"`
	EmployerIns   money.Money    `json:"employer_insurance"`
	Unemployment  money.Money    `json:"unemployment"`
	TaxableIncome money.Money    `json:"taxable_income"`
	Tax           money.Money    `json:"tax"`
	Deductions    []deductionDTO `json:"deductions"`
	Deducted      money.Money    `json:"deduction_total"`
	Net           money.Money    `json:"net"`
}

type deductionDTO struct {
	LoanID uuid.UUID   `json:"loan_id"`
	Amount money.Money `json:"amount"`
}

func toBreakdown(b payroll.Breakdown) breakdownDTO {
	ds := make([]deductionDTO, len(b.Deductions))
	for i, d := range b.Deductions {
		ds[i] = deductionDTO{d.LoanID, d.Amount}
	}
	return breakdownDTO{b.Days, b.MonthDays, b.Lines, b.Gross, b.Insurable, b.EmployeeIns, b.EmployerIns, b.Unemployment, b.TaxableIncome, b.Tax, ds, b.DeductionTotal, b.Net}
}

func toPayslip(p payroll.Payslip) payslipDTO {
	return payslipDTO{ID: p.ID, EmployeeID: p.EmployeeID, Year: p.Year, Month: p.Month, Status: p.Status, RuleVersionID: p.RuleID, RunID: p.RunID,
		VoidReason: p.VoidReason, breakdownDTO: toBreakdown(p.Breakdown)}
}

type payslipRequest struct {
	EmployeeID uuid.UUID           `json:"employee_id"`
	Year       int                 `json:"year"`  // Jalali
	Month      int                 `json:"month"` // Jalali, 1-12
	Days       int                 `json:"days"`
	Extras     []payroll.Allowance `json:"extras"`
	// Hours of overtime, holiday and night work (minutes), unused leave days paid out, and the end-of-service severance.
	Hours     payroll.Hours `json:"hours"`
	LeaveDays int           `json:"leave_days"`
	Severance bool          `json:"severance"`
	// BonusDays pays the annual bonus (عیدی) for this many days of service in the year (365 = a full year).
	BonusDays int `json:"bonus_days"`
}

func (q payslipRequest) input(actor string) payroll.PayslipInput {
	return payroll.PayslipInput{EmployeeID: q.EmployeeID, Year: q.Year, Month: q.Month, Days: q.Days, Extras: q.Extras, Hours: q.Hours,
		LeaveDays: q.LeaveDays, BonusDays: q.BonusDays, Severance: q.Severance, Actor: actor}
}

func (a *API) previewPayslip(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[payslipRequest](r)
	if err != nil {
		return err
	}
	b, err := a.m.Payroll.Preview(r.Context(), req.input(""))
	if err != nil {
		return err
	}
	return ok(w, toBreakdown(b))
}

// issuePayslip books the pay of an employee for a Jalali month; repeating it returns the same payslip.
func (a *API) issuePayslip(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[payslipRequest](r)
	if err != nil {
		return err
	}
	p, err := a.m.Payroll.IssuePayslip(r.Context(), req.input(actorOf(r.Context())))
	if err != nil {
		return err
	}
	return created(w, toPayslip(p))
}

func (a *API) listPayslips(w http.ResponseWriter, r *http.Request) error {
	params, err := httpx.ListParams(r)
	if err != nil {
		return err
	}
	page, err := a.m.Payroll.ListPayslips(r.Context(), params)
	if err != nil {
		return err
	}
	return ok(w, mapPage(page, toPayslip))
}

func (a *API) getPayslip(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	p, err := a.m.Payroll.Payslip(r.Context(), id)
	if err != nil {
		return err
	}
	return ok(w, toPayslip(p))
}

func (a *API) voidPayslip(w http.ResponseWriter, r *http.Request) error {
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
	p, err := a.m.Payroll.VoidPayslip(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toPayslip(p))
}

// payRun pays issued payslips of a Jalali month from a bank account (default: the main one).
func (a *API) payRun(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Year          int         `json:"year"`
		Month         int         `json:"month"`
		PayslipIDs    []uuid.UUID `json:"payslip_ids"`
		BankAccountID *uuid.UUID  `json:"bank_account_id"`
		Date          string      `json:"date"`
	}](r)
	if err != nil {
		return err
	}
	d, err := optDateBody(req.Date, "date")
	if err != nil {
		return err
	}
	in := payroll.RunInput{IdemKey: "payroll_run:" + key, Year: req.Year, Month: req.Month, PayslipIDs: req.PayslipIDs, Date: d, Actor: actorOf(r.Context())}
	if req.BankAccountID != nil {
		acc, err := a.m.Treasury.BankAccount(r.Context(), *req.BankAccountID)
		if err != nil {
			return err
		}
		in.PayFrom = acc.LedgerCode
	}
	run, err := a.m.Payroll.PayRun(r.Context(), in)
	if err != nil {
		return err
	}
	return created(w, map[string]any{"id": run.ID, "year": run.Year, "month": run.Month, "count": run.Count, "total": run.Total, "pay_from": run.PayFrom})
}

// ---- loans ----

type loanDTO struct {
	ID          uuid.UUID          `json:"id"`
	EmployeeID  uuid.UUID          `json:"employee_id"`
	Principal   money.Money        `json:"principal"`
	Installment money.Money        `json:"installment"`
	Remaining   money.Money        `json:"remaining"`
	StartYear   int                `json:"start_year"`
	StartMonth  int                `json:"start_month"`
	Status      payroll.LoanStatus `json:"status"`
	Note        string             `json:"note,omitempty"`
	WrittenOff  money.Money        `json:"written_off"`
}

func toLoan(l payroll.Loan) loanDTO {
	return loanDTO{l.ID, l.EmployeeID, l.Principal, l.Installment, l.Remaining, l.StartYear, l.StartMonth, l.Status, l.Note, l.WrittenOff}
}

// writeOffLoan forgives what an employee still owes on a loan.
func (a *API) writeOffLoan(w http.ResponseWriter, r *http.Request) error {
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
	l, err := a.m.Payroll.WriteOffLoan(r.Context(), id, req.Reason, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return ok(w, toLoan(l))
}

type holidayDTO struct {
	Year     int    `json:"jalali_year"`
	Month    int    `json:"jalali_month"`
	Day      int    `json:"jalali_day"`
	Name     string `json:"name"`
	Kind     string `json:"kind,omitempty"`
	Verified bool   `json:"verified"`
	Source   string `json:"source"`
}

func (a *API) addHoliday(w http.ResponseWriter, r *http.Request) error {
	req, err := httpx.Decode[holidayDTO](r)
	if err != nil {
		return err
	}
	h, err := a.m.Payroll.AddHoliday(r.Context(), payroll.Holiday{Year: req.Year, Month: req.Month, Day: req.Day, Name: req.Name, Kind: req.Kind, Verified: req.Verified, Source: req.Source}, actorOf(r.Context()))
	if err != nil {
		return err
	}
	return created(w, holidayDTO{h.Year, h.Month, h.Day, h.Name, h.Kind, h.Verified, h.Source})
}

func (a *API) listHolidays(w http.ResponseWriter, r *http.Request) error {
	year, err := strconv.Atoi(r.URL.Query().Get("jalali_year"))
	if err != nil || year < 1300 || year > 1499 {
		return apperr.InvalidField("jalali_year", "a Jalali year is required")
	}
	hs, err := a.m.Payroll.Holidays(r.Context(), year)
	if err != nil {
		return err
	}
	out := make([]holidayDTO, len(hs))
	for i, h := range hs {
		out[i] = holidayDTO{h.Year, h.Month, h.Day, h.Name, h.Kind, h.Verified, h.Source}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) deleteHoliday(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	y, e1 := strconv.Atoi(q.Get("jalali_year"))
	m, e2 := strconv.Atoi(q.Get("jalali_month"))
	d, e3 := strconv.Atoi(q.Get("jalali_day"))
	if e1 != nil || e2 != nil || e3 != nil {
		return apperr.Invalid("jalali_year, jalali_month and jalali_day are required")
	}
	if err := a.m.Payroll.DeleteHoliday(r.Context(), y, m, d, actorOf(r.Context())); err != nil {
		return err
	}
	return ok(w, map[string]any{"deleted": true})
}

// payFromOf resolves an optional bank account to the ledger account it pays from.
func (a *API) payFromOf(r *http.Request, id *uuid.UUID) (string, error) {
	if id == nil {
		return "", nil
	}
	acc, err := a.m.Treasury.BankAccount(r.Context(), *id)
	if err != nil {
		return "", err
	}
	return acc.LedgerCode, nil
}

// grantLoan lends money to an employee; the installment is deducted from the payslips from the start month (Jalali).
func (a *API) grantLoan(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Principal     money.Money `json:"principal"`
		Installment   money.Money `json:"installment"`
		StartYear     int         `json:"start_year"`
		StartMonth    int         `json:"start_month"`
		BankAccountID *uuid.UUID  `json:"bank_account_id"`
		Date          string      `json:"date"`
		Note          string      `json:"note"`
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
	l, err := a.m.Payroll.GrantLoan(r.Context(), payroll.LoanInput{IdemKey: "loan:" + key, EmployeeID: id, Principal: req.Principal, Installment: req.Installment,
		StartYear: req.StartYear, StartMonth: req.StartMonth, PayFrom: from, Date: d, Note: req.Note, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toLoan(l))
}

func (a *API) listLoans(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	list, err := a.m.Payroll.Loans(r.Context(), id)
	if err != nil {
		return err
	}
	out := make([]loanDTO, len(list))
	for i, l := range list {
		out[i] = toLoan(l)
	}
	return ok(w, out)
}

// ---- remittances ----

type remittanceDTO struct {
	ID          uuid.UUID         `json:"id"`
	Kind        payroll.Liability `json:"kind"`
	Amount      money.Money       `json:"amount"`
	PeriodYear  int               `json:"period_year,omitempty"`
	PeriodMonth int               `json:"period_month,omitempty"`
	PayFrom     string            `json:"pay_from"`
	Reference   string            `json:"reference,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
}

func toRemittance(m payroll.Remittance) remittanceDTO {
	return remittanceDTO{m.ID, m.Kind, m.Amount, m.PeriodYear, m.PeriodMonth, m.PayFrom, m.Reference, m.CreatedAt}
}

// remit pays withheld tax (kind=tax) or social security (kind=insurance) to its authority.
func (a *API) remit(w http.ResponseWriter, r *http.Request) error {
	key, err := idemKey(r)
	if err != nil {
		return err
	}
	req, err := httpx.Decode[struct {
		Kind          payroll.Liability `json:"kind"`
		Amount        money.Money       `json:"amount"`
		PeriodYear    int               `json:"period_year"`
		PeriodMonth   int               `json:"period_month"`
		BankAccountID *uuid.UUID        `json:"bank_account_id"`
		Date          string            `json:"date"`
		Reference     string            `json:"reference"`
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
	m, err := a.m.Payroll.Remit(r.Context(), payroll.RemitInput{IdemKey: "remit:" + key, Kind: req.Kind, Amount: req.Amount, PeriodYear: req.PeriodYear,
		PeriodMonth: req.PeriodMonth, PayFrom: from, Date: d, Reference: req.Reference, Actor: actorOf(r.Context())})
	if err != nil {
		return err
	}
	return created(w, toRemittance(m))
}

func (a *API) listRemittances(w http.ResponseWriter, r *http.Request) error {
	list, err := a.m.Payroll.Remittances(r.Context(), payroll.Liability(r.URL.Query().Get("kind")))
	if err != nil {
		return err
	}
	out := make([]remittanceDTO, len(list))
	for i, m := range list {
		out[i] = toRemittance(m)
	}
	return ok(w, out)
}

func (a *API) mountPayrollFiles(r *httpx.Router, p string) {
	r.Route("PUT "+p+"/payroll/employees/{id}/profile", a.putProfile)
	r.Route("GET "+p+"/payroll/employees/{id}/profile", a.getProfile)
	r.Route("PUT "+p+"/payroll/workshop", a.putWorkshop)
	r.Route("GET "+p+"/payroll/workshop", a.getWorkshop)
	r.Route("GET "+p+"/payroll/ssa-files", a.ssaFiles)
}

func (a *API) putProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pf, err := httpx.Decode[payroll.Profile](r)
	if err != nil {
		return err
	}
	if err := a.m.Payroll.SetProfile(r.Context(), id, pf); err != nil {
		return err
	}
	return ok(w, pf)
}

func (a *API) getProfile(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pf, found, err := a.m.Payroll.Profile(r.Context(), id)
	if err != nil {
		return err
	}
	if !found {
		return apperr.NotFound("profile")
	}
	return ok(w, pf)
}

func (a *API) putWorkshop(w http.ResponseWriter, r *http.Request) error {
	ws, err := httpx.Decode[payroll.Workshop](r)
	if err != nil {
		return err
	}
	if err := a.m.Payroll.SetWorkshop(r.Context(), ws); err != nil {
		return err
	}
	return ok(w, ws)
}

func (a *API) getWorkshop(w http.ResponseWriter, r *http.Request) error {
	ws, found, err := a.m.Payroll.Workshop(r.Context())
	if err != nil {
		return err
	}
	if !found {
		return apperr.NotFound("workshop")
	}
	return ok(w, ws)
}

// ssaFiles builds the two social-security list files of a month: ?year=&month=. The files come back base64-encoded in JSON. It refuses (409) when data is missing or not whole.
func (a *API) ssaFiles(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	year, err1 := strconv.Atoi(q.Get("year"))
	month, err2 := strconv.Atoi(q.Get("month"))
	if err1 != nil || err2 != nil {
		return apperr.Invalid("year and month are required (Jalali)")
	}
	kar, wor, err := a.m.SSAFiles(r.Context(), year, month, nil)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"DSKKAR00.DBF": base64.StdEncoding.EncodeToString(kar), "DSKWOR00.DBF": base64.StdEncoding.EncodeToString(wor),
		"note": "ASCII text only until a verified Iran System encoder is configured; the tax-office list (WH/WP) is not exported: its official spec was not found"})
}
