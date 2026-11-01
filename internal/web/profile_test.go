package web

import (
	"testing"
)

func TestProfile(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values('e2','o1','Dua','published',now()+interval '1 day',now(),now())`)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	seedOrder(t, env, "a2", "u1", "e1", "paid", 1)
	seedOrder(t, env, "a3", "u1", "e2", "paid", 1)
	env.Exec(t, `insert into ticket(id,order_id,order_item_id,event_id,ticket_type_id,ticket_number,barcode,status,created_at) values
		('t1','a1','a1-i','e1','tt1','N1','B1','valid',now()),('t2','a1','a1-i','e1','tt1','N2','B2','valid',now()),
		('t3','a2','a2-i','e1','tt1','N3','B3','valid',now()),('t4','a3','a3-i','e2','tt1','N4','B4','valid',now())`)
	env.Exec(t, `insert into wall_comment(id,event_id,user_id,content,created_at) values('c1','e1','u1','x',now()),('c2','e1','u2','y',now())`)
	env.Exec(t, `update "user" set created_at='2024-12-31 23:00:00' where id='u1'`)
	u1 := env.Login(t, "u1", nil)

	assertError(t, env.Do(t, "GET", "/api/profile", nil, nil), 401, "Unauthorized")
	assertError(t, env.Do(t, "PATCH", "/api/profile", map[string]string{"name": "x"}, nil), 401, "Unauthorized")

	rec := env.Do(t, "GET", "/api/profile", nil, u1)
	assertGolden(t, rec, "profile.json")
	got := decode[map[string]any](t, rec)
	if got["memberSince"] != 2024.0 || got["eventCount"] != 2.0 || got["commentCount"] != 1.0 || got["gender"] != "male" || got["coverImage"] != nil {
		t.Fatalf("got %s", rec.Body)
	}
	if _, ok := got["createdAt"]; ok {
		t.Fatal("createdAt leaked")
	}

	assertError(t, env.Do(t, "PATCH", "/api/profile", map[string]any{}, u1), 400, "No fields to update")
	assertError(t, env.Do(t, "PATCH", "/api/profile", map[string]any{"name": "  ", "role": "super_admin"}, u1), 400, "No fields to update")
	assertError(t, env.Do(t, "PATCH", "/api/profile", map[string]any{"location": 5}, u1), 400, "Invalid location")
	assertError(t, env.Do(t, "PATCH", "/api/profile", "null", u1), 400, "Invalid body")
	rec = env.Do(t, "PATCH", "/api/profile", map[string]any{"name": "Budi", "gender": "", "instagram": nil, "tiktok": "tt", "role": "super_admin"}, u1)
	if rec.Code != 200 || rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("patch %d %s", rec.Code, rec.Body)
	}
	var name, role string
	var gender, ig, tt, img *string
	if err := env.DB.QueryRow(t.Context(), `select name, role, gender, instagram, tiktok, image from "user" where id='u1'`).Scan(&name, &role, &gender, &ig, &tt, &img); err != nil {
		t.Fatal(err)
	}
	if name != "Budi" || role != "user" || gender != nil || ig != nil || tt == nil || *tt != "tt" || img == nil {
		t.Fatalf("row %s %s %v %v %v %v", name, role, gender, ig, tt, img)
	}
	if n := one[int](t, env, `select count(*) from "user" where id='u2' and name='u2'`); n != 1 {
		t.Fatal("u2 touched")
	}
}

func TestProfileActivity(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,ends_at,created_at,updated_at) values('past','o1','Lalu','published',now()-interval '3 days',now()-interval '2 days',now(),now())`)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	seedOrder(t, env, "s1", "u1", "past", "paid", 1)
	seedOrder(t, env, "pend", "u1", "e1", "pending_payment", 1)
	seedOrder(t, env, "other", "u2", "e1", "paid", 1)
	u1 := env.Login(t, "u1", nil)
	assertError(t, env.Do(t, "GET", "/api/profile/activity", nil, nil), 401, "Unauthorized")

	rec := env.Do(t, "GET", "/api/profile/activity", nil, u1)
	assertGolden(t, rec, "profile-activity.json")
	type page struct {
		Activity   []map[string]any `json:"activity"`
		NextOffset *int             `json:"nextOffset"`
	}
	p := decode[page](t, rec)
	if len(p.Activity) != 2 || p.Activity[0]["orderId"] != "a1" || p.Activity[0]["ticketCount"] != 2.0 || p.Activity[1]["orderId"] != "s1" || p.NextOffset != nil {
		t.Fatalf("got %s", rec.Body)
	}
	if _, ok := p.Activity[0]["referenceNumber"]; ok {
		t.Fatal("referenceNumber in activity")
	}
	p = decode[page](t, env.Do(t, "GET", "/api/profile/activity?limit=1", nil, u1))
	if len(p.Activity) != 1 || p.NextOffset == nil || *p.NextOffset != 1 {
		t.Fatalf("page %+v", p)
	}
	if rec := env.Do(t, "GET", "/api/profile/activity", nil, env.Login(t, "u2", nil)); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
}

func TestProfileTicket(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	u1 := env.Login(t, "u1", nil)
	assertError(t, env.Do(t, "GET", "/api/profile/ticket/a1", nil, nil), 401, "Unauthorized")
	assertError(t, env.Do(t, "GET", "/api/profile/ticket/a1", nil, env.Login(t, "u2", nil)), 404, "Not found")

	rec := env.Do(t, "GET", "/api/profile/ticket/a1", nil, u1)
	assertGolden(t, rec, "profile-ticket-{orderId}.json")
	if got := decode[map[string]any](t, rec); got["comment"] != nil || got["barcode"] != "BC-a1" {
		t.Fatalf("got %s", rec.Body)
	}

	env.Exec(t, `insert into wall_comment(id,event_id,user_id,content,created_at) values
		('c1','e1','u1','lama',now()-interval '1 day'),('c2','e1','u1','baru',now()),('c3','e1','u2','orang lain',now()+interval '1 hour')`)
	got := decode[map[string]any](t, env.Do(t, "GET", "/api/profile/ticket/a1", nil, u1))
	c, _ := got["comment"].(map[string]any)
	if c == nil || c["content"] != "baru" || c["createdAt"] == nil {
		t.Fatalf("comment %v", got["comment"])
	}
}
