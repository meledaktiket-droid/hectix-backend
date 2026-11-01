package organizer

import (
	"testing"
)

func TestPublishEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.Exec(t, `insert into event(id,organization_id,name,starts_at,created_at,updated_at) values('bare','org1','',now(),now(),now())`)
	authCases(t, f, "POST", "/api/events/e1/publish", nil)
	expect(t, f.do(t, "POST", "/api/events/e1/publish", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/events/x1/publish", nil, f.owner), 404, errBody("Not found"))

	expect(t, f.do(t, "POST", "/api/events/bare/publish", nil, f.owner), 422,
		`{"error":"Cannot publish","details":["Event name is required","End date is required","Venue name is required","Address is required","City is required","At least one category is required","At least one ticket type is required"]}`)
	expect(t, f.do(t, "POST", "/api/events/e1/publish", nil, f.owner), 422,
		`{"error":"Cannot publish","details":["At least one ticket type is required"]}`)

	f.ticket(t, "t1", "e1", 1000, 10, 0, 0)
	for _, st := range []string{"pending", "review", "rejected", "suspended"} {
		f.Exec(t, `update organization set status = $1 where id = 'org1'`, st)
		expect(t, f.do(t, "POST", "/api/events/e1/publish", nil, f.owner), 403, errBody(msgOrgNotVerified))
	}
	f.Exec(t, `update organization set status = 'active' where id = 'org1'`)
	got := ok200(t, f.do(t, "POST", "/api/events/e1/publish", nil, f.admin), "")
	assertShape(t, goldenPart(t, "events-{id}.json", "event"), got["event"])
	if got["event"].(map[string]any)["status"] != "published" {
		t.Fatalf("event %v", got)
	}
	f.Exec(t, `update event set deleted_at = now() where id = 'e1'`)
	expect(t, f.do(t, "POST", "/api/events/e1/publish", nil, f.owner), 404, errBody("Not found"))
}

func TestUnpublishEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "x1", "org2")
	f.Exec(t, `update event set status = 'published'`)
	f.ticket(t, "t2", "e2", 1000, 10, 1, 0)
	f.order(t, "o1", "e2", "buyer", "paid", "t2", 1000, 1, "now")
	authCases(t, f, "POST", "/api/events/e1/unpublish", nil)
	expect(t, f.do(t, "POST", "/api/events/e1/unpublish", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/events/x1/unpublish", nil, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "POST", "/api/events/e2/unpublish", nil, f.owner), 409, errBody("Cannot unpublish an event that has sold tickets"))
	got := ok200(t, f.do(t, "POST", "/api/events/e1/unpublish", nil, f.owner), "")
	if got["event"].(map[string]any)["status"] != "draft" {
		t.Fatalf("event %v", got)
	}
}

func TestDuplicateEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.Exec(t, `update event set status = 'published'`)
	f.ticket(t, "t1", "e1", 1000, 10, 4, 0)
	f.Exec(t, `insert into performer(id,event_id,name,display_order,created_at,updated_at,deleted_at) values
		('p1','e1','P1',1,now(),now(),null), ('pd','e1','PD',0,now(),now(),now())`)
	f.Exec(t, `insert into voucher(id,event_id,code,type,amount,usage_limit,usage_count,created_at,deleted_at) values
		('v1','e1','HEMAT','percent',10,5,3,now(),null), ('vd','e1','OLD','fixed',1000,null,0,now(),now())`)
	authCases(t, f, "POST", "/api/events/e1/duplicate", nil)
	expect(t, f.do(t, "POST", "/api/events/e1/duplicate", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/events/x1/duplicate", nil, f.owner), 404, errBody("Not found"))

	rec := f.do(t, "POST", "/api/events/e1/duplicate", nil, f.admin)
	expect(t, rec, 201, "")
	id := decode(t, rec)["id"].(string)
	if f.scalar(t, `select name || '|' || status || '|' || (ends_at is null)::text || '|' || organization_id || '|' || categories::text
		from event where id = $1`, id) != `Copy of Event e1|draft|true|org1|["musik"]` {
		t.Fatal("event copy wrong")
	}
	if f.scalar(t, `select string_agg(name || ':' || quantity || ':' || sold || ':' || benefits::text, ',') from ticket_type where event_id = $1`, id) != `Ticket t1:10:0:["a"]` {
		t.Fatal("tickets copy wrong")
	}
	if f.scalar(t, `select string_agg(name, ',') from performer where event_id = $1`, id) != "P1" {
		t.Fatal("performers copy wrong")
	}
	if f.scalar(t, `select string_agg(code || ':' || usage_count || ':' || usage_limit, ',') from voucher where event_id = $1`, id) != "HEMAT:0:5" {
		t.Fatal("vouchers copy wrong")
	}
}
