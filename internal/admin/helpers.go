package admin

import (
	"context"
	"errors"
	"log"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/deps"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
)

type handlers struct{ d deps.Deps }

func list[T any](ctx context.Context, db dbx.DB, sql string, args ...any) ([]T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[T])
}

func one[T any](ctx context.Context, db dbx.DB, sql string, args ...any) (*T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByPos[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("admin: %s: %v", where, err)
	httpx.Error(w, http.StatusInternalServerError, "Internal server error")
}

func badBody(w http.ResponseWriter) {
	httpx.Error(w, http.StatusBadRequest, "Invalid request body")
}

func ok(w http.ResponseWriter) { httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true}) }

func (h *handlers) send(ctx context.Context, to string, m mail.Message) error {
	if h.d.Mail == nil {
		return nil
	}
	return h.d.Mail.Send(ctx, to, m.Subject, m.HTML, m.Text)
}

func parseIntPrefix(s string) (int, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg, s = s[0] == '-', s[1:]
	}
	n, digits := 0, 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		if n < 1<<30 {
			n = n*10 + int(c-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func pageParam(r *http.Request, key string) int {
	v := r.URL.Query().Get(key)
	if !r.URL.Query().Has(key) {
		v = "1"
	}
	n, ok := parseIntPrefix(v)
	if !ok {
		return 1
	}
	return max(1, n)
}

func totalPages(total int64, limit int) int64 {
	return max(1, (total+int64(limit)-1)/int64(limit))
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

func formatIDR(n int64) string {
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteByte(s[i])
	}
	return sign + "Rp " + b.String()
}

func jsNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		return jsStringToNumber(x)
	case []any:
		switch len(x) {
		case 0:
			return 0
		case 1:
			return jsStringToNumber(jsString(x[0]))
		}
	}
	return math.NaN()
}

func jsStringToNumber(s string) float64 {
	s = jsTrim(s)
	if s == "" {
		return 0
	}
	switch s {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	if len(s) > 2 && s[0] == '0' && strings.ContainsRune("xXoObB", rune(s[1])) {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[s[1]]
		if n, err := strconv.ParseUint(s[2:], base, 64); err == nil {
			return float64(n)
		}
		return math.NaN()
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9') && !strings.ContainsRune("+-.eE", c) {
			return math.NaN()
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}

func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsNumberString(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			if e != nil {
				parts[i] = jsString(e)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

func jsNumberString(n float64) string {
	if n == 0 {
		return "0"
	}
	a := math.Abs(n)
	if a >= 1e-6 && a < 1e21 {
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	s := strconv.FormatFloat(n, 'e', -1, 64)
	mant, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	exp = strings.TrimLeft(exp[1:], "0")
	return mant + "e" + sign + exp
}

var emailRe = regexp.MustCompile(`^[^\s\x0B\p{Z}\x{FEFF}@]+@[^\s\x0B\p{Z}\x{FEFF}@]+\.[^\s\x0B\p{Z}\x{FEFF}@]+$`)

func itoa(n int) string { return strconv.Itoa(n) }

type conds struct {
	parts []string
	args  []any
}

func (c *conds) add(sql string, args ...any) {
	for _, a := range args {
		c.args = append(c.args, a)
		sql = strings.Replace(sql, "?", "$"+itoa(len(c.args)), 1)
	}
	c.parts = append(c.parts, sql)
}

func (c *conds) where() string {
	if len(c.parts) == 0 {
		return ""
	}
	return " where " + strings.Join(c.parts, " and ")
}

func isAssetURL(s string) bool {
	if strings.ContainsFunc(s, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '\\' }) {
		return false
	}
	if strings.HasPrefix(s, "/uploads/") {
		return !strings.Contains(s, "/../") && !strings.HasSuffix(s, "/..")
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func likeContains(s string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s) + "%"
}
