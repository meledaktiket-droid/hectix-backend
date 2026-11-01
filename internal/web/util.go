package web

import (
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"

	"hectix/backend/internal/httpx"
)

func serverError(w http.ResponseWriter, where string, err error) {
	slog.Error("web: "+where, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "Internal server error")
}

func jsNumber(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

func page(q url.Values) (limit, offset int) {
	l := jsNumber(q.Get("limit"))
	if l == 0 || math.IsNaN(l) {
		l = 5
	}
	l = math.Min(math.Max(l, 1), 50)
	o := jsNumber(q.Get("offset"))
	if math.IsNaN(o) {
		o = 0
	}
	o = math.Min(math.Max(o, 0), math.MaxInt32)
	return int(l), int(o)
}

func nextOffset(hasMore bool, offset, limit int) *int {
	if !hasMore {
		return nil
	}
	n := offset + limit
	return &n
}

func jsLen(s string) int { return len(utf16.Encode([]rune(s))) }

func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }
