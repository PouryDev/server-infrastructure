package shop

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

type SavedAddress struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Default bool    `json:"default"`
	Address Address `json:"address"`
}

var postalRE = regexp.MustCompile(`^[0-9]{10}$`)

func (a Address) Normalize() (Address, error) {
	a.Name = strings.TrimSpace(a.Name)
	a.Province = strings.TrimSpace(a.Province)
	a.City = strings.TrimSpace(a.City)
	a.Street = strings.TrimSpace(a.Street)
	a.Postal = strings.NewReplacer("۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9", "٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9").Replace(strings.TrimSpace(a.Postal))
	var err error
	a.Phone, err = Phone(a.Phone)
	if err != nil {
		return a, err
	}
	if utf8.RuneCountInString(a.Name) < 2 || utf8.RuneCountInString(a.Name) > 120 || a.City == "" || len(a.City) > 160 || a.Province == "" || len(a.Province) > 160 || utf8.RuneCountInString(a.Street) < 10 || len(a.Street) > 2000 || !postalRE.MatchString(a.Postal) {
		return a, fmt.Errorf("%w: نام گیرنده، نشانی کامل و کد پستی ۱۰ رقمی را بررسی کنید", ErrInvalid)
	}
	return a, nil
}

type CustomerSummary struct {
	Total, Pending, Processing, Delivered, Paid int64
	Spent                                       int64
}
