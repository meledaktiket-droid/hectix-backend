package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"hectix/backend/internal/config"
)

func TestErrorShape(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, 409, "Tiket habis")
	if rec.Code != 409 || rec.Body.String() != "{\"error\":\"Tiket habis\"}\n" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestByHostDispatchesAndSetsApp(t *testing.T) {
	apps := map[string]config.App{
		"dev.hectix.id":           {Name: "web"},
		"organizer.dev.hectix.id": {Name: "organizer"},
	}
	h := ByHost(apps, map[string]http.Handler{
		"web": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("web:" + AppFrom(r.Context()).Name))
		}),
		"organizer": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("org"))
		}),
	})

	for host, want := range map[string]string{"dev.hectix.id": "web:web", "organizer.dev.hectix.id": "org"} {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Body.String() != want {
			t.Errorf("%s: got %q", host, rec.Body.String())
		}
	}

	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("unknown host: got %d", rec.Code)
	}
}

func TestJSONDoesNotEscapeHTML(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, 200, map[string]string{"m": "a>=1 & <b>"})
	if rec.Body.String() != "{\"m\":\"a>=1 & <b>\"}\n" {
		t.Fatalf("got %q", rec.Body.String())
	}
}
