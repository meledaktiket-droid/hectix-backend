package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hectix/backend/internal/config"
)

func routeCfg() config.Config {
	return config.Config{
		MidtransServerKey: "SB-Mid-server-test",
		Apps: map[string]config.App{
			"web.test":       {Name: "web", Host: "web.test", BaseURL: "http://web.test"},
			"organizer.test": {Name: "organizer", Host: "organizer.test", BaseURL: "http://organizer.test"},
		},
	}
}

func do(h http.Handler, host, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

func TestPaymentRoutes(t *testing.T) {
	h := newServer(routeCfg(), nil)

	if rec := do(h, "web.test", "POST", "/api/orders", `{}`); rec.Code != 401 || rec.Body.String() != "{\"error\":\"Unauthorized\"}\n" {
		t.Fatalf("orders: %d %q", rec.Code, rec.Body)
	}
	body := `{"order_id":"o1","status_code":"200","gross_amount":"1000.00","transaction_status":"settlement","signature_key":"bad"}`
	if rec := do(h, "web.test", "POST", "/api/midtrans", body); rec.Code != 403 || rec.Body.String() != "{\"error\":\"Invalid signature\"}\n" {
		t.Fatalf("midtrans: %d %q", rec.Code, rec.Body)
	}
	for path, want := range map[string]int{"/api/midtrans": 404, "/api/orders": 405} {
		if rec := do(h, "organizer.test", "POST", path, body); rec.Code != want {
			t.Fatalf("organizer %s: %d %q", path, rec.Code, rec.Body)
		}
	}
}
