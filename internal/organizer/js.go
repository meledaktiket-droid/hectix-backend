package organizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type body map[string]json.RawMessage

func decodeObject(r *http.Request) (body, error) {
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, err
	}
	b := body{}
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' {
		if err := json.Unmarshal(t, &b); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (b body) has(k string) bool { _, ok := b[k]; return ok }

func (b body) isNull(k string) bool {
	v, ok := b[k]
	return !ok || string(bytes.TrimSpace(v)) == "null"
}

func (b body) str(k string) (string, bool) {
	var s string
	if v, ok := b[k]; ok && json.Unmarshal(v, &s) == nil && len(bytes.TrimSpace(v)) > 0 && bytes.TrimSpace(v)[0] == '"' {
		return s, true
	}
	return "", false
}

func (b body) nullStr(k string) (*string, bool) {
	if b.isNull(k) {
		return nil, true
	}
	s, ok := b.str(k)
	if !ok {
		return nil, false
	}
	return &s, true
}

func (b body) num(k string) (float64, bool) {
	v, ok := b[k]
	if !ok {
		return 0, false
	}
	var f float64
	t := bytes.TrimSpace(v)
	if len(t) == 0 || !(t[0] == '-' || (t[0] >= '0' && t[0] <= '9')) || json.Unmarshal(t, &f) != nil {
		return 0, false
	}
	return f, true
}

func (b body) integer(k string) (int64, bool) {
	f, ok := b.num(k)
	if !ok || f != math.Trunc(f) || f > math.MaxInt32 || f < math.MinInt32 {
		return 0, false
	}
	return int64(f), true
}

func (b body) truthy(k string) bool {
	v, ok := b[k]
	if !ok {
		return false
	}
	switch t := string(bytes.TrimSpace(v)); {
	case t == "null", t == "false", t == `""`:
		return false
	case len(t) > 0 && (t[0] == '-' || (t[0] >= '0' && t[0] <= '9')):
		f, _ := strconv.ParseFloat(t, 64)
		return f != 0
	}
	return true
}

func (b body) date(k string) (time.Time, bool) {
	if f, ok := b.num(k); ok {
		if math.IsInf(f, 0) || math.Abs(f) > 8.64e15 {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(f)).UTC(), true
	}
	s, ok := b.str(k)
	if !ok {
		return time.Time{}, false
	}
	return parseJSDate(s)
}

var jsDateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04",
	"2006-01-02",
	"2006-01",
	"2006",
}

func parseJSDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	for _, l := range jsDateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func jsNumber(v json.RawMessage, present bool) float64 {
	if !present {
		return math.NaN()
	}
	t := bytes.TrimSpace(v)
	switch {
	case len(t) == 0:
		return math.NaN()
	case string(t) == "null", string(t) == "false":
		return 0
	case string(t) == "true":
		return 1
	case t[0] == '"':
		var s string
		if json.Unmarshal(t, &s) != nil {
			return math.NaN()
		}
		return jsStringToNumber(s)
	case t[0] == '-' || (t[0] >= '0' && t[0] <= '9'):
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

func jsStringToNumber(s string) float64 {
	s = strings.TrimSpace(s)
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
		n, err := strconv.ParseUint(s[2:], base, 64)
		if err != nil {
			return math.NaN()
		}
		return float64(n)
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789+-.eE", c) {
			return math.NaN()
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}

func jsParseInt(s string) (int64, bool) {
	s = strings.TrimLeft(s, " \t\n\r\v\f\u00a0\ufeff")
	neg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		neg = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(s[:end], 10, 64)
	if err != nil {
		n = math.MaxInt64
	}
	if neg {
		n = -n
	}
	return n, true
}

func localeID(f float64) string {
	neg := f < 0
	s := strconv.FormatFloat(math.Abs(f), 'f', 3, 64)
	ip, fp, _ := strings.Cut(s, ".")
	fp = strings.TrimRight(fp, "0")
	var out []byte
	for i := range ip {
		if i > 0 && (len(ip)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, ip[i])
	}
	res := string(out)
	if fp != "" {
		res += "," + fp
	}
	if neg && res != "0" {
		res = "-" + res
	}
	return res
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

var eventCategories = []string{"seni-budaya", "atraksi", "edukasi", "musik", "outdoor"}

func sanitizeCategories(raw json.RawMessage) []string {
	var in []any
	out := []string{}
	if json.Unmarshal(raw, &in) != nil {
		return out
	}
	set := map[string]bool{}
	for _, v := range in {
		if s, ok := v.(string); ok {
			set[s] = true
		}
	}
	for _, c := range eventCategories {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}
