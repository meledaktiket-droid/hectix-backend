package payments

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type captured struct {
	path, user, pass string
	basic            bool
	body             map[string]any
}

func fakeMidtrans(t *testing.T, status int, resp string) (*Midtrans, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.path = r.Method + " " + r.URL.Path
		c.user, c.pass, c.basic = r.BasicAuth()
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &c.body); err != nil {
			t.Errorf("body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return &Midtrans{ServerKey: "SB-Mid-server-abc", BaseURL: srv.URL}, c
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestMidtransBaseURL(t *testing.T) {
	if MidtransBaseURL(false) != "https://api.sandbox.midtrans.com" || MidtransBaseURL(true) != "https://api.midtrans.com" {
		t.Fatal(MidtransBaseURL(false), MidtransBaseURL(true))
	}
}

func TestChargeBCA(t *testing.T) {
	m, c := fakeMidtrans(t, 200, `{"status_code":"201","status_message":"Success, Bank Transfer transaction is created","va_numbers":[{"bank":"bca","va_number":"12345678901"}]}`)
	va, qris, err := m.Charge(context.Background(), "ord-1", 205120, "va_bank", "bca")
	if err != nil || va != "12345678901" || qris != "" {
		t.Fatalf("va=%q qris=%q err=%v", va, qris, err)
	}
	if c.path != "POST /v2/charge" || !c.basic || c.user != "SB-Mid-server-abc" || c.pass != "" {
		t.Fatalf("%+v", c)
	}
	want := `{"bank_transfer":{"bank":"bca"},"custom_expiry":{"expiry_duration":15,"unit":"minute"},"payment_type":"bank_transfer","transaction_details":{"gross_amount":205120,"order_id":"ord-1"}}`
	if got := asJSON(c.body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestChargeMandiri(t *testing.T) {
	m, c := fakeMidtrans(t, 200, `{"status_code":"201","bill_key":"70012345","biller_code":"70012"}`)
	va, _, err := m.Charge(context.Background(), "ord-2", 100000, "va_bank", "mandiri")
	if err != nil || va != "70012345" {
		t.Fatalf("va=%q err=%v", va, err)
	}
	want := `{"custom_expiry":{"expiry_duration":15,"unit":"minute"},"echannel":{"bill_info1":"Payment:","bill_info2":"Online purchase"},"payment_type":"echannel","transaction_details":{"gross_amount":100000,"order_id":"ord-2"}}`
	if got := asJSON(c.body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestChargeQRIS(t *testing.T) {
	m, c := fakeMidtrans(t, 200, `{"status_code":"201","actions":[{"name":"deeplink-redirect","url":"https://x/deeplink"},{"name":"generate-qr-code","url":"https://x/qr"}]}`)
	va, qris, err := m.Charge(context.Background(), "ord-3", 50000, "qris", "")
	if err != nil || qris != "https://x/qr" || va != "" {
		t.Fatalf("va=%q qris=%q err=%v", va, qris, err)
	}
	want := `{"payment_type":"gopay","transaction_details":{"gross_amount":50000,"order_id":"ord-3"}}`
	if got := asJSON(c.body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestChargeErrors(t *testing.T) {
	cases := []struct {
		name, method, bank string
		status             int
		resp, wantErr      string
	}{
		{"status 406", "va_bank", "bca", 200, `{"status_code":"406","status_message":"Duplicate order ID"}`, "Duplicate order ID"},
		{"http 500", "va_bank", "bca", 500, `{"status_code":"500","status_message":"Sorry"}`, "500"},
		{"no va", "va_bank", "bri", 200, `{"status_code":"201","va_numbers":[]}`, "va"},
		{"no qr", "qris", "", 200, `{"status_code":"201","actions":[]}`, "qr"},
		{"bad json", "qris", "", 200, `not json`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, _ := fakeMidtrans(t, c.status, c.resp)
			_, _, err := m.Charge(context.Background(), "o", 1, c.method, c.bank)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestChargeUnsupportedMethod(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()
	m := &Midtrans{ServerKey: "k", BaseURL: srv.URL}
	for _, mb := range [][2]string{{"va_bank", ""}, {"cash", ""}, {"", "bca"}} {
		if _, _, err := m.Charge(context.Background(), "o", 1, mb[0], mb[1]); err == nil {
			t.Fatalf("%v: want error", mb)
		}
	}
	if called {
		t.Fatal("should not call Midtrans")
	}
}
