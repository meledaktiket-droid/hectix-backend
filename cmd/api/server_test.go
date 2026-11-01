package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hectix/backend/internal/config"
)

func TestServerRouting(t *testing.T) {
	cfg := config.Config{Apps: map[string]config.App{
		"web.test": {Name: "web", Host: "web.test", BaseURL: "http://web.test"},
	}}
	h := newServer(cfg, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("healthz: got %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Host = "unknown.test"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown host: got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/auth/get-session", nil)
	req.Host = "web.test"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "null\n" {
		t.Fatalf("get-session: got %d %q", rec.Code, rec.Body.String())
	}

	for _, path := range []string{"/api/auth/sign-out", "/api/anything"} {
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("POST", path, nil)
		req.Host = "web.test"
		req.Header.Set("Origin", "https://evil.com")
		req.Header.Set("Cookie", "web.session_token=x")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s cross-origin: got %d %q", path, rec.Code, rec.Body.String())
		}
	}
}
