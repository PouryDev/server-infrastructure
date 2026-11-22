package invoice_test

import (
	"testing"

	"github.com/PouryDev/server-infrastructure/gokit/apperr"
	"github.com/PouryDev/server-infrastructure/mizan/invoice"
	"github.com/PouryDev/server-infrastructure/mizan/money"
)

func TestComputeItem(t *testing.T) {
	zero := 0
	cases := []struct {
		name                      string
		in                        invoice.ItemInput
		discount, net, tax, total int64
	}{
		{"plain vat 10%", invoice.ItemInput{Description: "x", Quantity: 3, UnitPrice: 1000}, 0, 3000, 300, 3300},
		{"percent discount", invoice.ItemInput{Description: "x", Quantity: 1, UnitPrice: 10000, DiscountBP: 1500}, 1500, 8500, 850, 9350},
		{"fixed + percent", invoice.ItemInput{Description: "x", Quantity: 2, UnitPrice: 5000, DiscountBP: 1000, Discount: 500}, 1500, 8500, 850, 9350},
		{"tax free", invoice.ItemInput{Description: "x", Quantity: 1, UnitPrice: 999, TaxBP: &zero}, 0, 999, 0, 999},
		{"rounds half up", invoice.ItemInput{Description: "x", Quantity: 1, UnitPrice: 105}, 0, 105, 11, 116}, // 10.5 -> 11
	}
	for _, c := range cases {
		it, err := invoice.ComputeItem(1, c.in, money.IRR, 1000)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if it.Discount != c.discount || it.Net != c.net || it.Tax != c.tax || it.Total != c.total {
			t.Errorf("%s: got %+v", c.name, it)
		}
	}
}

func TestComputeItemRejectsBadInput(t *testing.T) {
	bad := []invoice.ItemInput{
		{Description: "", Quantity: 1, UnitPrice: 1},
		{Description: "x", Quantity: 0, UnitPrice: 1},
		{Description: "x", Quantity: 1, UnitPrice: -1},
		{Description: "x", Quantity: 1, UnitPrice: 100, Discount: 101},
		{Description: "x", Quantity: 1, UnitPrice: 100, DiscountBP: 10001},
		{Description: "x", Quantity: 1 << 40, UnitPrice: 1 << 40}, // overflow
	}
	for i, in := range bad {
		if _, err := invoice.ComputeItem(1, in, money.IRR, 1000); !apperr.Is(err, apperr.KindInvalid) {
			t.Errorf("case %d: expected invalid, got %v", i, err)
		}
	}
}

func TestInvoiceTotalsAddUp(t *testing.T) {
	inv := invoice.Invoice{Currency: money.IRR}
	err := inv.Compute([]invoice.ItemInput{
		{Description: "a", Quantity: 3, UnitPrice: 1000},
		{Description: "b", Quantity: 1, UnitPrice: 10000, DiscountBP: 1500},
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Total.Amount() != 3300+9350 || inv.TaxTotal.Amount() != 300+850 || inv.DiscountTotal.Amount() != 1500 || inv.Subtotal.Amount() != 13000 {
		t.Fatalf("%+v", inv)
	}
	if inv.Net().Amount()+inv.TaxTotal.Amount() != inv.Total.Amount() {
		t.Fatal("net + tax must equal total")
	}
	if err := (&invoice.Invoice{Currency: money.IRR}).Compute(nil, 1000); err == nil {
		t.Fatal("an invoice needs items")
	}
}
