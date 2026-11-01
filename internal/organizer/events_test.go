package organizer

import (
	"testing"
)

func TestListEvents(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/events", nil)
	if got := ok200(t, f.do(t, "GET", "/api/events", nil, f.member), ""); len(got["events"].([]any)) != 0 {
		t.Fatalf("want empty: %v", got)
	}
	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "gone", "org1")
	f.Exec(t, `update event set deleted_at = now() where id = 'gone'`)
	f.Exec(t, `update event set created_at = now() - interval '1 day' where id = 'e1'`)
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 10, 3, 0)
	f.ticket(t, "t2", "e1", 1000, 5, 0, 1)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 3, "now")
	f.order(t, "o2", "e1", "buyer", "expired", "t1", 1000, 4, "now")

	got := ok200(t, f.do(t, "GET", "/api/events", nil, f.member), "events.json")
	evs := got["events"].([]any)
	if len(evs) != 2 || evs[0].(map[string]any)["id"] != "e2" {
		t.Fatalf("events %v", evs)
	}
	e1 := evs[1].(map[string]any)
	if e1["soldCount"] != 3.0 || e1["capacity"] != 15.0 || e1["categories"].([]any)[0] != "musik" {
		t.Fatalf("e1 %v", e1)
	}
	if e2 := evs[0].(map[string]any); e2["soldCount"] != 0.0 || e2["capacity"] != 0.0 {
		t.Fatalf("e2 %v", e2)
	}
}

func TestCreateEvent(t *testing.T) {
	f := setup(t)
	authCases(t, f, "POST", "/api/events", map[string]any{})
	expect(t, f.do(t, "POST", "/api/events", map[string]any{}, f.member), 403, errBody("Forbidden"))

	rec := f.do(t, "POST", "/api/events", map[string]any{}, f.admin)
	expect(t, rec, 201, "")
	ev := decode(t, rec)["event"].(map[string]any)
	if ev["name"] != "Untitled Event" || ev["status"] != "draft" || ev["visibility"] != "public" ||
		ev["attendeeForm"] != false || len(ev["categories"].([]any)) != 0 || ev["organizationId"] != "org1" || ev["endsAt"] != nil {
		t.Fatalf("defaults %v", ev)
	}

	rec = f.do(t, "POST", "/api/events", map[string]any{
		"name": "Konser", "description": "<p>x</p>", "venueName": "V", "address": "A", "city": "Malang",
		"bannerUrl": "https://b", "startsAt": "2026-12-05T07:17:00.000Z", "endsAt": "2026-12-06T07:17:00.000Z",
		"visibility": "private", "attendeeForm": true, "categories": []any{"outdoor", "bogus", "musik", 3, "musik"},
	}, f.owner)
	expect(t, rec, 201, "")
	ev = decode(t, rec)["event"].(map[string]any)
	if ev["name"] != "Konser" || ev["startsAt"] != "2026-12-05T07:17:00.000Z" || ev["endsAt"] != "2026-12-06T07:17:00.000Z" ||
		ev["visibility"] != "private" || ev["attendeeForm"] != true || ev["city"] != "Malang" {
		t.Fatalf("event %v", ev)
	}
	assertShape(t, goldenPart(t, "events-{id}.json", "event"), ev)
	if c := ev["categories"].([]any); len(c) != 2 || c[0] != "musik" || c[1] != "outdoor" {
		t.Fatalf("categories %v", c)
	}
	expect(t, f.do(t, "POST", "/api/events", map[string]any{"startsAt": "nope"}, f.owner), 400, errBody("Invalid startsAt"))
}

func TestGetEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.event(t, "gone", "org1")
	f.Exec(t, `update event set deleted_at = now() where id = 'gone'`)
	authCases(t, f, "GET", "/api/events/e1", nil)
	expect(t, f.do(t, "GET", "/api/events/x1", nil, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "GET", "/api/events/gone", nil, f.owner), 404, errBody("Not found"))

	f.ticket(t, "t2", "e1", 500, 5, 0, 2)
	f.ticket(t, "t1", "e1", 1000, 10, 1, 1)
	f.Exec(t, `insert into performer(id,event_id,name,image_url,display_order,created_at,updated_at,deleted_at) values
		('p1','e1','P1','https://p',1,now(),now(),null), ('p0','e1','P0',null,0,now(),now(),null),
		('pd','e1','PD',null,0,now(),now(),now())`)
	f.Exec(t, `insert into voucher(id,event_id,code,type,amount,usage_limit,expires_at,created_at,deleted_at) values
		('v1','e1','HEMAT','percent',10,5,now(),now(),null), ('vd','e1','OLD','fixed',1000,null,null,now(),now())`)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 1, "now")
	f.order(t, "o2", "e1", "buyer", "pending_payment", "t1", 1000, 1, "now")

	got := ok200(t, f.do(t, "GET", "/api/events/e1", nil, f.member), "events-{id}.json")
	if tk := got["tickets"].([]any); len(tk) != 2 || tk[0].(map[string]any)["id"] != "t1" {
		t.Fatalf("tickets %v", tk)
	}
	if p := got["performers"].([]any); len(p) != 2 || p[0].(map[string]any)["id"] != "p0" {
		t.Fatalf("performers %v", p)
	}
	v := got["vouchers"].([]any)
	if len(v) != 1 {
		t.Fatalf("vouchers %v", v)
	}
	assertShape(t, voucherShape, v[0])
	if v0 := v[0].(map[string]any); v0["usageLimit"] != 5.0 || v0["expiresAt"] == nil || v0["deletedAt"] != nil {
		t.Fatalf("voucher %v", v0)
	}
	if got["paidOrderCount"] != 1.0 {
		t.Fatalf("paidOrderCount %v", got["paidOrderCount"])
	}
}

func TestUpdateEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	authCases(t, f, "PATCH", "/api/events/e1", map[string]any{})
	expect(t, f.do(t, "PATCH", "/api/events/e1", map[string]any{"name": "n"}, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "PATCH", "/api/events/x1", map[string]any{"name": "n"}, f.owner), 404, errBody("Not found"))
	if f.scalar(t, `select name from event where id='x1'`) != "Event x1" {
		t.Fatal("other org's event changed")
	}

	got := ok200(t, f.do(t, "PATCH", "/api/events/e1", map[string]any{
		"name": "New", "city": nil, "endsAt": "", "categories": []any{"edukasi"}, "attendeeForm": true,
		"startsAt": "2027-01-01T00:00:00Z",
	}, f.admin), "")
	ev := got["event"].(map[string]any)
	if ev["name"] != "New" || ev["city"] != nil || ev["endsAt"] != nil || ev["attendeeForm"] != true ||
		ev["startsAt"] != "2027-01-01T00:00:00.000Z" || ev["categories"].([]any)[0] != "edukasi" || ev["venueName"] != "Venue" {
		t.Fatalf("event %v", ev)
	}
	expect(t, f.do(t, "PATCH", "/api/events/e1", map[string]any{"name": nil}, f.admin), 400, errBody("Invalid name"))
	expect(t, f.do(t, "PATCH", "/api/events/e1", map[string]any{"startsAt": nil}, f.admin), 400, errBody("Invalid startsAt"))
	expect(t, f.do(t, "PATCH", "/api/events/e1", "{", f.admin), 400, errBody("Invalid JSON body"))

	f.Exec(t, `update event set deleted_at = now() where id = 'e1'`)
	expect(t, f.do(t, "PATCH", "/api/events/e1", map[string]any{"name": "n"}, f.owner), 404, errBody("Not found"))
}

func TestDeleteEvent(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t2", "e2", 1000, 10, 1, 0)
	f.order(t, "o1", "e2", "buyer", "paid", "t2", 1000, 1, "now")
	authCases(t, f, "DELETE", "/api/events/e1", nil)
	expect(t, f.do(t, "DELETE", "/api/events/e1", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "DELETE", "/api/events/x1", nil, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "DELETE", "/api/events/e2", nil, f.owner), 409, errBody("Cannot delete an event that has sold tickets"))
	expect(t, f.do(t, "DELETE", "/api/events/e1", nil, f.admin), 200, `{"ok":true}`)
	if f.scalar(t, `select deleted_at is not null from event where id='e1'`) != true {
		t.Fatal("not soft deleted")
	}
	expect(t, f.do(t, "DELETE", "/api/events/e1", nil, f.admin), 404, errBody("Not found"))
}
