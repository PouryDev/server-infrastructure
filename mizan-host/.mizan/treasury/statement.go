package treasury

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/jalali"
)

// LineStatus of a bank statement line.
type LineStatus string

const (
	LineUnmatched LineStatus = "unmatched"
	LineMatched   LineStatus = "matched"
	LineIgnored   LineStatus = "ignored"
)

// MatchKind says what a statement line was matched to.
type MatchKind string

const (
	MatchCheque     MatchKind = "cheque"     // clears a deposited received cheque or an issued one
	MatchSettlement MatchKind = "settlement" // confirms a gateway settlement already booked
	MatchFee        MatchKind = "fee"        // a bank charge: booked as an expense
	MatchIgnored    MatchKind = "ignored"    // nothing to book (already booked another way, or not ours to track)
)

// StatementRow is one line of an imported statement.
type StatementRow struct {
	Date        time.Time
	Amount      int64 // signed: positive money in, negative money out
	Description string
	Reference   string
	Balance     *int64
}

// Line is a stored statement line.
type Line struct {
	ID          uuid.UUID
	AccountID   uuid.UUID
	Date        time.Time
	Amount      int64
	Description string
	Reference   string
	Balance     *int64
	Fingerprint string
	Status      LineStatus
	MatchKind   MatchKind
	MatchID     string
	MatchedBy   string
	MatchedAt   *time.Time
	JournalID   *uuid.UUID
	ImportedBy  string
	CreatedAt   time.Time
}

// ImportResult says what an import did.
type ImportResult struct{ Read, Inserted, Skipped int }

// Fingerprints identify rows so that importing the same statement again, or an overlapping one, adds nothing:
// the account, date, amount, reference and description, plus the ordinal among identical rows of the file (two
// genuinely identical lines in one statement stay two lines).
func Fingerprints(account uuid.UUID, rows []StatementRow) []string {
	seen := map[string]int{}
	out := make([]string, len(rows))
	for i, r := range rows {
		base := fmt.Sprintf("%s|%s|%d|%s|%s", account, r.Date.Format("2006-01-02"), r.Amount, strings.TrimSpace(r.Reference), strings.TrimSpace(r.Description))
		n := seen[base]
		seen[base] = n + 1
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", base, n)))
		out[i] = hex.EncodeToString(sum[:])
	}
	return out
}

// ParseDate accepts 2026-11-09 (Gregorian) and 1405/08/18 (Jalali), Persian digits included.
func ParseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(normalizeDigits(s))
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '-' })
	if len(parts) == 3 {
		var n [3]int
		for i, p := range parts {
			v, err := strconv.Atoi(p)
			if err != nil {
				return time.Time{}, apperr.Invalid("dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
			}
			n[i] = v
		}
		if t, err := (jalali.Date{Year: n[0], Month: n[1], Day: n[2]}).Time(time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, apperr.Invalid("dates are YYYY-MM-DD or Jalali YYYY/MM/DD")
}

func parseAmount(s string) (int64, error) {
	s = strings.TrimSpace(normalizeDigits(s))
	s = strings.NewReplacer(",", "", "٬", "", " ", "", "+", "").Replace(s)
	if s == "" {
		return 0, nil
	}
	return strconv.ParseInt(s, 10, 64)
}

// ParseStatementCSV reads a bank statement. The first line names the columns (any order, any case):
//
//	date, description, reference, balance   and either   amount (signed)   or   credit and debit (both unsigned)
//
// Dates are Gregorian or Jalali, amounts are rials, thousands separators and Persian digits are accepted.
// Banks export differently: convert a bank's own format to this one (a spreadsheet is enough).
func ParseStatementCSV(r io.Reader) ([]StatementRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return nil, apperr.Invalid("the statement is empty")
	}
	idx := map[string]int{}
	for i, h := range head {
		idx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf")))] = i
	}
	_, hasAmount := idx["amount"]
	_, hasCredit := idx["credit"]
	_, hasDebit := idx["debit"]
	hasSplit := hasCredit && hasDebit
	if _, ok := idx["date"]; !ok || (!hasAmount && !hasSplit) {
		return nil, apperr.Invalid("the statement needs the columns date and amount (or credit and debit)")
	}
	get := func(rec []string, k string) string {
		if i, ok := idx[k]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var out []StatementRow
	for line := 2; ; line++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, apperr.Invalid(fmt.Sprintf("line %d: %v", line, err))
		}
		bad := func(what string) error { return apperr.Invalid(fmt.Sprintf("line %d: invalid %s", line, what)) }
		d, err := ParseDate(get(rec, "date"))
		if err != nil {
			return nil, bad("date")
		}
		var amount int64
		if hasAmount {
			if amount, err = parseAmount(get(rec, "amount")); err != nil {
				return nil, bad("amount")
			}
		} else {
			cr, err1 := parseAmount(get(rec, "credit"))
			db, err2 := parseAmount(get(rec, "debit"))
			if err1 != nil || err2 != nil || cr < 0 || db < 0 || (cr != 0 && db != 0) {
				return nil, bad("credit/debit")
			}
			amount = cr - db
		}
		if amount == 0 {
			return nil, bad("amount (zero)")
		}
		row := StatementRow{Date: d, Amount: amount, Description: get(rec, "description"), Reference: get(rec, "reference")}
		if b := get(rec, "balance"); b != "" {
			v, err := parseAmount(b)
			if err != nil {
				return nil, bad("balance")
			}
			row.Balance = &v
		}
		out = append(out, row)
	}
	if len(out) == 0 {
		return nil, apperr.Invalid("the statement has no lines")
	}
	return out, nil
}

// Candidate is something a statement line may belong to.
type Candidate struct {
	Kind MatchKind
	ID   uuid.UUID
	// High means the line names the item (its Sayad identifier or settlement reference appears in the text) and the
	// amount is equal: safe to apply automatically. Otherwise only the amount agrees and a person decides.
	High   bool
	Reason string
}
