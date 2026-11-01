package admin

import (
	"net/http"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
)

func RequireStaff(pred func(role string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth.LoadError(r.Context()) != nil {
				httpx.Error(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			u := auth.UserFrom(r.Context())
			if u == nil {
				httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
				return
			}
			if !IsAdminRole(u.Role) || (pred != nil && !pred(u.Role)) {
				httpx.Error(w, http.StatusForbidden, "Forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
