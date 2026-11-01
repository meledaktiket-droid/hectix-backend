package settings

import (
	"context"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
)

type Settings struct {
	WhatsappNumber      string  `json:"whatsappNumber"`
	ContactEmail        string  `json:"contactEmail"`
	InstagramURL        string  `json:"instagramUrl"`
	TiktokURL           string  `json:"tiktokUrl"`
	ServiceFee          float64 `json:"serviceFee"`
	PlatformFee         float64 `json:"platformFee"`
	MinPayout           float64 `json:"minPayout"`
	TaxPercent          float64 `json:"taxPercent"`
	MidtransVaFee       float64 `json:"midtransVaFee"`
	MidtransQrisPercent float64 `json:"midtransQrisPercent"`
}

var Defaults = Settings{
	ServiceFee:          7120,
	PlatformFee:         0,
	MinPayout:           50000,
	TaxPercent:          10,
	MidtransVaFee:       4000,
	MidtransQrisPercent: 0.7,
}

func num(v string, ok bool, fallback float64) float64 {
	v = strings.TrimSpace(v)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsInf(n, 0) || math.IsNaN(n) {
		return fallback
	}
	return n
}

func Get(ctx context.Context, db dbx.DB) (Settings, error) {
	rows, err := db.Query(ctx, `select key, value from site_setting`)
	if err != nil {
		return Settings{}, err
	}
	m := map[string]string{}
	var k, v string
	if _, err := pgx.ForEachRow(rows, []any{&k, &v}, func() error { m[k] = v; return nil }); err != nil {
		return Settings{}, err
	}
	str := func(key, fallback string) string {
		if v, ok := m[key]; ok {
			return v
		}
		return fallback
	}
	n := func(key string, fallback float64) float64 {
		v, ok := m[key]
		return num(v, ok, fallback)
	}
	d := Defaults
	return Settings{
		WhatsappNumber:      str("whatsapp_number", d.WhatsappNumber),
		ContactEmail:        str("contact_email", d.ContactEmail),
		InstagramURL:        str("instagram_url", d.InstagramURL),
		TiktokURL:           str("tiktok_url", d.TiktokURL),
		ServiceFee:          n("service_fee", d.ServiceFee),
		PlatformFee:         n("platform_fee", d.PlatformFee),
		MinPayout:           n("min_payout", d.MinPayout),
		TaxPercent:          n("tax_percent", d.TaxPercent),
		MidtransVaFee:       n("midtrans_va_fee", d.MidtransVaFee),
		MidtransQrisPercent: n("midtrans_qris_percent", d.MidtransQrisPercent),
	}, nil
}
