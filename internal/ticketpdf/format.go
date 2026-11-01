package ticketpdf

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

var monthsID = [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni",
	"Juli", "Agustus", "September", "Oktober", "November", "Desember"}

func formatDateID(t time.Time) string {
	t = t.In(wib)
	return fmt.Sprintf("%d %s %d", t.Day(), monthsID[t.Month()-1], t.Year())
}

func formatRupiah(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if s[0] == '-' {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(s[i])
	}
	return "Rp" + sign + b.String()
}

func location(venue, city string) string {
	var parts []string
	for _, p := range []string{venue, city} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",\n")
}
