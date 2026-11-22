package ssafile

import (
	"errors"
	"fmt"
	"time"
)

// KarFields is the structure of DSKKAR00.DBF (the workshop, one record) as read from the sample file.
var KarFields = []Field{
	{"DSK_ID", Char, 10}, {"DSK_NAME", Char, 30}, {"DSK_FARM", Char, 30}, {"DSK_ADRS", Char, 40}, {"DSK_KIND", Numeric, 1}, {"DSK_YY", Numeric, 2}, {"DSK_MM", Numeric, 2},
	{"DSK_LISTNO", Char, 12}, {"DSK_DISC", Char, 30}, {"DSK_NUM", Numeric, 5}, {"DSK_TDD", Numeric, 6}, {"DSK_TROOZ", Numeric, 12}, {"DSK_TMAH", Numeric, 12}, {"DSK_TMAZ", Numeric, 12},
	{"DSK_TMASH", Numeric, 12}, {"DSK_TTOTL", Numeric, 12}, {"DSK_TBIME", Numeric, 12}, {"DSK_TKOSO", Numeric, 12}, {"DSK_BIC", Numeric, 12}, {"DSK_RATE", Numeric, 5}, {"DSK_PRATE", Numeric, 2},
	{"DSK_BIMH", Numeric, 12}, {"MON_PYM", Char, 3},
}

// WorFields is the structure of DSKWOR00.DBF (the insured persons, one record each) as read from the sample file.
var WorFields = []Field{
	{"DSW_ID", Char, 10}, {"DSW_YY", Numeric, 2}, {"DSW_MM", Numeric, 2}, {"DSW_LISTNO", Char, 12}, {"DSW_ID1", Char, 8}, {"DSW_FNAME", Char, 50}, {"DSW_LNAME", Char, 50}, {"DSW_DNAME", Char, 50},
	{"DSW_IDNO", Char, 15}, {"DSW_IDPLC", Char, 30}, {"DSW_IDATE", Char, 8}, {"DSW_BDATE", Char, 8}, {"DSW_SEX", Char, 3}, {"DSW_NAT", Char, 10}, {"DSW_OCP", Char, 50}, {"DSW_SDATE", Char, 8},
	{"DSW_EDATE", Char, 8}, {"DSW_DD", Numeric, 2}, {"DSW_ROOZ", Numeric, 12}, {"DSW_MAH", Numeric, 12}, {"DSW_MAZ", Numeric, 12}, {"DSW_MASH", Numeric, 12}, {"DSW_TOTL", Numeric, 12},
	{"DSW_BIME", Numeric, 12}, {"DSW_PRATE", Numeric, 2}, {"DSW_JOB", Char, 6}, {"PER_NATCOD", Char, 10},
}

// Workshop is the workshop record. Every field is explicit: none has a default, because the sample files cannot confirm what the owner's values mean.
type Workshop struct {
	ID          string // DSK_ID: workshop code (10 characters)
	Name        string // DSK_NAME
	Employer    string // DSK_FARM
	Address     string // DSK_ADRS
	Kind        int64  // DSK_KIND (1 digit; meaning not verified)
	ListNo      string // DSK_LISTNO
	Description string // DSK_DISC
	// Employer-side figures whose rules the sample only approximates (about 20% and 3% of the insurable wages): supplied by the caller from the payroll rule, never derived here.
	EmployerShare    int64  // DSK_TKOSO
	UnemploymentCont int64  // DSK_BIC
	Rate             int64  // DSK_RATE (5 digits; meaning not verified)
	PRate            int64  // DSK_PRATE
	DifferenceAmount int64  // DSK_BIMH
	PaymentMonth     string // MON_PYM (3 characters)
}

// Worker is one insured person. Identities verified on the sample: MAH = ROOZ x DD and MASH = MAH + MAZ (checked by Build).
type Worker struct {
	ID            string // DSW_ID1: insurance number (8)
	First         string
	Last          string
	Father        string
	IDNumber      string // DSW_IDNO
	IDPlace       string // DSW_IDPLC
	IDDate        string // DSW_IDATE yy/mm/dd
	BirthDate     string // DSW_BDATE yy/mm/dd
	Sex           string // DSW_SEX
	Nationality   string // DSW_NAT
	Occupation    string // DSW_OCP
	StartDate     string // DSW_SDATE
	EndDate       string // DSW_EDATE
	Days          int64  // DSW_DD
	DailyWage     int64  // DSW_ROOZ
	MonthlyWage   int64  // DSW_MAH
	Benefits      int64  // DSW_MAZ
	Insurable     int64  // DSW_MASH
	Total         int64  // DSW_TOTL (>= Insurable: includes non-insurable pay; meaning from the name, not verified)
	EmployeeShare int64  // DSW_BIME
	PRate         int64  // DSW_PRATE
	JobCode       string // DSW_JOB (6)
	NationalID    string // PER_NATCOD (10)
}

// Build writes the two files for a Jalali month. The totals of the workshop record are the sums of the worker rows (as the sample shows) and the count is the number of workers; a worker
// whose figures break MAH = ROOZ x DD or MASH = MAH + MAZ, or with an empty required text, refuses the build.
func Build(year, month int, w Workshop, workers []Worker, enc TextEncoder, now time.Time) (kar, wor []byte, err error) {
	if year < 1300 || year > 1499 || month < 1 || month > 12 {
		return nil, nil, errors.New("ssafile: a Jalali year and month are required")
	}
	if len(workers) == 0 {
		return nil, nil, errors.New("ssafile: no insured person")
	}
	for _, s := range []struct{ name, v string }{{"workshop id", w.ID}, {"workshop name", w.Name}, {"list number", w.ListNo}, {"payment month", w.PaymentMonth}} {
		if s.v == "" {
			return nil, nil, fmt.Errorf("ssafile: %s is required", s.name)
		}
	}
	yy, mm := int64(year%100), int64(month)
	var tdd, trooz, tmah, tmaz, tmash, ttotl, tbime int64
	rows := make([]Row, 0, len(workers))
	for i, k := range workers {
		if k.NationalID == "" || len(k.NationalID) != 10 || k.First == "" || k.Last == "" || k.ID == "" {
			return nil, nil, fmt.Errorf("ssafile: worker %d: insurance number, names and a 10-digit national id are required", i+1)
		}
		if k.MonthlyWage != k.DailyWage*k.Days {
			return nil, nil, fmt.Errorf("ssafile: worker %d: monthly wage must equal daily wage x days", i+1)
		}
		if k.Insurable != k.MonthlyWage+k.Benefits {
			return nil, nil, fmt.Errorf("ssafile: worker %d: insurable total must equal monthly wage + benefits", i+1)
		}
		if k.Total < k.Insurable {
			return nil, nil, fmt.Errorf("ssafile: worker %d: total pay is below the insurable pay", i+1)
		}
		tdd += k.Days
		trooz += k.DailyWage
		tmah += k.MonthlyWage
		tmaz += k.Benefits
		tmash += k.Insurable
		ttotl += k.Total
		tbime += k.EmployeeShare
		rows = append(rows, Row{"DSW_ID": w.ID, "DSW_YY": yy, "DSW_MM": mm, "DSW_LISTNO": w.ListNo, "DSW_ID1": k.ID, "DSW_FNAME": k.First, "DSW_LNAME": k.Last, "DSW_DNAME": k.Father,
			"DSW_IDNO": k.IDNumber, "DSW_IDPLC": k.IDPlace, "DSW_IDATE": k.IDDate, "DSW_BDATE": k.BirthDate, "DSW_SEX": k.Sex, "DSW_NAT": k.Nationality, "DSW_OCP": k.Occupation,
			"DSW_SDATE": k.StartDate, "DSW_EDATE": k.EndDate, "DSW_DD": k.Days, "DSW_ROOZ": k.DailyWage, "DSW_MAH": k.MonthlyWage, "DSW_MAZ": k.Benefits, "DSW_MASH": k.Insurable,
			"DSW_TOTL": k.Total, "DSW_BIME": k.EmployeeShare, "DSW_PRATE": k.PRate, "DSW_JOB": k.JobCode, "PER_NATCOD": k.NationalID})
	}
	karRow := Row{"DSK_ID": w.ID, "DSK_NAME": w.Name, "DSK_FARM": w.Employer, "DSK_ADRS": w.Address, "DSK_KIND": w.Kind, "DSK_YY": yy, "DSK_MM": mm, "DSK_LISTNO": w.ListNo,
		"DSK_DISC": w.Description, "DSK_NUM": int64(len(workers)), "DSK_TDD": tdd, "DSK_TROOZ": trooz, "DSK_TMAH": tmah, "DSK_TMAZ": tmaz, "DSK_TMASH": tmash, "DSK_TTOTL": ttotl,
		"DSK_TBIME": tbime, "DSK_TKOSO": w.EmployerShare, "DSK_BIC": w.UnemploymentCont, "DSK_RATE": w.Rate, "DSK_PRATE": w.PRate, "DSK_BIMH": w.DifferenceAmount, "MON_PYM": w.PaymentMonth}
	if kar, err = Write(KarFields, []Row{karRow}, enc, now); err != nil {
		return nil, nil, err
	}
	if wor, err = Write(WorFields, rows, enc, now); err != nil {
		return nil, nil, err
	}
	return kar, wor, nil
}
