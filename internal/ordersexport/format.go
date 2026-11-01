package ordersexport

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var wib = time.FixedZone("WIB", 7*3600)

var monthsID = [12]string{"Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

func formatRupiah(n float64) string {
	r := math.Floor(n)
	if n-r >= 0.5 {
		r++
	}
	sign := ""
	if r < 0 || (r == 0 && (n < 0 || math.Signbit(n))) {
		sign = "-"
	}
	return "Rp" + sign + groupThousands(strconv.FormatFloat(math.Abs(r), 'f', 0, 64))
}

func formatIDR(n float64) string {
	switch {
	case math.IsNaN(n):
		return "RpNaN"
	case math.IsInf(n, 1):
		return "Rp∞"
	case math.IsInf(n, -1):
		return "-Rp∞"
	}
	sign := ""
	if math.Signbit(n) {
		sign = "-"
	}
	intPart, frac := roundDecimal(strconv.FormatFloat(math.Abs(n), 'f', -1, 64), 2)
	s := sign + "Rp " + groupThousands(intPart)
	if frac != "" {
		s += "," + frac
	}
	return s
}

func roundDecimal(s string, digits int) (string, string) {
	intPart, frac, _ := strings.Cut(s, ".")
	if len(frac) > digits {
		up := frac[digits] >= '5'
		b := []byte(intPart + frac[:digits])
		if up {
			i := len(b) - 1
			for ; i >= 0; i-- {
				if b[i] == '9' {
					b[i] = '0'
					continue
				}
				b[i]++
				break
			}
			if i < 0 {
				b = append([]byte{'1'}, b...)
			}
		}
		intPart, frac = string(b[:len(b)-digits]), string(b[len(b)-digits:])
	}
	return intPart, strings.TrimRight(frac, "0")
}

func groupThousands(digits string) string {
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func formatDateID(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return fmt.Sprintf("%d %s %d", t.Day(), monthsID[t.Month()-1], t.Year())
}
