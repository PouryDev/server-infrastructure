package ledger

import (
	"slices"
	"strings"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
)

// CodingNode is one code of a chart of accounts (کدینگ حسابداری): group (گروه), general (کل), subsidiary
// (معین) or detail (تفصیلی). The chart is optional metadata layered over the ledger.
type CodingNode struct {
	Code     string
	Parent   string // empty for a group
	Level    int    // 1..4
	Title    string
	TypeHint AccountType // advisory, not enforced
}

// ValidateCoding checks levels, parents and duplicates.
func ValidateCoding(nodes []CodingNode) error {
	byCode := map[string]CodingNode{}
	for _, n := range nodes {
		if n.Code == "" || n.Title == "" || n.Level < 1 || n.Level > 4 {
			return apperr.Invalid("every coding node needs a code, a title and a level between 1 and 4")
		}
		if _, dup := byCode[n.Code]; dup {
			return apperr.Invalid("duplicate coding code " + n.Code)
		}
		byCode[n.Code] = n
	}
	for _, n := range nodes {
		if n.Parent == "" {
			if n.Level != 1 {
				return apperr.Invalid("coding " + n.Code + " has no parent but is not a group")
			}
			continue
		}
		p, ok := byCode[n.Parent]
		if !ok || p.Level != n.Level-1 {
			return apperr.Invalid("coding " + n.Code + " must have a parent one level above")
		}
	}
	return nil
}

// CodingBalance is the total of every account mapped at or below a code.
type CodingBalance struct {
	Code    string
	Parent  string
	Level   int
	Title   string
	Debit   int64
	Credit  int64
	Balance int64 // natural direction of the node's type hint (debit-credit when unknown)
}

// CodingReport is a trial balance by chart-of-accounts code.
type CodingReport struct {
	Nodes        []CodingBalance   // only nodes that received an amount, in code order
	Unclassified []TrialBalanceRow // accounts without a (known) coding code
}

// RollUp adds every account's debit and credit to its coding node and to all of the node's ancestors.
func RollUp(rows []TrialBalanceRow, chart []CodingNode) CodingReport {
	byCode := make(map[string]CodingNode, len(chart))
	for _, n := range chart {
		byCode[n.Code] = n
	}
	acc := map[string]*CodingBalance{}
	var rep CodingReport
	for _, r := range rows {
		n, ok := byCode[r.CodingCode]
		if r.CodingCode == "" || !ok {
			rep.Unclassified = append(rep.Unclassified, r)
			continue
		}
		for guard := 0; ok && guard < 8; guard++ {
			b := acc[n.Code]
			if b == nil {
				b = &CodingBalance{Code: n.Code, Parent: n.Parent, Level: n.Level, Title: n.Title}
				acc[n.Code] = b
			}
			b.Debit += r.Debit
			b.Credit += r.Credit
			n, ok = byCode[n.Parent]
		}
	}
	for code, b := range acc {
		t := byCode[code].TypeHint
		if t == "" {
			t = Asset
		}
		b.Balance = Natural(t, b.Debit, b.Credit)
		rep.Nodes = append(rep.Nodes, *b)
	}
	slices.SortFunc(rep.Nodes, func(a, b CodingBalance) int { return strings.Compare(a.Code, b.Code) })
	return rep
}

// IranianCoding is a starting chart of accounts in the usual Iranian layout: group (1 digit), general
// (3 digits) and subsidiary (5 digits). It is a template, not a legal standard: companies adapt it, and
// the ledger works without it.
//
//nolint:staticcheck // the titles use the zero-width non-joiner (U+200C), which is correct Persian orthography
func IranianCoding() []CodingNode {
	n := func(code, parent string, level int, title string, t AccountType) CodingNode {
		return CodingNode{Code: code, Parent: parent, Level: level, Title: title, TypeHint: t}
	}
	return []CodingNode{
		n("1", "", 1, "دارایی‌های جاری", Asset),
		n("101", "1", 2, "موجودی نقد و بانک", Asset),
		n("10101", "101", 3, "صندوق", Asset),
		n("10102", "101", 3, "بانک‌ها", Asset),
		n("10103", "101", 3, "وجوه نزد درگاه‌های پرداخت", Asset),
		n("102", "1", 2, "حساب‌ها و اسناد دریافتنی", Asset),
		n("10201", "102", 3, "حساب‌های دریافتنی تجاری", Asset),
		n("10202", "102", 3, "اسناد دریافتنی (چک‌های دریافتنی)", Asset),
		n("10203", "102", 3, "سایر حساب‌های دریافتنی", Asset),
		n("103", "1", 2, "موجودی مواد و کالا", Asset),
		n("10301", "103", 3, "موجودی کالا", Asset),
		n("104", "1", 2, "پیش‌پرداخت‌ها", Asset),
		n("10401", "104", 3, "پیش‌پرداخت‌ها", Asset),
		n("105", "1", 2, "مالیات بر ارزش افزوده خرید", Asset),
		n("10501", "105", 3, "مالیات بر ارزش افزوده خرید (اعتبار مالیاتی)", Asset),
		n("2", "", 1, "دارایی‌های غیرجاری", Asset),
		n("201", "2", 2, "دارایی‌های ثابت مشهود", Asset),
		n("20101", "201", 3, "تجهیزات و اثاثه", Asset),
		n("202", "2", 2, "استهلاک انباشته", Asset),
		n("20201", "202", 3, "استهلاک انباشته تجهیزات و اثاثه", Asset),
		n("3", "", 1, "بدهی‌های جاری", Liability),
		n("301", "3", 2, "حساب‌ها و اسناد پرداختنی", Liability),
		n("30101", "301", 3, "حساب‌های پرداختنی تجاری", Liability),
		n("30102", "301", 3, "اسناد پرداختنی (چک‌های پرداختنی)", Liability),
		n("302", "3", 2, "سپرده‌ها و پیش‌دریافت‌ها", Liability),
		n("30201", "302", 3, "کیف پول مشتریان", Liability),
		n("30202", "302", 3, "پیش‌دریافت از مشتریان", Liability),
		n("303", "3", 2, "مالیات‌ها و عوارض پرداختنی", Liability),
		n("30301", "303", 3, "مالیات بر ارزش افزوده فروش", Liability),
		n("30302", "303", 3, "مالیات حقوق پرداختنی", Liability),
		n("30303", "303", 3, "حق بیمه تأمین اجتماعی پرداختنی", Liability),
		n("304", "3", 2, "حقوق و دستمزد پرداختنی", Liability),
		n("30401", "304", 3, "حقوق و دستمزد پرداختنی", Liability),
		n("4", "", 1, "بدهی‌های غیرجاری", Liability),
		n("401", "4", 2, "تسهیلات مالی بلندمدت", Liability),
		n("40101", "401", 3, "تسهیلات مالی بلندمدت", Liability),
		n("5", "", 1, "حقوق صاحبان سهام", Equity),
		n("501", "5", 2, "سرمایه", Equity),
		n("50101", "501", 3, "سرمایه", Equity),
		n("502", "5", 2, "اندوخته‌ها و سود (زیان) انباشته", Equity),
		n("50201", "502", 3, "اندوخته قانونی", Equity),
		n("50202", "502", 3, "سود (زیان) انباشته", Equity),
		n("6", "", 1, "درآمدها", Revenue),
		n("601", "6", 2, "درآمد عملیاتی", Revenue),
		n("60101", "601", 3, "فروش کالا و خدمات", Revenue),
		n("60102", "601", 3, "برگشت از فروش و تخفیفات", Revenue),
		n("602", "6", 2, "سایر درآمدها", Revenue),
		n("60201", "602", 3, "سایر درآمدها", Revenue),
		n("7", "", 1, "هزینه‌ها", Expense),
		n("701", "7", 2, "بهای تمام‌شده کالای فروش‌رفته", Expense),
		n("70101", "701", 3, "بهای تمام‌شده کالای فروش‌رفته", Expense),
		n("702", "7", 2, "هزینه‌های عملیاتی", Expense),
		n("70201", "702", 3, "حقوق و دستمزد", Expense),
		n("70202", "702", 3, "کارمزد درگاه‌های پرداخت و بانکی", Expense),
		n("70203", "702", 3, "سایر هزینه‌های عملیاتی", Expense),
	}
}
