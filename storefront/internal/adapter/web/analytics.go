package web

import (
	"encoding/csv"
	"fmt"
	"github.com/PouryDev/storefront/internal/domain/shop"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) analyticsCSV(w http.ResponseWriter, r *http.Request, u shop.User) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days != 7 && days != 30 && days != 90 {
		days = 30
	}
	a, e := s.App.Analytics(r.Context(), u, time.Now().AddDate(0, 0, -days))
	if e != nil {
		failure(w, e)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="storefront-analytics.csv"`)
	_, _ = w.Write([]byte{0xef, 0xbb, 0xbf})
	out := csv.NewWriter(w)
	rows := [][]string{{"شاخص", "مقدار", "واحد"}, {"بازه", strconv.Itoa(days), "روز"}, {"مرورگر متمایز", fmt.Sprint(a.Visitors), "مرورگر"}, {"بازدید", fmt.Sprint(a.Views), "رویداد"}, {"افزودن به سبد", fmt.Sprint(a.Adds), "رویداد"}, {"شروع تکمیل سفارش", fmt.Sprint(a.Checkouts), "رویداد"}, {"سفارش ثبت‌شده", fmt.Sprint(a.Orders), "سفارش"}, {"سفارش پرداخت‌شده", fmt.Sprint(a.Paid), "سفارش"}, {"فروش پرداخت‌شده", fmt.Sprint(a.Revenue), "ریال"}, {"تخفیف مصرف‌شده", fmt.Sprint(a.Discounts), "ریال"}}
	for _, row := range rows {
		if e = out.Write(row); e != nil {
			return
		}
	}
	for _, m := range a.Daily {
		if e = out.Write([]string{"فروش " + m.Label, fmt.Sprint(m.Value), "ریال"}); e != nil {
			return
		}
	}
	out.Flush()
}
