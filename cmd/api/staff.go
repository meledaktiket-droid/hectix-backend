package main

import (
	"net/http"
	"strings"

	adminroles "hectix/backend/internal/admin"
)

func staffOnly(next http.Handler) http.Handler {
	gated := adminroles.RequireStaff(nil)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.URL.Path == adminroles.AcceptPath && (r.URL.RawPath == "" || r.URL.RawPath == adminroles.AcceptPath)
		if strings.HasPrefix(r.URL.Path, "/api/auth") || accept {
			next.ServeHTTP(w, r)
			return
		}
		gated.ServeHTTP(w, r)
	})
}
