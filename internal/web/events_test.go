package web

import (
	"testing"
)

func TestListEvents(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,visibility,starts_at,deleted_at,created_at,updated_at) values
		('early','o1','Awal','published','public',now()-interval '30 days',null,now(),now()),
		('priv','o1','Privat','published','private',now()+interval '1 day',null,now(),now()),
		('draft','o1','Draft','draft','public',now()+interval '1 day',null,now(),now()),
		('del','o1','Del','published','public',now()+interval '1 day',now(),now(),now())`)
	rec := env.Do(t, "GET", "/api/events", nil, nil)
	assertGolden(t, rec, "events.json")
	got := decode[[]eventListItem](t, rec)
	if len(got) != 2 || got[0].ID != "early" || got[0].MinPrice != 0 || got[1].ID != "e1" || got[1].MinPrice != 100000 || got[1].OrganizationName != "Org Satu" {
		t.Fatalf("got %s", rec.Body)
	}
}

func TestGetEvent(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.SeedUser(t, "u3", "user")
	seedOrder(t, env, "ord1", "u1", "e1", "paid", 1)
	env.Exec(t, `insert into wall_comment(id,event_id,user_id,content,created_at) values
		('c1','e1','u1','a',now()-interval '1 hour'),('c2','e1','u2','b',now()-interval '2 hour'),
		('c3','e1','u1','c',now()-interval '3 hour'),('c4','e1','u3','d',now()-interval '4 hour')`)
	env.Exec(t, `update "user" set image='https://img/u3.jpg' where id='u3'`)
	env.Exec(t, `insert into performer(id,event_id,name,display_order,deleted_at,created_at,updated_at) values('p2','e1','Gone',1,now(),now(),now())`)

	rec := env.Do(t, "GET", "/api/events/e1", nil, env.Login(t, "u1", nil))
	assertGolden(t, rec, "events-{id}.json")
	type resp struct {
		Tickets []struct {
			ID        string
			Benefits  any
			Remaining int
		}
		Performers       []eventPerformer
		CommentCount     int
		CommenterAvatars []string
		CanComment       bool
		EndsAt           *string
	}
	got := decode[resp](t, rec)
	if len(got.Tickets) != 2 || got.Tickets[0].ID != "tt2" || got.Tickets[0].Remaining != 0 || got.Tickets[0].Benefits != nil ||
		got.Tickets[1].Remaining != 8 || len(got.Performers) != 1 || got.CommentCount != 4 || !got.CanComment || got.EndsAt == nil {
		t.Fatalf("got %s", rec.Body)
	}
	if len(got.CommenterAvatars) != 2 || got.CommenterAvatars[0] != "https://img/u1.jpg" || got.CommenterAvatars[1] != "https://img/u3.jpg" {
		t.Fatalf("avatars %v", got.CommenterAvatars)
	}

	if got := decode[resp](t, env.Do(t, "GET", "/api/events/e1", nil, nil)); got.CanComment {
		t.Fatal("anon canComment")
	}
	if got := decode[resp](t, env.Do(t, "GET", "/api/events/e1", nil, env.Login(t, "u2", nil))); got.CanComment {
		t.Fatal("u2 canComment")
	}

	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,deleted_at,created_at,updated_at) values
		('draft','o1','D','draft',now(),null,now(),now()),('del','o1','D','published',now(),now(),now(),now())`)
	for _, id := range []string{"nope", "draft", "del"} {
		assertError(t, env.Do(t, "GET", "/api/events/"+id, nil, nil), 404, "Not found")
	}
}

func TestSearch(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,venue_name,status,visibility,starts_at,ends_at,deleted_at,created_at,updated_at) values
		('past','o1','Konser Lalu',null,'published','public',now()-interval '2 days',now()-interval '1 day',null,now(),now()),
		('ongoing','o1','Konser Jalan',null,'published','public',now()-interval '2 days',now()+interval '1 day',null,now(),now()),
		('priv','o1','Konser Privat',null,'published','private',now()+interval '1 day',null,null,now(),now()),
		('pct','o1','Diskon 100% Promo','Hall_A','published','public',now()+interval '1 day',null,null,now(),now())`)

	rec := env.Do(t, "GET", "/api/search?q=konser", nil, nil)
	assertGolden(t, rec, "search.json")
	got := decode[[]eventSuggestion](t, rec)
	if len(got) != 2 || got[0].ID != "ongoing" || got[1].ID != "e1" {
		t.Fatalf("got %s", rec.Body)
	}
	for q, want := range map[string]string{"org%20satu": "ongoing", "lampung": "e1", "100%25": "pct", "l_a": "pct"} {
		got := decode[[]eventSuggestion](t, env.Do(t, "GET", "/api/search?q="+q, nil, nil))
		if len(got) == 0 || got[0].ID != want {
			t.Errorf("q=%s: got %+v", q, got)
		}
	}
	if got := decode[[]eventSuggestion](t, env.Do(t, "GET", "/api/search?q=%25_", nil, nil)); len(got) != 0 {
		t.Errorf("wildcards matched: %+v", got)
	}
	for _, q := range []string{"", "k", "%20k%20"} {
		if rec := env.Do(t, "GET", "/api/search?q="+q, nil, nil); rec.Body.String() != "[]\n" {
			t.Errorf("q=%q: %s", q, rec.Body)
		}
	}
	for i := range 8 {
		env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values($1,'o1','Zeta','published',now()+interval '9 days',now(),now())`, "z"+string(rune('a'+i)))
	}
	if got := decode[[]eventSuggestion](t, env.Do(t, "GET", "/api/search?q=zeta", nil, nil)); len(got) != 6 {
		t.Errorf("limit: %d", len(got))
	}
}
