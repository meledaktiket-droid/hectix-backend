package main

import (
	"net/http/httptest"
	"testing"

	"hectix/backend/internal/config"
)

func TestOrgRoutesOrganizerOnly(t *testing.T) {
	cfg := config.Config{Apps: map[string]config.App{
		"web.test":   {Name: "web", Host: "web.test", BaseURL: "http://web.test"},
		"org.test":   {Name: "organizer", Host: "org.test", BaseURL: "http://org.test"},
		"admin.test": {Name: "admin", Host: "admin.test", BaseURL: "http://admin.test"},
	}}
	h := newServer(cfg, nil)
	for host, want := range map[string]int{"org.test": 401, "web.test": 404, "admin.test": 404} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/auth/organization/list", nil)
		req.Host = host
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: got %d %q, want %d", host, rec.Code, rec.Body.String(), want)
		}
	}
}
