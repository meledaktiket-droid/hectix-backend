package admin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"hectix/backend/internal/auth"
)

func run(t *testing.T, ctx context.Context, pred func(string) bool) *httptest.ResponseRecorder {
	t.Helper()
	h := RequireStaff(pred)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	req := httptest.NewRequest("GET", "/api/x", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if rec.Code != code || rec.Body.String() != body {
		t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), code, body)
	}
}

func as(role string) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "u1", Email: "a@b.c", Role: role}, nil)
}

func TestRequireStaff(t *testing.T) {
	const unauth, forbidden = `{"error":"Unauthorized"}` + "\n", `{"error":"Forbidden"}` + "\n"

	expect(t, run(t, context.Background(), nil), 401, unauth)
	expect(t, run(t, auth.WithLoadError(context.Background(), errors.New("db down")), nil), 500, `{"error":"Internal server error"}`+"\n")
	for _, role := range []string{"user", "", "finance", "SUPER_ADMIN"} {
		expect(t, run(t, as(role), nil), 403, forbidden)
	}
	for _, role := range AdminRoles {
		expect(t, run(t, as(role), nil), 200, "ok")
	}
	expect(t, run(t, as("admin"), CanManageTeam), 403, forbidden)
	expect(t, run(t, as("super_admin"), CanManageTeam), 200, "ok")
	expect(t, run(t, as("user"), func(string) bool { return true }), 403, forbidden)
}

func TestRoles(t *testing.T) {
	cases := []struct {
		role                                  string
		admin, team, verify, payouts, content bool
	}{
		{"super_admin", true, true, true, true, true},
		{"admin", true, false, true, true, true},
		{"staff", true, false, false, false, true},
		{"user", false, false, false, false, false},
		{"finance", false, false, false, false, false},
		{"", false, false, false, false, false},
	}
	for _, c := range cases {
		got := []bool{IsAdminRole(c.role), CanManageTeam(c.role), CanVerifyOrgs(c.role), CanManagePayouts(c.role), CanManageContent(c.role)}
		want := []bool{c.admin, c.team, c.verify, c.payouts, c.content}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%q: predicate %d = %v", c.role, i, got[i])
			}
		}
		if StaffRoles[c.role] != c.admin {
			t.Errorf("%q: StaffRoles = %v", c.role, StaffRoles[c.role])
		}
	}
	if RoleLabels["super_admin"] != "Super Admin" || RoleLabels["admin"] != "Admin" || RoleLabels["staff"] != "Staff" || len(RoleLabels) != 3 {
		t.Errorf("RoleLabels = %v", RoleLabels)
	}
}
