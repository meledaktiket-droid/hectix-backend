package web

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func keysOf(t *testing.T, body []byte) [][]string {
	t.Helper()
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(body, &arr); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	var out [][]string
	for _, o := range arr {
		var ks []string
		for k := range o {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		out = append(out, ks)
	}
	return out
}

func TestPublishedEvents(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.SeedUser(t, "u3", "user")
	env.SeedUser(t, "u4", "user")
	env.SeedUser(t, "u5", "user")
	env.Exec(t, `update "user" set image='https://img/'||id||'.jpg' where id in ('u3','u4','u5')`)
	env.Exec(t, `insert into event(id,organization_id,name,venue_name,city,status,visibility,starts_at,ends_at,categories,deleted_at,created_at,updated_at) values
		('musik','o1','Jazz Malam','Hall_A','Medan','published','public',now()+interval '1 day',null,'["musik","outdoor"]',null,now(),now()),
		('ongoing','o1','Pameran','Galeri','Jakarta','published','public',now()-interval '1 day',now()+interval '1 day','["seni-budaya"]',null,now(),now()),
		('past','o1','Lalu','X','Jakarta','published','public',now()-interval '3 days',now()-interval '2 days','["musik"]',null,now(),now()),
		('priv','o1','Privat','X','Jakarta','published','private',now()+interval '1 day',null,'["musik"]',null,now(),now()),
		('draft','o1','Draft','X','Jakarta','draft','public',now()+interval '1 day',null,'["musik"]',null,now(),now()),
		('del','o1','Del','X','Jakarta','published','public',now()+interval '1 day',null,'["musik"]',now(),now(),now())`)
	env.Exec(t, `insert into wall_comment(id,event_id,user_id,content,created_at) values
		('c1','e1','u2','a',now()-interval '1 minute'),('c2','e1','u1','b',now()-interval '2 minute'),
		('c3','e1','u1','c',now()-interval '3 minute'),('c4','e1','u3','d',now()-interval '4 minute'),
		('c5','e1','u4','e',now()-interval '5 minute'),('c6','e1','u5','f',now()-interval '6 minute'),
		('c7','musik','u5','g',now())`)

	rec := env.Do(t, "GET", "/api/events/published", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	want := []string{"bannerUrl", "city", "commentCount", "commenterAvatars", "id", "minPrice", "name", "organizationName", "startsAt", "venueName"}
	for _, ks := range keysOf(t, rec.Body.Bytes()) {
		if !reflect.DeepEqual(ks, want) {
			t.Fatalf("keys %v", ks)
		}
	}
	type item struct {
		ID               string   `json:"id"`
		StartsAt         string   `json:"startsAt"`
		MinPrice         int      `json:"minPrice"`
		CommentCount     int      `json:"commentCount"`
		CommenterAvatars []string `json:"commenterAvatars"`
	}
	got := decode[[]item](t, rec)
	ids := func(items []item) []string {
		var out []string
		for _, i := range items {
			out = append(out, i.ID)
		}
		return out
	}
	if !reflect.DeepEqual(ids(got), []string{"ongoing", "musik", "e1"}) {
		t.Fatalf("ids %v", ids(got))
	}
	e1 := got[2]
	if e1.MinPrice != 100000 || e1.CommentCount != 6 || !reflect.DeepEqual(e1.CommenterAvatars, []string{"https://img/u1.jpg", "https://img/u3.jpg", "https://img/u4.jpg"}) {
		t.Fatalf("e1 %+v", e1)
	}
	if got[0].CommentCount != 0 || got[0].CommenterAvatars == nil || got[0].MinPrice != 0 || len(got[0].StartsAt) != len("2026-10-02T10:17:14.264Z") {
		t.Fatalf("ongoing %+v", got[0])
	}
	if got[1].CommentCount != 1 || len(got[1].CommenterAvatars) != 1 {
		t.Fatalf("musik %+v", got[1])
	}

	for query, wantIDs := range map[string][]string{
		"?category=musik":              {"musik"},
		"?category=outdoor&q=jazz":     {"musik"},
		"?category=music":              {"ongoing", "musik", "e1"},
		"?q=%20medan%20":               {"musik"},
		"?q=org%20satu":                {"ongoing", "musik", "e1"},
		"?q=galeri":                    {"ongoing"},
		"?q=l_a":                       {"musik"},
		"?q=%25":                       nil,
		"?q=lalu":                      nil,
		"?category=seni-budaya&q=jazz": nil,
	} {
		got := decode[[]item](t, env.Do(t, "GET", "/api/events/published"+query, nil, nil))
		if !reflect.DeepEqual(ids(got), wantIDs) {
			t.Errorf("%s: %v want %v", query, ids(got), wantIDs)
		}
	}
	if rec := env.Do(t, "GET", "/api/events/published?q=zzz", nil, nil); rec.Body.String() != "[]\n" {
		t.Fatalf("empty: %s", rec.Body)
	}
}

func TestActiveBanners(t *testing.T) {
	env, _ := newEnv(t)
	if rec := env.Do(t, "GET", "/api/banners", nil, nil); rec.Code != 200 || rec.Body.String() != "[]\n" {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	env.Exec(t, `insert into banner(id,tag,description,image_url,display_order,is_active,deleted_at,created_at,updated_at) values
		('b2','Promo','Dua','https://img/b2.jpg',1,true,null,now()-interval '1 day',now()),
		('b3','Promo','Tiga','https://img/b3.jpg',1,true,null,now(),now()),
		('b1','Baru','Satu','https://img/b1.jpg',0,true,null,now(),now()),
		('off','x','x','x',0,false,null,now(),now()),
		('gone','x','x','x',0,true,now(),now(),now())`)
	rec := env.Do(t, "GET", "/api/banners", nil, nil)
	got := decode[[]homeBanner](t, rec)
	if len(got) != 3 || got[0].ID != "b1" || got[1].ID != "b2" || got[2].ID != "b3" || got[0].ImageURL != "https://img/b1.jpg" || got[0].Tag != "Baru" || got[0].Description != "Satu" {
		t.Fatalf("got %s", rec.Body)
	}
	for _, ks := range keysOf(t, rec.Body.Bytes()) {
		if !reflect.DeepEqual(ks, []string{"description", "id", "imageUrl", "tag"}) {
			t.Fatalf("keys %v", ks)
		}
	}
}
