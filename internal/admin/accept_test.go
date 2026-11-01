package admin

import (
	"net/http"
	"sync"
	"testing"
)

func (e *env) seedInvite(t *testing.T, token, email, role, status, expiresIn string) {
	t.Helper()
	e.Exec(t, `insert into admin_invitation(id,email,role,token,status,invited_by,expires_at,created_at)
		values('inv-'||$1,$2,$3,$1,$4,'super',(now() at time zone 'utc') + $5::interval,now())`, token, email, role, status, expiresIn)
}

func (e *env) role(t *testing.T, id string) string {
	t.Helper()
	var r string
	e.scalar(t, &r, `select role from "user" where id = $1`, id)
	return r
}

func TestAcceptInvitation(t *testing.T) {
	e := setup(t)
	e.Exec(t, `insert into "user"(id,name,email,email_verified,role,created_at,updated_at) values('siti','Siti','Siti@Example.com',true,'user',now(),now())`)
	siti := e.Login(t, "siti", nil)
	e.seedInvite(t, "tok", "siti@example.com", "admin", "pending", "1 day")
	e.seedInvite(t, "old", "siti@example.com", "admin", "pending", "-1 minute")
	e.seedInvite(t, "rev", "siti@example.com", "admin", "revoked", "1 day")
	e.seedInvite(t, "done", "x@example.com", "admin", "accepted", "1 day")
	e.seedInvite(t, "odd", "siti@example.com", "finance", "pending", "1 day")

	cases := []struct {
		token  any
		cookie *http.Cookie
		want   string
	}{
		{"", siti, `{"status":"invalid"}`},
		{nil, siti, `{"status":"invalid"}`},
		{"missing", nil, `{"status":"invalid"}`},
		{"rev", siti, `{"status":"invalid"}`},
		{"done", nil, `{"status":"accepted"}`},
		{"old", siti, `{"status":"expired"}`},
		{"tok", nil, `{"status":"needs-signin","email":"siti@example.com","roleLabel":"Admin"}`},
		{"odd", nil, `{"status":"needs-signin","email":"siti@example.com","roleLabel":"finance"}`},
		{"tok", e.user, `{"status":"mismatch","invitedEmail":"siti@example.com","signedInEmail":"usr@example.com"}`},
		{"tok", e.staff, `{"status":"mismatch","invitedEmail":"siti@example.com","signedInEmail":"stf@example.com"}`},
	}
	for _, c := range cases {
		want(t, e.do(t, "POST", AcceptPath, map[string]any{"token": c.token}, c.cookie), 200, c.want)
	}
	if e.role(t, "siti") != "user" {
		t.Fatal("role changed before accept")
	}
	want(t, e.do(t, "POST", AcceptPath, "{", siti), 400, `{"error":"Invalid request body"}`)

	want(t, e.do(t, "POST", AcceptPath, map[string]any{"token": "tok"}, siti), 200, `{"status":"accepted"}`)
	var status string
	var accepted bool
	if err := e.DB.QueryRow(t.Context(), `select status, accepted_at is not null from admin_invitation where token='tok'`).Scan(&status, &accepted); err != nil {
		t.Fatal(err)
	}
	if e.role(t, "siti") != "admin" || status != "accepted" || !accepted {
		t.Fatalf("role %s status %s accepted %v", e.role(t, "siti"), status, accepted)
	}
	want(t, e.do(t, "POST", AcceptPath, map[string]any{"token": "tok"}, siti), 200, `{"status":"accepted"}`)
	want(t, e.do(t, "GET", "/api/banners", nil, siti), 200, `[]`)
}

func TestAcceptInvitationKeepsLastSuperAdmin(t *testing.T) {
	e := setup(t)
	e.seedInvite(t, "tok", "super@example.com", "staff", "pending", "1 day")
	want(t, e.do(t, "POST", AcceptPath, map[string]any{"token": "tok"}, e.super), 200, `{"status":"accepted"}`)
	if e.role(t, "super") != "super_admin" {
		t.Fatal("last super admin demoted")
	}
	e.SeedUser(t, "super2", "super_admin")
	e.seedInvite(t, "tok2", "super@example.com", "staff", "pending", "1 day")
	want(t, e.do(t, "POST", AcceptPath, map[string]any{"token": "tok2"}, e.super), 200, `{"status":"accepted"}`)
	if e.role(t, "super") != "staff" {
		t.Fatal("role not applied")
	}
}

func TestAcceptInvitationConcurrentRevoke(t *testing.T) {
	e := setup(t)
	e.Exec(t, `insert into "user"(id,name,email,email_verified,role,created_at,updated_at) values('siti','Siti','siti@example.com',true,'user',now(),now())`)
	siti := e.Login(t, "siti", nil)
	for i := range 5 {
		tok := "tok" + itoa(i)
		e.seedInvite(t, tok, "siti@example.com", "admin", "pending", "1 day")
		var wg sync.WaitGroup
		var acc, rev int
		wg.Go(func() { acc = e.do(t, "POST", AcceptPath, map[string]any{"token": tok}, siti).Code })
		wg.Go(func() { rev = e.do(t, "DELETE", "/api/team/invitations/inv-"+tok, nil, e.super).Code })
		wg.Wait()
		var status string
		e.scalar(t, &status, `select status from admin_invitation where token = $1`, tok)
		granted := e.role(t, "siti") == "admin"
		if acc != 200 || (status == "revoked") == granted || (rev == 200) != (status == "revoked") {
			t.Fatalf("status %s granted %v revoke %d", status, granted, rev)
		}
		e.Exec(t, `update "user" set role='user' where id='siti'`)
	}
}
