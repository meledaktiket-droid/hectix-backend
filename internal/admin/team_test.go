package admin

import (
	"regexp"
	"strings"
	"sync"
	"testing"
)

func TestInvitations(t *testing.T) {
	e := setup(t)
	inv := map[string]any{"email": "new@x.com", "role": "staff"}
	e.guarded(t, "GET", "/api/team/invitations", nil, e.admin, e.staff)
	e.guarded(t, "POST", "/api/team/invitations", inv, e.admin, e.staff)
	e.guarded(t, "POST", "/api/team/invitations/x", nil, e.admin, e.staff)
	e.guarded(t, "DELETE", "/api/team/invitations/x", nil, e.admin, e.staff)

	want(t, e.do(t, "GET", "/api/team/invitations", nil, e.super), 200, `[]`)

	for _, b := range []map[string]any{
		{"email": "nope", "role": "staff"},
		{"email": "a@b.c", "role": "user"},
		{"email": "a@b.c", "role": "finance"},
		{"email": 5, "role": "staff"},
		{"email": "a@b.c"},
	} {
		want(t, e.do(t, "POST", "/api/team/invitations", b, e.super), 400, `{"error":"A valid email and role are required."}`)
	}
	want(t, e.do(t, "POST", "/api/team/invitations", map[string]any{"email": " ADM@example.com ", "role": "staff"}, e.super), 409, `{"error":"This person is already a team member."}`)

	want(t, e.do(t, "POST", "/api/team/invitations", map[string]any{"email": "  New@X.com ", "role": "admin"}, e.super), 200, `{"ok":true}`)
	want(t, e.do(t, "POST", "/api/team/invitations", inv, e.super), 409, `{"error":"An invite is already pending for this email.","code":"PENDING"}`)
	want(t, e.do(t, "POST", "/api/team/invitations", map[string]any{"email": "usr@example.com", "role": "super_admin"}, e.super), 200, `{"ok":true}`)

	rec := e.do(t, "GET", "/api/team/invitations", nil, e.super)
	assertGolden(t, "team-invitations.json", rec)
	got := decode[[]adminInvitation](t, rec)
	if len(got) != 2 || got[1].Email != "new@x.com" || got[1].Role != "admin" || got[1].InvitedBy != "super" || got[1].Status != "pending" ||
		got[1].AcceptedAt != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got[1].Token) {
		t.Fatalf("invitations %+v", got)
	}
	if d := got[1].ExpiresAt.Sub(got[1].CreatedAt.Time); d != inviteTTL {
		t.Fatalf("ttl %v", d)
	}
	msgs := e.mail.all()
	if len(msgs) != 2 || msgs[0].To != "new@x.com" || msgs[0].Subject != "You've been invited to Hectix Admin" ||
		!strings.Contains(msgs[0].Text, "as Admin.\n\nAccept: https://admin.test/invite/accept?token="+got[1].Token+"\n") {
		t.Fatalf("mail %+v", msgs)
	}

	want(t, e.do(t, "POST", "/api/team/invitations/"+got[1].ID, nil, e.super), 200, `{"ok":true}`)
	var token string
	e.scalar(t, &token, `select token from admin_invitation where id = $1`, got[1].ID)
	if token == got[1].Token || !strings.Contains(e.mail.all()[2].Text, token) {
		t.Fatal("resend did not rotate token")
	}

	want(t, e.do(t, "DELETE", "/api/team/invitations/"+got[1].ID, nil, e.super), 200, `{"ok":true}`)
	want(t, e.do(t, "DELETE", "/api/team/invitations/"+got[1].ID, nil, e.super), 404, `{"error":"No pending invite to revoke."}`)
	want(t, e.do(t, "POST", "/api/team/invitations/"+got[1].ID, nil, e.super), 404, `{"error":"No pending invite to resend."}`)
	want(t, e.do(t, "POST", "/api/team/invitations/nope", nil, e.super), 404, `{"error":"No pending invite to resend."}`)
	want(t, e.do(t, "POST", "/api/team/invitations", inv, e.super), 200, `{"ok":true}`)
}

func TestInvitationsConcurrent(t *testing.T) {
	e := setup(t)
	var wg sync.WaitGroup
	codes := make([]int, 6)
	for i := range codes {
		wg.Go(func() {
			codes[i] = e.do(t, "POST", "/api/team/invitations", map[string]any{"email": "race@x.com", "role": "staff"}, e.super).Code
		})
	}
	wg.Wait()
	var n int
	e.scalar(t, &n, `select count(*) from admin_invitation where status = 'pending'`)
	if n != 1 {
		t.Fatalf("pending invites %d, codes %v", n, codes)
	}
}

func TestMembers(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/team/members", nil, e.admin, e.staff)
	e.guarded(t, "PATCH", "/api/team/members/stf", map[string]any{"role": "admin"}, e.admin, e.staff)
	e.guarded(t, "DELETE", "/api/team/members/stf", nil, e.admin, e.staff)

	rec := e.do(t, "GET", "/api/team/members", nil, e.super)
	assertGolden(t, "team-members.json", rec)
	got := decode[[]teamMember](t, rec)
	if len(got) != 3 {
		t.Fatalf("members %+v", got)
	}
	role := func(id string) string {
		var r string
		e.scalar(t, &r, `select role from "user" where id = $1`, id)
		return r
	}

	want(t, e.do(t, "PATCH", "/api/team/members/stf", map[string]any{"role": "user"}, e.super), 400, `{"error":"Invalid role"}`)
	want(t, e.do(t, "PATCH", "/api/team/members/usr", map[string]any{"role": "admin"}, e.super), 404, `{"error":"Not a team member"}`)
	want(t, e.do(t, "PATCH", "/api/team/members/nope", map[string]any{"role": "admin"}, e.super), 404, `{"error":"Not a team member"}`)
	want(t, e.do(t, "PATCH", "/api/team/members/stf", map[string]any{"role": "admin"}, e.super), 200, `{"ok":true}`)
	if role("stf") != "admin" {
		t.Fatal("not promoted")
	}
	want(t, e.do(t, "PATCH", "/api/team/members/super", map[string]any{"role": "admin"}, e.super), 409, `{"error":"Cannot demote the last Super Admin."}`)
	want(t, e.do(t, "DELETE", "/api/team/members/super", nil, e.super), 409, `{"error":"Cannot remove the last Super Admin."}`)
	want(t, e.do(t, "PATCH", "/api/team/members/super", map[string]any{"role": "super_admin"}, e.super), 200, `{"ok":true}`)

	want(t, e.do(t, "DELETE", "/api/team/members/stf", nil, e.super), 200, `{"ok":true}`)
	want(t, e.do(t, "DELETE", "/api/team/members/stf", nil, e.super), 404, `{"error":"Not a team member"}`)
	if role("stf") != "user" {
		t.Fatal("not demoted")
	}

	e.SeedUser(t, "super2", "super_admin")
	want(t, e.do(t, "DELETE", "/api/team/members/super2", nil, e.super), 200, `{"ok":true}`)
}

func TestMembersLastSuperAdminRace(t *testing.T) {
	e := setup(t)
	e.SeedUser(t, "super2", "super_admin")
	s2 := e.Login(t, "super2", nil)
	for range 5 {
		e.Exec(t, `update "user" set role = 'super_admin' where id in ('super', 'super2')`)
		var wg sync.WaitGroup
		wg.Go(func() { e.do(t, "PATCH", "/api/team/members/super2", map[string]any{"role": "staff"}, e.super) })
		wg.Go(func() { e.do(t, "DELETE", "/api/team/members/super", nil, s2) })
		wg.Wait()
		var n int
		e.scalar(t, &n, `select count(*) from "user" where role = 'super_admin'`)
		if n != 1 {
			t.Fatalf("super admins left: %d", n)
		}
	}
}
