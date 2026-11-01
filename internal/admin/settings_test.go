package admin

import (
	"math"
	"testing"
)

func TestJSCoercions(t *testing.T) {
	nums := []struct {
		v    any
		want float64
	}{
		{nil, 0}, {true, 1}, {false, 0}, {"", 0}, {"  12 ", 12}, {"1e3", 1000}, {"0x10", 16}, {".5", 0.5},
		{[]any{}, 0}, {[]any{"7"}, 7}, {12.5, 12.5}, {"-0", math.Copysign(0, -1)},
	}
	for _, c := range nums {
		if got := jsNumber(c.v); got != c.want {
			t.Errorf("Number(%#v) = %v want %v", c.v, got, c.want)
		}
	}
	for _, v := range []any{"abc", "1,000", "inf", "nan", "1_000", []any{1.0, 2.0}, map[string]any{}, "0x"} {
		if got := jsNumber(v); !math.IsNaN(got) {
			t.Errorf("Number(%#v) = %v want NaN", v, got)
		}
	}
	strs := []struct {
		v    any
		want string
	}{
		{nil, "null"}, {true, "true"}, {12.0, "12"}, {0.7, "0.7"}, {1e21, "1e+21"}, {1e-7, "1e-7"}, {123456789012.0, "123456789012"},
		{[]any{1.0, nil, "a"}, "1,,a"}, {map[string]any{}, "[object Object]"},
	}
	for _, c := range strs {
		if got := jsString(c.v); got != c.want {
			t.Errorf("String(%#v) = %q want %q", c.v, got, c.want)
		}
	}
}

func TestSettings(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/settings", nil)
	e.guarded(t, "PUT", "/api/settings", map[string]any{})

	want(t, e.do(t, "GET", "/api/settings", nil, e.staff), 200,
		`{"whatsapp_number":"","contact_email":"","instagram_url":"","tiktok_url":"","service_fee":"7120","platform_fee":"0","min_payout":"50000","tax_percent":"10","midtrans_va_fee":"4000","midtrans_qris_percent":"0.7"}`)

	want(t, e.do(t, "PUT", "/api/settings", map[string]any{
		"whatsapp_number": "+62 812-3456", "contact_email": "  Hi@Example.com ", "instagram_url": "instagram.com/x",
		"tiktok_url": "HTTP://tiktok.com/@x", "service_fee": "4000.5", "platform_fee": 7000, "min_payout": 50000.4,
		"tax_percent": "7.5", "midtrans_va_fee": 4440, "midtrans_qris_percent": 0.7,
	}, e.staff), 200, `{"ok":true}`)
	rec := e.do(t, "GET", "/api/settings", nil, e.staff)
	assertGolden(t, "settings.json", rec)
	want(t, rec, 200,
		`{"whatsapp_number":"628123456","contact_email":"Hi@Example.com","instagram_url":"https://instagram.com/x","tiktok_url":"HTTP://tiktok.com/@x","service_fee":"4001","platform_fee":"7000","min_payout":"50000","tax_percent":"7.5","midtrans_va_fee":"4440","midtrans_qris_percent":"0.7"}`)

	want(t, e.do(t, "PUT", "/api/settings", map[string]any{"instagram_url": " ", "contact_email": "", "whatsapp_number": nil, "platform_fee": nil}, e.staff), 200, `{"ok":true}`)
	want(t, e.do(t, "GET", "/api/settings", nil, e.staff), 200,
		`{"whatsapp_number":"","contact_email":"","instagram_url":"","tiktok_url":"HTTP://tiktok.com/@x","service_fee":"4001","platform_fee":"0","min_payout":"50000","tax_percent":"7.5","midtrans_va_fee":"4440","midtrans_qris_percent":"0.7"}`)

	bad := []struct {
		body map[string]any
		msg  string
	}{
		{map[string]any{"contact_email": "nope"}, "Email tidak valid"},
		{map[string]any{"contact_email": "a b@c.d"}, "Email tidak valid"},
		{map[string]any{"service_fee": -1}, "Biaya layanan tidak valid"},
		{map[string]any{"platform_fee": "abc"}, "Biaya platform tidak valid"},
		{map[string]any{"min_payout": map[string]any{}}, "Minimal pencairan tidak valid"},
		{map[string]any{"tax_percent": "Infinity"}, "Pajak tidak valid"},
		{map[string]any{"midtrans_va_fee": "-5"}, "Biaya Midtrans VA tidak valid"},
		{map[string]any{"midtrans_qris_percent": "x", "service_fee": 1}, "Biaya Midtrans QRIS tidak valid"},
		{map[string]any{"service_fee": 100000000.6}, "Biaya layanan maksimal Rp100.000.000"},
		{map[string]any{"platform_fee": 1e9}, "Biaya platform maksimal Rp100.000.000"},
		{map[string]any{"min_payout": "100000001"}, "Minimal pencairan maksimal Rp100.000.000"},
		{map[string]any{"tax_percent": 100.01}, "Pajak maksimal 100%"},
		{map[string]any{"midtrans_va_fee": 1e21}, "Biaya Midtrans VA maksimal Rp100.000.000"},
		{map[string]any{"midtrans_qris_percent": 101}, "Biaya Midtrans QRIS maksimal 100%"},
	}
	for _, b := range bad {
		want(t, e.do(t, "PUT", "/api/settings", b.body, e.staff), 400, `{"error":"`+b.msg+`"}`)
	}
	var fee string
	e.scalar(t, &fee, `select value from site_setting where key='service_fee'`)
	if fee != "4001" {
		t.Fatalf("service_fee %s", fee)
	}
	want(t, e.do(t, "PUT", "/api/settings", map[string]any{"service_fee": 100000000.4, "platform_fee": 100000000, "min_payout": 0,
		"tax_percent": 100, "midtrans_va_fee": "99999999.5", "midtrans_qris_percent": 100}, e.staff), 200, `{"ok":true}`)
	rec = e.do(t, "GET", "/api/settings", nil, e.staff)
	got := decode[siteSettings](t, rec)
	if got.ServiceFee != "100000000" || got.PlatformFee != "100000000" || got.MinPayout != "0" || got.TaxPercent != "100" ||
		got.MidtransVaFee != "100000000" || got.MidtransQrisPercent != "100" {
		t.Fatalf("bounds %+v", got)
	}
	want(t, e.do(t, "PUT", "/api/settings", "[1]", e.staff), 400, `{"error":"Invalid request body"}`)
	want(t, e.do(t, "PUT", "/api/settings", "null", e.staff), 400, `{"error":"Invalid request body"}`)
}
