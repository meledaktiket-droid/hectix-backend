package admin

import (
	"strings"
	"testing"
)

func TestAdminVolunteerInviteAndRevoke(t *testing.T) {
	e := setup(t)
	e.seedOrg(t, "org1", "Org One", "active")
	e.seedEvent(t, "ev1", "org1", "Gauland Fest", "published")

	e.guarded(t, "GET", "/api/events/ev1/volunteers", nil)
	e.guarded(t, "POST", "/api/events/ev1/volunteers", map[string]any{"email": "vol@example.com"})

	// 1. Invite volunteer successfully
	recInvite := e.do(t, "POST", "/api/events/ev1/volunteers", map[string]any{
		"email": "vol@example.com",
	}, e.admin)
	want(t, recInvite, 201, "")

	var crewID, crewStatus string
	e.scalar(t, &crewID, `select id from event_crew where event_id = 'ev1' and email = 'vol@example.com'`)
	e.scalar(t, &crewStatus, `select status from event_crew where id = $1`, crewID)
	if crewStatus != "pending" {
		t.Fatalf("expected pending status, got %s", crewStatus)
	}

	// Verify activity history logged
	var histCount int
	e.scalar(t, &histCount, `select count(*) from event_activity_history where event_id = 'ev1' and target_id = $1 and category = 'volunteer'`, crewID)
	if histCount != 1 {
		t.Fatalf("expected 1 history record, got %d", histCount)
	}

	// Verify email sent with accept url
	msgs := e.mail.all()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Text, "/invite/accept?id=") {
		t.Fatalf("expected email with accept url, got msgs: %+v", msgs)
	}

	// 2. C1: If email is already invited with role 'admin', inviting as volunteer must return 409 Conflict
	e.Exec(t, `insert into invitation(id, organization_id, email, role, status, expires_at, inviter_id, created_at)
		values('inv-adm-1', 'org1', 'eo-admin@example.com', 'admin', 'pending', now() + interval '1 day', 'adm', now())`)
	recConflict := e.do(t, "POST", "/api/events/ev1/volunteers", map[string]any{
		"email": "eo-admin@example.com",
	}, e.admin)
	want(t, recConflict, 409, "")

	// 3. List volunteers returns list and history
	recList := e.do(t, "GET", "/api/events/ev1/volunteers", nil, e.admin)
	want(t, recList, 200, "")
	listData := decode[map[string]any](t, recList)
	vols := listData["volunteers"].([]any)
	if len(vols) != 1 {
		t.Fatalf("expected 1 volunteer in list, got %d", len(vols))
	}

	// 4. Revoke volunteer access
	recRevoke := e.do(t, "DELETE", "/api/events/ev1/volunteers/"+crewID, nil, e.admin)
	want(t, recRevoke, 200, "")

	e.scalar(t, &crewStatus, `select status from event_crew where id = $1`, crewID)
	if crewStatus != "revoked" {
		t.Fatalf("expected revoked status, got %s", crewStatus)
	}

	// Verify revoke logged in activity history
	var revokedHistCount int
	e.scalar(t, &revokedHistCount, `select count(*) from event_activity_history where event_id = 'ev1' and target_id = $1 and action = 'revoked'`, crewID)
	if revokedHistCount != 1 {
		t.Fatalf("expected 1 revoked history record, got %d", revokedHistCount)
	}
}
