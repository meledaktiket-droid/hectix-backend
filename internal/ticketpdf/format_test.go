package ticketpdf

import (
	"testing"
	"time"
)

func TestFormatDateID(t *testing.T) {
	cases := map[string]string{
		"2026-11-14T12:30:00Z": "14 November 2026",
		"2026-11-14T17:00:00Z": "15 November 2026",
		"2026-01-01T00:00:00Z": "1 Januari 2026",
		"2025-12-31T16:59:59Z": "31 Desember 2025",
		"2025-12-31T17:00:00Z": "1 Januari 2026",
		"2026-05-09T03:00:00Z": "9 Mei 2026",
	}
	for in, want := range cases {
		ts, _ := time.Parse(time.RFC3339, in)
		if got := formatDateID(ts); got != want {
			t.Errorf("formatDateID(%s) = %q, want %q", in, got, want)
		}
		if got := formatDateID(ts.In(time.FixedZone("X", -5*3600))); got != want {
			t.Errorf("formatDateID(%s, other zone) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatRupiah(t *testing.T) {
	cases := map[int64]string{
		0: "Rp0", 5: "Rp5", 999: "Rp999", 1000: "Rp1.000", 227120: "Rp227.120",
		1227120: "Rp1.227.120", 1000000000: "Rp1.000.000.000", -2500: "Rp-2.500",
	}
	for in, want := range cases {
		if got := formatRupiah(in); got != want {
			t.Errorf("formatRupiah(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLocation(t *testing.T) {
	cases := []struct{ venue, city, want string }{
		{"Gelora Bung Karno", "Jakarta Pusat", "Gelora Bung Karno,\nJakarta Pusat"},
		{"Gelora Bung Karno", "", "Gelora Bung Karno"},
		{"", "Jakarta Pusat", "Jakarta Pusat"},
		{"", "", "-"},
	}
	for _, c := range cases {
		if got := location(c.venue, c.city); got != c.want {
			t.Errorf("location(%q,%q) = %q, want %q", c.venue, c.city, got, c.want)
		}
	}
}
