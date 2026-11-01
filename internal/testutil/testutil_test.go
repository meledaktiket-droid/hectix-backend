package testutil

import (
	"net/http"
	"testing"

	"hectix/backend/internal/auth"
)

func TestLoginAndDo(t *testing.T) {
	e := New(t, "web")
	e.Router.Get("/api/me", func(w http.ResponseWriter, r *http.Request) {
		if u := auth.UserFrom(r.Context()); u != nil {
			w.Write([]byte(u.ID))
		}
	})
	e.SeedUser(t, "u1", "user")
	if got := e.Do(t, "GET", "/api/me", nil, e.Login(t, "u1", nil)).Body.String(); got != "u1" {
		t.Fatalf("got %q", got)
	}
	if got := e.Do(t, "GET", "/api/me", nil, nil).Body.String(); got != "" {
		t.Fatalf("anonymous got %q", got)
	}
}
