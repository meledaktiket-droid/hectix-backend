package admin

import (
	"math"
	"net/http"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type siteSettings struct {
	WhatsappNumber      string `json:"whatsapp_number"`
	ContactEmail        string `json:"contact_email"`
	InstagramURL        string `json:"instagram_url"`
	TiktokURL           string `json:"tiktok_url"`
	ServiceFee          string `json:"service_fee"`
	PlatformFee         string `json:"platform_fee"`
	MinPayout           string `json:"min_payout"`
	TaxPercent          string `json:"tax_percent"`
	MidtransVaFee       string `json:"midtrans_va_fee"`
	MidtransQrisPercent string `json:"midtrans_qris_percent"`
}

func (h *handlers) getSettings(w http.ResponseWriter, r *http.Request) {
	rows, err := h.d.DB.Query(r.Context(), `select key, value from site_setting`)
	if err != nil {
		serverError(w, "settings", err)
		return
	}
	m := map[string]string{}
	var k, v string
	if _, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { m[k] = v; return nil }); err != nil {
		serverError(w, "settings", err)
		return
	}
	get := func(key, def string) string {
		if v, ok := m[key]; ok {
			return v
		}
		return def
	}
	httpx.JSON(w, http.StatusOK, siteSettings{
		get("whatsapp_number", ""), get("contact_email", ""), get("instagram_url", ""), get("tiktok_url", ""),
		get("service_fee", "7120"), get("platform_fee", "0"), get("min_payout", "50000"), get("tax_percent", "10"),
		get("midtrans_va_fee", "4000"), get("midtrans_qris_percent", "0.7"),
	})
}

var (
	nonDigits = regexp.MustCompile(`[^0-9]`)
	hasScheme = regexp.MustCompile(`(?i)^https?://`)
)

var numericSettings = []struct {
	key, msg, maxMsg string
	round            bool
	max              float64
}{
	{"service_fee", "Biaya layanan tidak valid", "Biaya layanan maksimal Rp100.000.000", true, maxRupiahSetting},
	{"platform_fee", "Biaya platform tidak valid", "Biaya platform maksimal Rp100.000.000", true, maxRupiahSetting},
	{"min_payout", "Minimal pencairan tidak valid", "Minimal pencairan maksimal Rp100.000.000", true, maxRupiahSetting},
	{"tax_percent", "Pajak tidak valid", "Pajak maksimal 100%", false, 100},
	{"midtrans_va_fee", "Biaya Midtrans VA tidak valid", "Biaya Midtrans VA maksimal Rp100.000.000", true, maxRupiahSetting},
	{"midtrans_qris_percent", "Biaya Midtrans QRIS tidak valid", "Biaya Midtrans QRIS maksimal 100%", false, 100},
}

const maxRupiahSetting = 100_000_000

func (h *handlers) putSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := httpx.Decode(r, &body); err != nil || body == nil {
		badBody(w)
		return
	}
	var updates [][2]string
	if v, ok := body["whatsapp_number"]; ok {
		updates = append(updates, [2]string{"whatsapp_number", nonDigits.ReplaceAllString(jsString(v), "")})
	}
	if v, ok := body["contact_email"]; ok {
		email := jsTrim(jsString(v))
		if email != "" && !emailRe.MatchString(email) {
			httpx.Error(w, http.StatusBadRequest, "Email tidak valid")
			return
		}
		updates = append(updates, [2]string{"contact_email", email})
	}
	normalizeURL := func(v any) string {
		s := jsTrim(jsString(v))
		if s == "" || hasScheme.MatchString(s) {
			return s
		}
		return "https://" + s
	}
	for _, k := range []string{"instagram_url", "tiktok_url"} {
		if v, ok := body[k]; ok {
			updates = append(updates, [2]string{k, normalizeURL(v)})
		}
	}
	for _, s := range numericSettings {
		v, ok := body[s.key]
		if !ok {
			continue
		}
		n := jsNumber(v)
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			httpx.Error(w, http.StatusBadRequest, s.msg)
			return
		}
		if s.round {
			n = jsRound(n)
		}
		if n > s.max {
			httpx.Error(w, http.StatusBadRequest, s.maxMsg)
			return
		}
		updates = append(updates, [2]string{s.key, jsNumberString(n + 0)})
	}

	now := time.Now().UTC()
	err := dbx.Tx(r.Context(), h.d.DB, func(tx pgx.Tx) error {
		for _, u := range updates {
			if _, err := tx.Exec(r.Context(), `insert into site_setting(key, value, updated_at) values($1, $2, $3)
				on conflict (key) do update set value = excluded.value, updated_at = excluded.updated_at`, u[0], u[1], now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		serverError(w, "put settings", err)
		return
	}
	ok(w)
}
