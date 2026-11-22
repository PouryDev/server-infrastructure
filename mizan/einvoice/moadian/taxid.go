package moadian

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The unique invoice tax number (شماره منحصر به فرد مالیاتی, taxid) follows the official paper RC_DCPS.SN_V1.3 «قالب شناسه یکتای حافظه مالیاتی و شماره منحصر به فرد مالیاتی»
// (Khordad 1401). Source of the text: its verbatim repost on maliatman.com (the official PDFs on intamedia.ir could not be reached), cross-checked with two working third-party
// implementations (Jooyeshgar/moadian PHP, googool2/moadian-invoice Python); the three official worked examples of the paper are the unit tests.
//
//	taxid (22 characters) = MEMORYID (6) + DATE (5 hex) + SERIAL (10 hex) + CHECK (1 decimal digit)
//
//   - MEMORYID: the fiscal-memory unique id from the Karpooshe. Allowed characters 1-9 and A D E F G H K M N O P R T W X Y Z; forbidden V Q L J I and 0; U S C B are reserved; a first
//     character P or N is reserved for the Central Bank's payment instruments and ids of trusted tax-service companies start with TP.
//   - DATE: the number of whole days from 1970-01-01 (Gregorian) to the registration date, in hex, left-padded with zeros to 5 digits.
//   - SERIAL: the memory's own invoice serial, ascending and unique inside the memory, in hex, left-padded to 10 digits.
//   - CHECK: the Verhoeff check digit of the decimal control string  decimal(MEMORYID) + days (decimal, 6 digits) + serial (decimal, 12 digits), where in the memory id a digit stays
//     itself and a letter becomes its ASCII code in decimal (A=65 ... Z=90).
//
// Open point of the paper: whether the day counter is the UTC or the Tehran-local day. Both implementations read, and this code uses, the UTC day of the issue time.

var (
	memoryIDPattern = regexp.MustCompile(`^[1-9ADEFGHKMNOPRTWXYZ]{6}$`)
	taxIDPattern    = regexp.MustCompile(`^[0-9A-Z]{6}[0-9A-F]{5}[0-9A-F]{10}[0-9]$`)
)

// Verhoeff tables.
var (
	verhoeffD = [10][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 2, 3, 4, 0, 6, 7, 8, 9, 5}, {2, 3, 4, 0, 1, 7, 8, 9, 5, 6}, {3, 4, 0, 1, 2, 8, 9, 5, 6, 7}, {4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
		{5, 9, 8, 7, 6, 0, 4, 3, 2, 1}, {6, 5, 9, 8, 7, 1, 0, 4, 3, 2}, {7, 6, 5, 9, 8, 2, 1, 0, 4, 3}, {8, 7, 6, 5, 9, 3, 2, 1, 0, 4}, {9, 8, 7, 6, 5, 4, 3, 2, 1, 0}}
	verhoeffP = [8][10]int{
		{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {1, 5, 7, 6, 2, 8, 3, 0, 9, 4}, {5, 8, 0, 3, 7, 9, 6, 1, 4, 2}, {8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
		{9, 4, 5, 3, 1, 2, 6, 8, 7, 0}, {4, 2, 8, 6, 5, 7, 3, 9, 0, 1}, {2, 7, 9, 3, 8, 0, 6, 4, 1, 5}, {7, 0, 4, 6, 9, 1, 3, 2, 5, 8}}
	verhoeffInv = [10]int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}
)

// VerhoeffDigit returns the Verhoeff check digit of a string of decimal digits.
func VerhoeffDigit(number string) (int, error) {
	c := 0
	n := len(number)
	for i := 0; i < n; i++ {
		ch := number[n-1-i]
		if ch < '0' || ch > '9' {
			return 0, errors.New("moadian: the control string must be decimal digits")
		}
		c = verhoeffD[c][verhoeffP[(i+1)%8][int(ch-'0')]]
	}
	return verhoeffInv[c], nil
}

// ValidMemoryID reports whether s has the shape of a fiscal-memory unique id.
func ValidMemoryID(s string) bool { return memoryIDPattern.MatchString(s) }

func memoryIDDecimal(id string) string {
	var b strings.Builder
	for _, r := range id {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteString(strconv.Itoa(int(r)))
		}
	}
	return b.String()
}

// DaysSinceEpoch is the number of whole UTC days from 1970-01-01 to t.
func DaysSinceEpoch(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// TaxID builds the 22-character unique tax number of an invoice from the fiscal-memory id, the issue time and the memory's own serial.
func TaxID(memoryID string, issuedAt time.Time, serial int64) (string, error) {
	if !ValidMemoryID(memoryID) {
		return "", fmt.Errorf("moadian: %q is not a fiscal-memory id (6 characters from 1-9 and ADEFGHKMNOPRTWXYZ)", memoryID)
	}
	days := DaysSinceEpoch(issuedAt)
	if days < 0 || days > 0xFFFFF {
		return "", errors.New("moadian: the issue date is outside the 5-hex-digit day range")
	}
	if serial < 0 || serial > 0xFFFFFFFFFF {
		return "", errors.New("moadian: the serial does not fit 10 hex digits")
	}
	control := fmt.Sprintf("%s%06d%012d", memoryIDDecimal(memoryID), days, serial)
	check, err := VerhoeffDigit(control)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%05X%010X%d", memoryID, days, serial, check), nil
}

// ValidTaxID checks the shape and the check digit of a tax number.
func ValidTaxID(id string) bool {
	if !taxIDPattern.MatchString(id) {
		return false
	}
	days, _ := strconv.ParseInt(id[6:11], 16, 64)
	serial, _ := strconv.ParseInt(id[11:21], 16, 64)
	check, err := VerhoeffDigit(fmt.Sprintf("%s%06d%012d", memoryIDDecimal(id[:6]), days, serial))
	return err == nil && strconv.Itoa(check) == id[21:]
}
