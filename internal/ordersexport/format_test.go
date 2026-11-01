package ordersexport

import (
	"math"
	"testing"
	"time"
)

func TestFormatRupiah(t *testing.T) {
	cases := map[float64]string{
		0: "Rp0", 1500: "Rp1.500", 1234567: "Rp1.234.567", -4000: "Rp-4.000",
		1144.5: "Rp1.145", 2.675: "Rp3", 1144.125: "Rp1.144", -0.001: "Rp-0",
	}
	for in, want := range cases {
		if got := formatRupiah(in); got != want {
			t.Errorf("formatRupiah(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatCurrencyIDR(t *testing.T) {
	cases := map[float64]string{
		0: "Rp 0", 1500: "Rp 1.500", 1234567: "Rp 1.234.567", -4000: "-Rp 4.000",
		1144.5: "Rp 1.144,5", 0.125: "Rp 0,13", 1.005: "Rp 1,01", 2.675: "Rp 2,68",
		1144.125: "Rp 1.144,13", -0.001: "-Rp 0", 12.3456: "Rp 12,35",
		0.1 + 0.2: "Rp 0,3", 1234.565: "Rp 1.234,57", 99.995: "Rp 100", 0.005: "Rp 0,01",
		0.0049999: "Rp 0", 1e-7: "Rp 0", 1e21: "Rp 1.000.000.000.000.000.000.000",
		math.Inf(1): "Rp∞", math.Inf(-1): "-Rp∞", math.NaN(): "RpNaN",
	}
	for in, want := range cases {
		if got := formatIDR(in); got != want {
			t.Errorf("formatIDR(%v) = %q, want %q", in, got, want)
		}
	}
	if got := formatIDR(math.NaN()); got != "RpNaN" {
		t.Errorf("NaN: %q", got)
	}
}

func TestFormatDateID(t *testing.T) {
	ts := time.Date(2026, 9, 3, 20, 0, 0, 0, time.UTC)
	if got := formatDateID(ts, wib); got != "4 Sep 2026" {
		t.Errorf("got %q", got)
	}
	want := []string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}
	for m := 1; m <= 12; m++ {
		got := formatDateID(time.Date(2026, time.Month(m), 1, 0, 0, 0, 0, time.UTC), time.UTC)
		if got != "1 "+want[m-1]+" 2026" {
			t.Errorf("month %d: %q", m, got)
		}
	}
}

func TestFilename(t *testing.T) {
	now := time.Date(2026, 5, 31, 18, 30, 0, 0, time.UTC)
	if got := Filename("xlsx", now); got != "orders-2026-05-31.xlsx" {
		t.Errorf("got %q", got)
	}
	if got := ContentDisposition("orders-2026-05-31.csv"); got != `attachment; filename="orders-2026-05-31.csv"` {
		t.Errorf("got %q", got)
	}
}

func TestPaymentLabel(t *testing.T) {
	bca := "bca"
	cases := []struct {
		method string
		bank   *string
		want   string
	}{{"qris", nil, "QRIS"}, {"va_bank", &bca, "BCA VA"}, {"va_bank", nil, "Bank Transfer"}, {"credit_card", nil, "credit_card"}}
	for _, c := range cases {
		if got := PaymentLabel(c.method, c.bank); got != c.want {
			t.Errorf("PaymentLabel(%q) = %q, want %q", c.method, got, c.want)
		}
	}
}

func TestTicketSummary(t *testing.T) {
	got := TicketSummary([]Item{{2, "VIP"}, {1, "Regular"}})
	if got != "2× VIP, 1× Regular" {
		t.Errorf("got %q", got)
	}
	if TicketSummary(nil) != "" {
		t.Error("empty")
	}
}
