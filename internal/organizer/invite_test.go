package organizer

import (
	"testing"
)

func TestInspectInvite(t *testing.T) {
	f := setup(t)
	f.Exec(t, `update "user" set name = 'Owner Name' where id = 'owner'`)
	f.Exec(t, `update "user" set name = '' where id = 'adm'`)
	f.SeedUser(t, "invitee", "user")
	f.Exec(t, `update "user" set email = 'Invitee@Example.com' where id = 'invitee'`)
	f.Exec(t, `insert into invitation(id,organization_id,email,role,status,expires_at,inviter_id,created_at) values
		('i-ok','org1','invitee@example.com','admin','pending',now() + interval '1 day','owner',now()),
		('i-norole','org1','invitee@example.com',null,'pending',now() + interval '1 day','adm',now()),
		('i-custom','org1','invitee@example.com','billing','pending',now() + interval '1 day','owner',now()),
		('i-mem','org1','mem@example.com','member','pending',now() + interval '1 day','owner',now()),
		('i-exp','org1','invitee@example.com','member','pending',now() - interval '1 minute','owner',now()),
		('i-acc','org1','invitee@example.com','member','accepted',now() - interval '1 day','owner',now()),
		('i-can','org1','invitee@example.com','member','canceled',now() + interval '1 day','owner',now()),
		('i-rej','org1','invitee@example.com','member','rejected',now() + interval '1 day','owner',now())`)
	invitee := f.Login(t, "invitee", nil)
	post := func(id any, c any) string {
		t.Helper()
		body := map[string]any{"id": id}
		cookie := f.member
		switch v := c.(type) {
		case nil:
			cookie = nil
		case string:
			if v == "invitee" {
				cookie = invitee
			}
		}
		rec := f.do(t, "POST", "/api/invitations/inspect", body, cookie)
		expect(t, rec, 200, "")
		return rec.Body.String()
	}
	cases := []struct {
		id   any
		who  any
		want string
	}{
		{"missing", nil, `{"status":"invalid"}`},
		{"", nil, `{"status":"invalid"}`},
		{5, nil, `{"status":"invalid"}`},
		{"i-can", nil, `{"status":"invalid"}`},
		{"i-rej", "invitee", `{"status":"invalid"}`},
		{"i-acc", nil, `{"status":"accepted"}`},
		{"i-exp", nil, `{"status":"expired"}`},
		{"i-ok", nil, `{"status":"needs-signin","email":"invitee@example.com","orgName":"Org One","roleLabel":"Admin","inviterName":"Owner Name"}`},
		{"i-norole", nil, `{"status":"needs-signin","email":"invitee@example.com","orgName":"Org One","roleLabel":"Member","inviterName":"adm@example.com"}`},
		{"i-custom", nil, `{"status":"needs-signin","email":"invitee@example.com","orgName":"Org One","roleLabel":"billing","inviterName":"Owner Name"}`},
		{"i-ok", "member", `{"status":"mismatch","invitedEmail":"invitee@example.com","signedInEmail":"mem@example.com"}`},
		{"i-ok", "invitee", `{"status":"needs-accept"}`},
		{"i-mem", "member", `{"status":"accepted"}`},
	}
	for _, c := range cases {
		if got := post(c.id, c.who); got != c.want+"\n" {
			t.Errorf("%v/%v: got %s want %s", c.id, c.who, got, c.want)
		}
	}
	f.Exec(t, `alter table invitation drop constraint invitation_inviter_id_user_id_fk`)
	f.Exec(t, `update invitation set inviter_id = 'gone' where id = 'i-ok'`)
	if got := post("i-ok", nil); got != `{"status":"needs-signin","email":"invitee@example.com","orgName":"Org One","roleLabel":"Admin","inviterName":"A teammate"}`+"\n" {
		t.Errorf("inviter fallback: %s", got)
	}
	if f.scalar(t, `select count(*) from member where user_id = 'invitee'`) != int64(0) ||
		f.scalar(t, `select status from invitation where id = 'i-ok'`) != "pending" {
		t.Fatal("inspect mutated state")
	}
}

func TestVolunteerInviteInspectAndDecision(t *testing.T) {
	f := setup(t)
	f.SeedUser(t, "vol_user", "user")
	f.Exec(t, `update "user" set email = 'vol_test@example.com' where id = 'vol_user'`)
	f.event(t, "ev1", "org1")
	f.Exec(t, `insert into invitation(id, organization_id, email, role, status, expires_at, inviter_id, created_at)
		values('inv-vol', 'org1', 'vol_test@example.com', 'volunteer', 'pending', now() + interval '1 day', 'owner', now())`)
	f.Exec(t, `insert into event_crew(id, event_id, email, user_id, role, status, token, invited_by, expires_at, created_at)
		values('crew-vol', 'ev1', 'vol_test@example.com', null, 'volunteer', 'pending', 'tok-vol', 'owner', now() + interval '1 day', now())`)

	volCookie := f.Login(t, "vol_user", nil)

	// Inspect returns status: needs-accept with is_volunteer: true
	rec := f.do(t, "POST", "/api/invitations/inspect", map[string]any{"id": "inv-vol"}, volCookie)
	expect(t, rec, 200, "")
	res := decode(t, rec)
	if res["status"] != "needs-accept" || res["is_volunteer"] != true || res["eventName"] != "Event ev1" {
		t.Fatalf("expected needs-accept with is_volunteer=true and eventName=Event ev1, got %+v", res)
	}

	// Reject invitation updates invitation status and event_crew status to rejected
	recRej := f.do(t, "POST", "/api/auth/organization/reject-invitation", map[string]any{"invitationId": "inv-vol"}, volCookie)
	expect(t, recRej, 200, "")

	invStatus := f.scalar(t, `select status from invitation where id = $1`, "inv-vol").(string)
	crewStatus := f.scalar(t, `select status from event_crew where id = $1`, "crew-vol").(string)
	if invStatus != "rejected" || crewStatus != "rejected" {
		t.Fatalf("expected rejected status, got inv=%s crew=%s", invStatus, crewStatus)
	}
}
