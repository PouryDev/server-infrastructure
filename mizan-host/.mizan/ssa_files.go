package mizan

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/payroll"
	"github.com/PouryDev/server-infrastructure/mizan/payroll/ssafile"
)

// SSAFiles builds DSKKAR00.DBF and DSKWOR00.DBF for a Jalali month from the month's payslips (issued or paid), the employees' profiles and the workshop data. It FAILS CLOSED: every
// employee needs a profile, the workshop data must exist, and a payslip whose insurable pay is not a whole multiple of its days (the file stores a whole daily wage and requires
// monthly wage = daily wage x days) is refused rather than rounded. The mapping is the module's reading of the field names, confirmed only by the numeric identities of the sample
// files: DSW_DD = days, DSW_MAH = insurable pay, DSW_MAZ = 0, DSW_MASH = DSW_MAH, DSW_TOTL = gross, DSW_BIME = the employee's share withheld; DSK_TKOSO and DSK_BIC are the workshop's
// employer and unemployment shares of the total insurable pay at the rates stored with the workshop. Persian text goes through enc (nil = the built-in Iran System encoder).
func (m *Mizan) SSAFiles(ctx context.Context, year, month int, enc ssafile.TextEncoder) (kar, wor []byte, err error) {
	ws, ok, err := m.Payroll.Workshop(ctx)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, apperr.New(apperr.KindConflict, "no_workshop", "store the workshop data first (PUT /payroll/workshop)")
	}
	slips, err := m.Payroll.MonthSlips(ctx, year, month)
	if err != nil {
		return nil, nil, err
	}
	emps, err := m.Payroll.Employees(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := map[uuid.UUID]payroll.Employee{}
	for _, e := range emps {
		byID[e.ID] = e
	}
	var workers []ssafile.Worker
	var totalInsurable int64
	for _, sl := range slips {
		if !sl.Insurable.IsPositive() {
			continue // not insured this month
		}
		emp := byID[sl.EmployeeID]
		pf, found, err := m.Payroll.Profile(ctx, emp.ID)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			return nil, nil, apperr.New(apperr.KindConflict, "no_profile", "employee "+emp.Code+" has no profile (PUT /payroll/employees/{id}/profile)")
		}
		days, ins := int64(sl.Days), sl.Insurable.Amount()
		if days <= 0 || ins%days != 0 {
			return nil, nil, apperr.New(apperr.KindConflict, "daily_wage_not_whole", fmt.Sprintf("employee %s: the insurable pay %d is not a whole multiple of %d days", emp.Code, ins, days))
		}
		first, last := splitName(emp.Name)
		workers = append(workers, ssafile.Worker{ID: emp.InsuranceNo, First: first, Last: last, Father: pf.FatherName, IDNumber: pf.IDNumber, IDPlace: pf.IDPlace, IDDate: pf.IDDate,
			BirthDate: pf.BirthDate, Sex: pf.Sex, Nationality: pf.Nationality, Occupation: pf.Occupation, StartDate: pf.StartDate, EndDate: pf.EndDate, Days: days, DailyWage: ins / days,
			MonthlyWage: ins, Insurable: ins, Total: sl.Gross.Amount(), EmployeeShare: sl.EmployeeIns.Amount(), PRate: ws.WorkerPRate, JobCode: pf.JobCode, NationalID: emp.NationalID})
		totalInsurable += ins
	}
	w := ssafile.Workshop{ID: ws.Code, Name: ws.Name, Employer: ws.Employer, Address: ws.Address, Kind: ws.Kind, ListNo: ws.ListNo, Description: ws.Description, PaymentMonth: ws.PaymentMonth,
		Rate: ws.Rate, PRate: ws.PRate, EmployerShare: totalInsurable * int64(ws.EmployerShareBP) / 10000, UnemploymentCont: totalInsurable * int64(ws.UnemploymentBP) / 10000}
	return ssafile.Build(year, month, w, workers, enc, m.Ledger.Today())
}

// splitName splits "first last" at the last space; a single word is both.
func splitName(full string) (first, last string) {
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == ' ' {
			return full[:i], full[i+1:]
		}
	}
	return full, full
}
