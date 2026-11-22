// Package treasury is the treasury context (خزانه‌داری): bank accounts and the cash position, cheques received
// and issued (چک دریافتنی / پرداختنی) with their lifecycle, bank statement import and matching, and the
// settlement of gateway clearing into the bank.
//
// Like every context it never imports another one: money moves in the books through books.Poster, and the
// composition root decides which ledger accounts are meant.
package treasury

import (
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

// BankAccount is one of our accounts at a bank. Its balance is the balance of its ledger account.
type BankAccount struct {
	ID         uuid.UUID
	Name       string
	BankName   string
	IBAN       string // شبا, optional
	AccountNo  string // optional
	Currency   money.Currency
	LedgerCode string
	Active     bool
	CreatedAt  time.Time
}

// OpenAccountInput creates a bank account.
type OpenAccountInput struct {
	Name      string
	BankName  string
	IBAN      string
	AccountNo string
	Currency  money.Currency // default: the books' currency
	// LedgerCode is an existing or new asset account of the ledger. Empty: a new one is created.
	LedgerCode string
	Actor      string
}

func (in OpenAccountInput) validate() (OpenAccountInput, error) {
	in.Name, in.BankName = strings.TrimSpace(in.Name), strings.TrimSpace(in.BankName)
	var err error
	if in.IBAN, err = ValidateIBAN(in.IBAN); err != nil {
		return in, err
	}
	in.AccountNo = strings.TrimSpace(normalizeDigits(in.AccountNo))
	switch {
	case in.Name == "" || len(in.Name) > 120:
		return in, apperr.InvalidField("name", "required, at most 120 characters")
	case len(in.BankName) > 120:
		return in, apperr.InvalidField("bank_name", "at most 120 characters")
	case len(in.AccountNo) > 40:
		return in, apperr.InvalidField("account_no", "at most 40 characters")
	case in.Currency != "" && !in.Currency.Valid():
		return in, apperr.InvalidField("currency", "unsupported currency")
	case len(in.LedgerCode) > 100:
		return in, apperr.InvalidField("ledger_code", "at most 100 characters")
	}
	return in, nil
}

// ValidateIBAN checks an Iranian IBAN (شبا): "IR" and 24 digits whose ISO 13616 mod-97 check is valid.
// Spaces and Persian digits are accepted; an empty value is allowed (the field is optional).
func ValidateIBAN(s string) (string, error) {
	s = strings.ToUpper(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, normalizeDigits(s)))
	if s == "" {
		return "", nil
	}
	if len(s) != 26 || !strings.HasPrefix(s, "IR") || strings.Trim(s[2:], "0123456789") != "" || !mod97(s) {
		return "", apperr.InvalidField("iban", "must be a valid Iranian IBAN: IR followed by 24 digits")
	}
	return s, nil
}

// mod97 is the IBAN check: move the first four characters to the end, letters become 10..35, the number mod 97 is 1.
func mod97(iban string) bool {
	rest := iban[4:] + iban[:4]
	rem := 0
	for _, r := range rest {
		switch {
		case r >= '0' && r <= '9':
			rem = (rem*10 + int(r-'0')) % 97
		case r >= 'A' && r <= 'Z':
			rem = (rem*100 + int(r-'A') + 10) % 97
		default:
			return false
		}
	}
	return rem == 1
}

// normalizeDigits turns Persian and Arabic-Indic digits into ASCII.
func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		}
		return r
	}, s)
}

// AccountBalance is a bank account with its current balance.
type AccountBalance struct {
	Account BankAccount
	Balance money.Money
}

// ChequeSum is the number and total of the cheques in one state.
type ChequeSum struct {
	Direction Direction
	Status    ChequeStatus
	Count     int
	Total     money.Money
}

// Position is the cash position: what is in the banks, what is on its way in and what is already promised out.
type Position struct {
	Accounts []AccountBalance
	// Banks is the sum of all active bank accounts.
	Banks money.Money
	// Cheques lists the open cheques by direction and state.
	Cheques []ChequeSum
	// ChequesIn is received cheques not yet cleared (in hand and deposited); ChequesOut is issued cheques not yet paid
	// (issued and bounced).
	ChequesIn, ChequesOut money.Money
	// GatewayPending is what gateways collected and have not yet paid to the bank (the clearing account).
	GatewayPending money.Money
	// Projected is Banks + ChequesIn + GatewayPending - ChequesOut: where the cash ends up when everything clears.
	Projected money.Money
}
