package web

import (
	"testing"
)

func TestConfig(t *testing.T) {
	env, _ := newEnv(t)
	env.Exec(t, `insert into site_setting(key,value,updated_at) values('tax_percent','7',now()),('whatsapp_number','62811',now())`)
	rec := env.Do(t, "GET", "/api/config", nil, nil)
	assertGolden(t, rec, "config.json")
	got := decode[map[string]any](t, rec)
	if got["taxPercent"] != 7.0 || got["whatsappNumber"] != "62811" || got["serviceFee"] != 7120.0 {
		t.Fatalf("got %v", got)
	}
}

func TestFavorites(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values
		('e2','o1','Dua','published',now()+interval '3 days',now(),now()),
		('draft','o1','Draft','draft',now()+interval '3 days',now(),now()),
		('gone','o1','Gone','published',now()+interval '3 days',now(),now())`)
	u1 := env.Login(t, "u1", nil)
	u2 := env.Login(t, "u2", nil)

	for _, p := range []struct{ m, path string }{{"GET", "/api/favorites"}, {"POST", "/api/favorites"}, {"DELETE", "/api/favorites/e1"}} {
		assertError(t, env.Do(t, p.m, p.path, map[string]string{"eventId": "e1"}, nil), 401, "Unauthorized")
	}
	if rec := env.Do(t, "GET", "/api/favorites/ids", nil, nil); rec.Code != 200 || rec.Body.String() != "[]\n" {
		t.Fatalf("anon ids: %d %s", rec.Code, rec.Body)
	}

	assertError(t, env.Do(t, "POST", "/api/favorites", map[string]string{}, u1), 400, "Missing eventId")
	assertError(t, env.Do(t, "POST", "/api/favorites", map[string]string{"eventId": "nope"}, u1), 404, "Not found")
	assertError(t, env.Do(t, "POST", "/api/favorites", map[string]string{"eventId": "draft"}, u1), 404, "Not found")
	for _, id := range []string{"e1", "e1", "e2", "gone"} {
		if rec := env.Do(t, "POST", "/api/favorites", map[string]string{"eventId": id}, u1); rec.Code != 200 || rec.Body.String() != "{\"ok\":true}\n" {
			t.Fatalf("add %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	env.Exec(t, `update event set deleted_at=now() where id='gone'`)
	env.Exec(t, `update favorite set created_at=now()-interval '1 hour' where event_id='e1'`)
	if n := one[int](t, env, `select count(*) from favorite where user_id='u1'`); n != 3 {
		t.Fatalf("favorites=%d", n)
	}

	rec := env.Do(t, "GET", "/api/favorites?limit=1", nil, u1)
	assertGolden(t, rec, "favorites.json")
	type page struct {
		Favorites []struct {
			EventID string `json:"eventId"`
		} `json:"favorites"`
		NextOffset *int `json:"nextOffset"`
	}
	p := decode[page](t, rec)
	if len(p.Favorites) != 1 || p.Favorites[0].EventID != "e2" || p.NextOffset == nil || *p.NextOffset != 1 {
		t.Fatalf("page1 %s", rec.Body)
	}
	p = decode[page](t, env.Do(t, "GET", "/api/favorites?limit=1&offset=1", nil, u1))
	if len(p.Favorites) != 1 || p.Favorites[0].EventID != "e1" || p.NextOffset != nil {
		t.Fatalf("page2 %+v", p)
	}
	if rec := env.Do(t, "GET", "/api/favorites", nil, u2); rec.Body.String() != "{\"favorites\":[],\"nextOffset\":null}\n" {
		t.Fatalf("u2: %s", rec.Body)
	}

	rec = env.Do(t, "GET", "/api/favorites/ids", nil, u1)
	assertGolden(t, rec, "favorites-ids.json")
	if ids := decode[[]string](t, rec); len(ids) != 3 {
		t.Fatalf("ids %v", ids)
	}

	env.Do(t, "DELETE", "/api/favorites/e1", nil, u2)
	if rec := env.Do(t, "DELETE", "/api/favorites/e1", nil, u1); rec.Code != 200 || rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if n := one[int](t, env, `select count(*) from favorite where user_id='u1' and event_id='e1'`); n != 0 {
		t.Fatal("not deleted")
	}
}
