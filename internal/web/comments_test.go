package web

import (
	"strings"
	"testing"
)

func TestComments(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	seedOrder(t, env, "ord1", "u1", "e1", "paid", 1)
	seedOrder(t, env, "ord2", "u2", "e1", "pending_payment", 1)
	u1, u2 := env.Login(t, "u1", nil), env.Login(t, "u2", nil)

	if rec := env.Do(t, "GET", "/api/events/e1/comments", nil, nil); rec.Body.String() != "{\"comments\":[]}\n" {
		t.Fatalf("empty: %s", rec.Body)
	}

	assertError(t, env.Do(t, "POST", "/api/events/e1/comments", map[string]string{"content": "hi"}, nil), 401, "Unauthorized")
	for _, c := range []any{map[string]string{}, map[string]string{"content": "   "}, map[string]string{"content": strings.Repeat("a", 151)}, map[string]int{"content": 5}} {
		assertError(t, env.Do(t, "POST", "/api/events/e1/comments", c, u1), 400, "Invalid content")
	}
	assertError(t, env.Do(t, "POST", "/api/events/e1/comments", map[string]string{"content": "hi"}, u2), 403, "Forbidden")

	if rec := env.Do(t, "POST", "/api/events/e1/comments", map[string]string{"content": strings.Repeat("😀", 76)}, u1); rec.Code != 400 {
		t.Fatalf("76 emoji: %d", rec.Code)
	}
	rec := env.Do(t, "POST", "/api/events/e1/comments", map[string]string{"content": "  Seru banget!  "}, u1)
	if rec.Code != 201 {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	c := decode[comment](t, rec)
	if c.Content != "Seru banget!" || c.Author.Name != "u1" || c.Author.Image == nil || *c.Author.Image != "https://img/u1.jpg" || c.ID == "" {
		t.Fatalf("post body %s", rec.Body)
	}
	if n := one[int](t, env, `select count(*) from wall_comment where id=$1 and user_id='u1' and event_id='e1' and content='Seru banget!'`, c.ID); n != 1 {
		t.Fatal("not inserted")
	}
	env.Exec(t, `insert into wall_comment(id,event_id,user_id,content,created_at) values('old','e1','u2','lama',now()-interval '1 day')`)

	rec = env.Do(t, "GET", "/api/events/e1/comments", nil, nil)
	assertGolden(t, rec, "events-{id}-comments.json")
	got := decode[struct{ Comments []comment }](t, rec)
	if len(got.Comments) != 2 || got.Comments[0].ID != c.ID || got.Comments[0].CreatedAt.String() != c.CreatedAt.String() || got.Comments[1].ID != "old" || got.Comments[1].Author.Image != nil {
		t.Fatalf("list %s", rec.Body)
	}
}

func TestCommentsRequireVisibleEvent(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,deleted_at,created_at,updated_at) values
		('draft','o1','D','draft',now(),null,now(),now()),('del','o1','D','published',now(),now(),now(),now())`)
	seedOrder(t, env, "od", "u1", "draft", "paid", 1)
	seedOrder(t, env, "ox", "u1", "del", "paid", 1)
	u1 := env.Login(t, "u1", nil)
	for _, id := range []string{"nope", "draft", "del"} {
		assertError(t, env.Do(t, "GET", "/api/events/"+id+"/comments", nil, nil), 404, "Not found")
		assertError(t, env.Do(t, "POST", "/api/events/"+id+"/comments", map[string]string{"content": "hi"}, u1), 404, "Not found")
	}
	if n := one[int](t, env, `select count(*) from wall_comment`); n != 0 {
		t.Fatalf("comments inserted: %d", n)
	}
}
