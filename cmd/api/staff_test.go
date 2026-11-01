package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	adminroles "hectix/backend/internal/admin"
)

func TestStaffOnlyExemptsInviteAccept(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := staffOnly(ok)
	for path, want := range map[string]int{adminroles.AcceptPath: 204, "/api/auth/get-session": 204, "/api/users": 401,
		"/api/team/invitations/%61ccept": 401, "/api/team/invitations/accept/": 401} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
		if rec.Code != want {
			t.Errorf("%s: got %d want %d", path, rec.Code, want)
		}
	}
}
