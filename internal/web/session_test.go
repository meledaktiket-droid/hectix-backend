package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/auth"
)

func TestOptionalSessionLoadError(t *testing.T) {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithLoadError(req.Context(), errors.New("db down"))))
		})
	})
	mount(r, &handlers{cancelCharge: func(context.Context, string) error { return nil }})
	for path, want := range map[string]string{
		"/api/events/e1":             "{\"error\":\"Internal server error\"}\n",
		"/api/favorites/ids":         "{\"error\":\"Internal server error\"}\n",
		"/api/histori/a1/ticket.pdf": "Internal server error",
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 500 || rec.Body.String() != want {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Body)
		}
	}
}
