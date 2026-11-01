package admin

import (
	"fmt"
	"testing"
)

func TestListOrgs(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/orgs", nil)
	statuses := []string{"suspended", "active", "pending", "rejected", "review"}
	for i := range 25 {
		id := fmt.Sprintf("o%02d", i)
		e.seedOrg(t, id, "Org "+id, statuses[i%5])
		e.Exec(t, `update organization set created_at = now() - make_interval(mins => $1) where id = $2`, i, id)
	}
	rec := e.do(t, "GET", "/api/orgs", nil, e.staff)
	assertGolden(t, "orgs.json", rec)
	p := decode[page[org]](t, rec)
	if p.Total != 25 || p.TotalPages != 2 || len(p.Data) != 20 || p.Data[0].ID != "o22" || p.Data[4].ID != "o02" || p.Data[5].Status != "review" {
		t.Fatalf("orgs %+v", p)
	}
	p = decode[page[org]](t, e.do(t, "GET", "/api/orgs?page=2", nil, e.staff))
	if len(p.Data) != 5 || p.Data[0].Status != "suspended" {
		t.Fatalf("page 2 %+v", p)
	}
	p = decode[page[org]](t, e.do(t, "GET", "/api/orgs?status=rejected", nil, e.staff))
	if p.Total != 5 || len(p.Data) != 5 || p.TotalPages != 1 {
		t.Fatalf("filtered %+v", p)
	}
	want(t, e.do(t, "GET", "/api/orgs?status=nope", nil, e.staff), 400, `{"error":"Invalid status"}`)
}

func TestGetOrg(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/orgs/o1", nil)
	e.seedOrg(t, "o1", "Org", "active")
	e.Exec(t, `update organization set metadata = '{"type":"personal"}', verification_data = '{"ktpNumber":"1"}' where id = 'o1'`)
	e.seedMember(t, "o1", "usr", "owner")
	e.seedMember(t, "o1", "stf", "admin")
	e.Exec(t, `insert into organization_status_history(id,organization_id,from_status,to_status,actor_id,actor_type,created_at) values
		('h1','o1',null,'pending','usr','organizer',now()-interval '2 hours'),
		('h2','o1','pending','active','adm','admin',now()-interval '1 hour'),
		('h3','o1','active','active',null,'system',now())`)

	rec := e.do(t, "GET", "/api/orgs/o1", nil, e.staff)
	assertGolden(t, "orgs-{id}.json", rec)
	got := decode[struct {
		org
		Members []orgMember
		History []statusHistory
	}](t, rec)
	if got.ID != "o1" || *got.Metadata != `{"type":"personal"}` || len(got.Members) != 2 || got.Members[0].Email != "usr@example.com" {
		t.Fatalf("org %+v", got)
	}
	if len(got.History) != 3 || got.History[0].ID != "h3" || got.History[0].ActorName != nil || *got.History[1].ActorEmail != "adm@example.com" {
		t.Fatalf("history %+v", got.History)
	}
	want(t, e.do(t, "GET", "/api/orgs/nope", nil, e.staff), 404, `{"error":"Not found"}`)
}

func TestPatchOrg(t *testing.T) {
	e := setup(t)
	body := map[string]any{"status": "active"}
	e.guarded(t, "PATCH", "/api/orgs/o1", body, e.staff)
	e.seedOrg(t, "o1", "Org", "review")

	type state struct {
		Status        string
		Note          *string
		AllowResubmit bool
	}
	get := func() state {
		var s state
		if err := e.DB.QueryRow(t.Context(), `select status, status_note, allow_resubmit from organization where id='o1'`).Scan(&s.Status, &s.Note, &s.AllowResubmit); err != nil {
			t.Fatal(err)
		}
		return s
	}
	history := func() int {
		var n int
		e.scalar(t, &n, `select count(*) from organization_status_history`)
		return n
	}

	want(t, e.do(t, "PATCH", "/api/orgs/o1", map[string]any{"status": "rejected", "statusNote": "KTP blur", "allowResubmit": true}, e.admin), 200, `{"ok":true}`)
	if s := get(); s.Status != "rejected" || *s.Note != "KTP blur" || !s.AllowResubmit {
		t.Fatalf("%+v", s)
	}
	var from, to, actor, actorType, note string
	if err := e.DB.QueryRow(t.Context(), `select from_status, to_status, actor_id, actor_type, note from organization_status_history`).Scan(&from, &to, &actor, &actorType, &note); err != nil {
		t.Fatal(err)
	}
	if from != "review" || to != "rejected" || actor != "adm" || actorType != "admin" || note != "KTP blur" {
		t.Fatalf("history %s %s %s %s %s", from, to, actor, actorType, note)
	}

	want(t, e.do(t, "PATCH", "/api/orgs/o1", map[string]any{"status": "rejected"}, e.super), 200, `{"ok":true}`)
	if s := get(); s.Note != nil || s.AllowResubmit || history() != 1 {
		t.Fatalf("%+v %d", s, history())
	}
	want(t, e.do(t, "PATCH", "/api/orgs/o1", map[string]any{"status": "active", "allowResubmit": true}, e.super), 200, `{"ok":true}`)
	if s := get(); s.Status != "active" || s.AllowResubmit || history() != 2 {
		t.Fatalf("%+v %d", s, history())
	}

	want(t, e.do(t, "PATCH", "/api/orgs/nope", body, e.admin), 404, `{"error":"Not found"}`)
	want(t, e.do(t, "PATCH", "/api/orgs/o1", map[string]any{"status": "banned"}, e.admin), 400, `{"error":"Invalid status"}`)
	want(t, e.do(t, "PATCH", "/api/orgs/o1", map[string]any{}, e.admin), 400, `{"error":"Invalid status"}`)
	want(t, e.do(t, "PATCH", "/api/orgs/o1", "nope", e.admin), 400, `{"error":"Invalid request body"}`)
	if get().Status != "active" {
		t.Fatal("status changed by rejected request")
	}
}
