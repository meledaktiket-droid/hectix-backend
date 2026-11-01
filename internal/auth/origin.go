package auth

import (
	"net/http"
	"net/url"
	"strings"

	"hectix/backend/internal/httpx"
)

func OriginCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authRoute := strings.HasPrefix(r.URL.Path, "/api/auth/")
		if authRoute && r.ContentLength != 0 && !jsonContentType(r.Header.Get("Content-Type")) {
			ct := r.Header.Get("Content-Type")
			msg := `Content-Type "` + ct + `" is not allowed. Allowed types: application/json`
			if ct == "" {
				msg = "Content-Type is required. Allowed types: application/json"
			}
			authError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", msg)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Cookie") == "" {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = r.Header.Get("Referer")
		}
		if origin == "" || origin == "null" {
			if authRoute {
				authError(w, http.StatusForbidden, "MISSING_OR_NULL_ORIGIN", "Missing or null Origin")
			} else {
				httpx.Error(w, http.StatusForbidden, "Invalid origin")
			}
			return
		}
		if !sameOrigin(origin, httpx.AppFrom(r.Context()).BaseURL) {
			if authRoute {
				authError(w, http.StatusForbidden, "INVALID_ORIGIN", "Invalid origin")
			} else {
				httpx.Error(w, http.StatusForbidden, "Invalid origin")
			}
			return
		}
		next.ServeHTTP(w, r)
	})
}

func jsonContentType(ct string) bool {
	base, _, _ := strings.Cut(strings.ToLower(ct), ";")
	return strings.Contains(strings.TrimSpace(base), "application/json")
}

func sameOrigin(u, base string) bool {
	a, err := url.Parse(u)
	b, berr := url.Parse(base)
	if err != nil || berr != nil || a.Host == "" || (a.Scheme != "http" && a.Scheme != "https") {
		return false
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}
