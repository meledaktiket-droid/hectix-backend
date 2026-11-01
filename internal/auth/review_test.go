package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/httpx"
)

func sessionCookies(rec *httptest.ResponseRecorder, name string) []string {
	var out []string
	for _, c := range rec.Header()["Set-Cookie"] {
		if strings.HasPrefix(c, name+"=") {
			out = append(out, c)
		}
	}
	return out
}

func TestSessionReviewFixes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	t.Run("db error is a 500, not anonymous", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		tok := e.session(uid, time.Minute, week, nil)
		closed, err := pgxpool.New(ctx, e.pool.Config().ConnString())
		if err != nil {
			t.Fatal(err)
		}
		closed.Close()
		a := &Auth{DB: closed, Secret: secret}
		r := chi.NewRouter()
		r.Use(OriginCheck, a.Load)
		a.Routes(r)
		r.With(Required).Get("/api/whoami", func(w http.ResponseWriter, r *http.Request) {})
		broken := &env{t: t, pool: e.pool, a: a, h: r}

		rec := broken.do("web", "GET", "/api/auth/get-session", tok, "")
		e.expect(rec, 500, `{"message":"Failed to get session","code":"FAILED_TO_GET_SESSION"}`+"\n")
		if c := rec.Header().Get("Set-Cookie"); c != "" {
			t.Errorf("cookie touched on db error: %q", c)
		}
		e.expect(broken.do("web", "GET", "/api/whoami", tok, ""), 500, `{"error":"Internal server error"}`+"\n")
		e.expect(broken.do("web", "GET", "/api/auth/list-sessions", tok, ""), 500, `{"message":"Failed to get session","code":"FAILED_TO_GET_SESSION"}`+"\n")
		e.expect(broken.do("web", "POST", "/api/auth/update-user", tok, `{"name":"x"}`), 500, `{"message":"Failed to get session","code":"FAILED_TO_GET_SESSION"}`+"\n")
	})

	t.Run("refresh that updates no row is no session", func(t *testing.T) {
		e := e.on(t)
		_, err := e.pool.Exec(ctx, `
			create function skip_update() returns trigger language plpgsql as $$ begin return null; end $$;
			create trigger skip_update before update on session for each row
				when (old.user_agent = 'no-update') execute function skip_update();`)
		if err != nil {
			t.Fatal(err)
		}
		defer e.pool.Exec(ctx, `drop trigger skip_update on session; drop function skip_update();`)
		uid := e.user("user")
		tok := e.session(uid, 2*24*time.Hour, week, nil)
		e.pool.Exec(ctx, `update session set user_agent = 'no-update' where token = $1`, tok)

		rec := e.do("web", "GET", "/api/auth/get-session", tok, "")
		if rec.Code != 200 || rec.Body.String() != "null\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		cs := sessionCookies(rec, "web.session_token")
		if len(cs) != 1 || !strings.Contains(cs[0], "Max-Age=0") {
			t.Errorf("want one clearing cookie, got %q", cs)
		}
	})

	t.Run("invalid signature: null without Set-Cookie", func(t *testing.T) {
		e := e.on(t)
		req := httptest.NewRequest("GET", "/api/auth/get-session", nil)
		req = req.WithContext(httpx.WithApp(req.Context(), apps["web"]))
		req.AddCookie(&http.Cookie{Name: "web.session_token", Value: Sign("whatever", "wrong-secret")})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Body.String() != "null\n" || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("%q %q", rec.Body, rec.Header().Get("Set-Cookie"))
		}
		rec = e.do("web", "GET", "/api/auth/get-session", "unknown-token", "")
		if cs := sessionCookies(rec, "web.session_token"); rec.Body.String() != "null\n" || len(cs) != 1 || !strings.Contains(cs[0], "Max-Age=0") {
			t.Errorf("unknown token: %q %q", rec.Body, cs)
		}
	})

	t.Run("update-user null name is not provided", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		tok := e.session(uid, time.Minute, week, nil)
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `{"name":null}`), 400, `{"message":"No fields to update"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `{"name":null,"image":"https://x/y.png"}`), 200, `{"status":true}`+"\n")
		var name string
		var image *string
		e.pool.QueryRow(ctx, `select name, image from "user" where id = $1`, uid).Scan(&name, &image)
		if name != "Sample Name" || image == nil || *image != "https://x/y.png" {
			t.Errorf("name %q image %v", name, image)
		}
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `{"image":null}`), 200, `{"status":true}`+"\n")
		e.pool.QueryRow(ctx, `select image from "user" where id = $1`, uid).Scan(&image)
		if image != nil {
			t.Errorf("image not cleared: %v", *image)
		}
	})

	t.Run("sign-out of an old session sends one Set-Cookie", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		tok := e.session(uid, 2*24*time.Hour, week, nil)
		rec := e.do("web", "POST", "/api/auth/sign-out", tok, "")
		cs := sessionCookies(rec, "web.session_token")
		if len(cs) != 1 || !strings.Contains(cs[0], "Max-Age=0") {
			t.Errorf("got %q", cs)
		}
	})
}
