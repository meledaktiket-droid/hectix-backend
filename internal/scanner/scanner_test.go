package scanner

import (
	"encoding/json"
	"net/http"
	"testing"

	"hectix/backend/internal/testutil"
)

func TestStandaloneScannerMultiOrgAndIsolation(t *testing.T) {
	e := testutil.New(t, "scanner")
	Mount(e.Router, e.Deps)

	e.SeedUser(t, "owner", "user")
	e.SeedUser(t, "vol", "user")

	e.Exec(t, `
		insert into organization(id, name, slug, created_at, status) values
			('org1', 'Org One', 'org1', now(), 'verified'),
			('org2', 'Org Two', 'org2', now(), 'verified'),
			('org3', 'Org Three', 'org3', now(), 'verified');
		insert into member(id, organization_id, user_id, role, created_at) values
			('m1', 'org1', 'owner', 'owner', now()),
			('m2', 'org2', 'owner', 'owner', now()),
			('m3', 'org3', 'owner', 'owner', now()),
			('m-vol', 'org1', 'vol', 'owner', now());
		insert into event(id, organization_id, name, starts_at, ends_at, status, created_at, updated_at) values
			('ev1', 'org1', 'Gauland Fest', now() + interval '1 day', now() + interval '2 days', 'published', now(), now()),
			('ev2', 'org2', 'Pesta Ghoib', now() - interval '2 days', now() - interval '1 day', 'published', now(), now()),
			('ev3', 'org3', 'Secret Event', now() + interval '5 days', now() + interval '6 days', 'published', now(), now());
		insert into event_crew(id, event_id, email, user_id, role, status, token, invited_by, expires_at, created_at, accepted_at) values
			('ec2', 'ev2', 'vol@example.com', 'vol', 'volunteer', 'accepted', 'tok-2', 'owner', now() + interval '7 days', now(), now());
	`)

	org1 := "org1"
	volCookie := e.Login(t, "vol", &org1)

	t.Run("lists both org1 member events and org2 crew events despite active_organization_id=org1", func(t *testing.T) {
		rec := e.Do(t, http.MethodGet, "/api/scanner/events", nil, volCookie)
		if rec.Code != 200 {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Events []eventOption `json:"events"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Events) != 2 {
			t.Fatalf("expected 2 events (ev1, ev2), got %d: %+v", len(out.Events), out.Events)
		}
	})

	t.Run("sessions allowed for ev2 and forbidden for unassigned ev3", func(t *testing.T) {
		rec2 := e.Do(t, http.MethodGet, "/api/scanner/sessions?eventId=ev2", nil, volCookie)
		if rec2.Code != 200 {
			t.Fatalf("ev2 sessions status=%d body=%s", rec2.Code, rec2.Body.String())
		}
		rec3 := e.Do(t, http.MethodGet, "/api/scanner/sessions?eventId=ev3", nil, volCookie)
		if rec3.Code != 403 {
			t.Fatalf("expected 403 for unassigned ev3, got %d", rec3.Code)
		}
	})

	t.Run("admin staff can access all published events across orgs, not draft, and unassigned regular user gets 0 events", func(t *testing.T) {
		e.SeedUser(t, "staff1", "staff")
		e.SeedUser(t, "stranger", "user")
		e.Exec(t, `insert into event(id, organization_id, name, starts_at, ends_at, status, created_at, updated_at) values
			('ev_draft', 'org3', 'Draft Event', now() + interval '5 days', now() + interval '6 days', 'draft', now(), now())`)

		staffCookie := e.Login(t, "staff1", nil)
		recStaff := e.Do(t, http.MethodGet, "/api/scanner/events", nil, staffCookie)
		if recStaff.Code != 200 {
			t.Fatalf("staff events status=%d body=%s", recStaff.Code, recStaff.Body.String())
		}
		var out struct {
			Events []eventOption `json:"events"`
		}
		_ = json.Unmarshal(recStaff.Body.Bytes(), &out)
		if len(out.Events) != 3 {
			t.Fatalf("expected 3 published events for staff, got %d: %+v", len(out.Events), out.Events)
		}

		// Staff can access published ev3 sessions, but not draft ev_draft
		if rec := e.Do(t, http.MethodGet, "/api/scanner/sessions?eventId=ev3", nil, staffCookie); rec.Code != 200 {
			t.Fatalf("expected 200 for staff on published ev3, got %d", rec.Code)
		}
		if rec := e.Do(t, http.MethodGet, "/api/scanner/sessions?eventId=ev_draft", nil, staffCookie); rec.Code != 403 {
			t.Fatalf("expected 403 for staff on draft ev_draft, got %d", rec.Code)
		}

		// Stranger gets 0 events and 403 on ev3
		strangerCookie := e.Login(t, "stranger", nil)
		recStr := e.Do(t, http.MethodGet, "/api/scanner/events", nil, strangerCookie)
		_ = json.Unmarshal(recStr.Body.Bytes(), &out)
		if len(out.Events) != 0 {
			t.Fatalf("expected 0 events for stranger, got %d", len(out.Events))
		}
		if rec := e.Do(t, http.MethodGet, "/api/scanner/sessions?eventId=ev3", nil, strangerCookie); rec.Code != 403 {
			t.Fatalf("expected 403 for stranger on ev3, got %d", rec.Code)
		}
	})
}
