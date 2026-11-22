package payroll

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// Profile is the identity data of an employee that the insurance and tax lists carry. No field has a default; dates are "yy/mm/dd" Jalali strings as the list files write them.
type Profile struct {
	FatherName  string `json:"father_name"`
	IDNumber    string `json:"id_number"`  // شماره شناسنامه
	IDPlace     string `json:"id_place"`   // محل صدور
	IDDate      string `json:"id_date"`    // تاریخ صدور
	BirthDate   string `json:"birth_date"` // تاریخ تولد
	Sex         string `json:"sex"`        // M / F as the list expects (the sample uses Persian words: pass them through your TextEncoder)
	Nationality string `json:"nationality"`
	Occupation  string `json:"occupation"` // عنوان شغل
	JobCode     string `json:"job_code"`   // کد شغل (6)
	StartDate   string `json:"start_date"` // تاریخ شروع به کار
	EndDate     string `json:"end_date"`   // تاریخ ترک کار, empty while employed
}

// Workshop is the workshop (کارگاه) data of the insurance list. Single record.
type Workshop struct {
	Code            string `json:"code"` // کد کارگاه (10)
	Name            string `json:"name"`
	Employer        string `json:"employer"`
	Address         string `json:"address"`
	Kind            int64  `json:"kind"`
	ListNo          string `json:"list_no"` // شماره لیست (12)
	Description     string `json:"description"`
	PaymentMonth    string `json:"payment_month"` // 3 characters
	Rate            int64  `json:"rate"`
	PRate           int64  `json:"prate"`
	WorkerPRate     int64  `json:"worker_prate"`
	EmployerShareBP int    `json:"employer_share_bp"` // the employer's insurance share in basis points of the insurable pay (the rule version's: 2000 = 20%)
	UnemploymentBP  int    `json:"unemployment_bp"`   // the unemployment contribution (300 = 3%)
}

func (p Profile) validate() error {
	if len(p.JobCode) > 6 || len(p.Sex) > 3 || len(p.IDNumber) > 15 {
		return apperr.Invalid("a field is longer than the list file allows (job_code 6, sex 3, id_number 15)")
	}
	return nil
}

// SetProfile stores the profile of an employee (replacing the earlier one).
func (s *Service) SetProfile(ctx context.Context, employee uuid.UUID, p Profile) error {
	if err := p.validate(); err != nil {
		return err
	}
	if _, err := s.repo.Employee(ctx, employee, false); err != nil {
		return err
	}
	return s.repo.SetProfile(ctx, employee, p)
}

// Profile returns the profile of an employee; ok is false when none was stored.
func (s *Service) Profile(ctx context.Context, employee uuid.UUID) (Profile, bool, error) {
	return s.repo.Profile(ctx, employee)
}

// SetWorkshop stores the workshop data.
func (s *Service) SetWorkshop(ctx context.Context, w Workshop) error {
	if strings.TrimSpace(w.Code) == "" || len(w.Code) > 10 || len(w.ListNo) > 12 || len(w.PaymentMonth) > 3 || w.EmployerShareBP < 0 || w.UnemploymentBP < 0 {
		return apperr.Invalid("code (at most 10), list_no (12), payment_month (3) and non-negative shares are required")
	}
	return s.repo.SetWorkshop(ctx, w)
}

// Workshop returns the workshop data; ok is false when none was stored.
func (s *Service) Workshop(ctx context.Context) (Workshop, bool, error) { return s.repo.Workshop(ctx) }

// MonthSlips lists the payslips of a month that are not void (issued or paid), with their employee.
func (s *Service) MonthSlips(ctx context.Context, year, month int) ([]Payslip, error) {
	return s.repo.MonthSlips(ctx, year, month)
}
