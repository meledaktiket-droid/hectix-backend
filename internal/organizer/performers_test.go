package organizer

import (
	"testing"
)

func TestPerformers(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.Exec(t, `insert into performer(id,event_id,name,image_url,display_order,created_at,updated_at,deleted_at) values
		('p1','e1','P1','https://p',1,now(),now(),null), ('p0','e1','P0',null,0,now(),now(),null),
		('pd','e1','PD',null,0,now(),now(),now()), ('px','x1','PX',null,0,now(),now(),null)`)

	authCases(t, f, "GET", "/api/events/e1/performers", nil)
	expect(t, f.do(t, "GET", "/api/events/x1/performers", nil, f.owner), 404, errBody("Not found"))
	got := ok200(t, f.do(t, "GET", "/api/events/e1/performers", nil, f.member), "events-{id}-performers.json")
	if p := got["performers"].([]any); len(p) != 2 || p[0].(map[string]any)["id"] != "p0" {
		t.Fatalf("performers %v", p)
	}

	authCases(t, f, "POST", "/api/events/e1/performers", map[string]any{"name": "x"})
	expect(t, f.do(t, "POST", "/api/events/x1/performers", map[string]any{"name": "x"}, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "POST", "/api/events/e1/performers", map[string]any{"name": "x"}, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/events/e1/performers", map[string]any{"name": "  "}, f.owner), 400, errBody("Name is required"))
	rec := f.do(t, "POST", "/api/events/e1/performers", map[string]any{"name": " Band ", "imageUrl": " "}, f.admin)
	expect(t, rec, 201, "")
	p := decode(t, rec)["performer"].(map[string]any)
	assertShape(t, goldenPart(t, "events-{id}-performers.json", "performers", 0), p)
	if p["name"] != "Band" || p["imageUrl"] != nil || p["displayOrder"] != 3.0 {
		t.Fatalf("performer %v", p)
	}

	authCases(t, f, "DELETE", "/api/events/e1/performers/p1", nil)
	expect(t, f.do(t, "DELETE", "/api/events/e1/performers/p1", nil, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "DELETE", "/api/events/x1/performers/px", nil, f.owner), 404, errBody("Not found"))
	expect(t, f.do(t, "DELETE", "/api/events/e1/performers/px", nil, f.owner), 200, `{"ok":true}`)
	if f.scalar(t, `select deleted_at is null from performer where id='px'`) != true {
		t.Fatal("other org's performer deleted")
	}
	expect(t, f.do(t, "DELETE", "/api/events/e1/performers/p1", nil, f.admin), 200, `{"ok":true}`)
	if f.scalar(t, `select deleted_at is not null from performer where id='p1'`) != true {
		t.Fatal("not soft deleted")
	}
}
