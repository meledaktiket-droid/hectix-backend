package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/config"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/golden"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/testdb"
)

const secret = "test-secret-at-least-32-characters-long"

var apps = map[string]config.App{
	"web":       {Name: "web", Host: "web.test", BaseURL: "http://web.test"},
	"organizer": {Name: "organizer", Host: "org.test", BaseURL: "https://org.test", Secure: true},
	"admin":     {Name: "admin", Host: "admin.test", BaseURL: "http://admin.test"},
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	a    *Auth
	h    http.Handler
}

func newEnv(t *testing.T) *env {
	pool := testdb.New(t)
	a := &Auth{DB: pool, Secret: secret}
	r := chi.NewRouter()
	r.Use(OriginCheck, a.Load)
	a.Routes(r)
	r.With(Required).Get("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(UserFrom(r.Context()).ID))
	})
	return &env{t: t, pool: pool, a: a, h: r}
}

func (e *env) user(role string) string {
	e.t.Helper()
	id := dbx.ID()
	_, err := e.pool.Exec(context.Background(),
		`insert into "user" (id, name, email, email_verified, image, role, created_at, updated_at)
		 values ($1, 'Sample Name', $2, true, 'https://example.com/a.jpg', $3, $4, $4)`,
		id, id+"@example.com", role, time.Now().UTC())
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) session(userID string, age, expiresIn time.Duration, org *string) string {
	e.t.Helper()
	token := dbx.ID()
	created := time.Now().UTC().Add(-age)
	_, err := e.pool.Exec(context.Background(),
		`insert into session (id, expires_at, token, created_at, updated_at, ip_address, user_agent, user_id, active_organization_id)
		 values ($1, $2, $3, $4, $4, '127.0.0.1', 'test-agent', $5, $6)`,
		dbx.ID(), created.Add(expiresIn), token, created, userID, org)
	if err != nil {
		e.t.Fatal(err)
	}
	return token
}

func (e *env) do(app, method, path, token, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	a := apps[app]
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(httpx.WithApp(req.Context(), a))
	asSameOrigin(req, body)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: CookieName(a.Name, a.Secure, "session_token"), Value: Sign(token, secret)})
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func asSameOrigin(req *http.Request, body string) {
	req.Header.Set("Origin", httpx.AppFrom(req.Context()).BaseURL)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

const week = 7 * 24 * time.Hour

const unauthBody = `{"message":"Unauthorized","code":"UNAUTHORIZED"}` + "\n"

func (e *env) expect(rec *httptest.ResponseRecorder, code int, body string) {
	e.t.Helper()
	if rec.Code != code || rec.Body.String() != body {
		e.t.Errorf("got %d %q, want %d %q", rec.Code, rec.Body.String(), code, body)
	}
}

var isoMillis = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)

func TestSessions(t *testing.T) {
	e := newEnv(t)
	org := "LR64qEPTy1nGCA7ehhUd5814YgU5MhpY"

	t.Run("get-session matches goldens per app", func(t *testing.T) {
		for _, app := range []string{"web", "organizer", "admin"} {
			uid := e.user("super_admin")
			tok := e.session(uid, time.Minute, week, &org)
			rec := e.do(app, "GET", "/api/auth/get-session", tok, "")
			if rec.Code != 200 {
				t.Fatalf("%s: %d %s", app, rec.Code, rec.Body)
			}
			body := rec.Body.Bytes()
			golden.Assert(t, "../../testdata/golden/"+app+"/get-session.json", body)
			s := rec.Body.String()
			if hasRole := strings.Contains(s, `"role"`); hasRole != (app == "admin") {
				t.Errorf("%s: role present=%v: %s", app, hasRole, s)
			}
			if hasOrg := strings.Contains(s, `"activeOrganizationId"`); hasOrg != (app == "organizer") {
				t.Errorf("%s: activeOrganizationId present=%v: %s", app, hasOrg, s)
			}
			var got struct{ Session, User map[string]any }
			json.Unmarshal(body, &got)
			for _, m := range []map[string]any{got.Session, got.User} {
				for k, v := range m {
					if strings.HasSuffix(k, "At") && !isoMillis.MatchString(v.(string)) {
						t.Errorf("%s: %s = %v", app, k, v)
					}
				}
			}
			if got.Session["token"] != tok || got.User["id"] != uid {
				t.Errorf("%s: wrong session/user: %s", app, s)
			}
			if rec.Header().Get("Set-Cookie") != "" {
				t.Errorf("%s: fresh session should not set cookie", app)
			}
		}
	})

	t.Run("no cookie returns null", func(t *testing.T) {
		rec := e.do("web", "GET", "/api/auth/get-session", "", "")
		if rec.Code != 200 || rec.Body.String() != "null\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
	})

	t.Run("Required passes a valid user", func(t *testing.T) {
		uid := e.user("user")
		rec := e.do("web", "GET", "/api/whoami", e.session(uid, time.Minute, week, nil), "")
		if rec.Code != 200 || rec.Body.String() != uid {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
	})

	t.Run("forged cookie is unauthorized", func(t *testing.T) {
		uid := e.user("user")
		tok := e.session(uid, time.Minute, week, nil)
		req := httptest.NewRequest("GET", "/api/whoami", nil)
		req = req.WithContext(httpx.WithApp(req.Context(), apps["web"]))
		req.AddCookie(&http.Cookie{Name: "web.session_token", Value: Sign(tok, "wrong-secret")})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 || rec.Body.String() != "{\"error\":\"Unauthorized\"}\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		rec = httptest.NewRecorder()
		req.URL.Path = "/api/auth/get-session"
		e.h.ServeHTTP(rec, req)
		if rec.Code != 200 || rec.Body.String() != "null\n" {
			t.Fatalf("get-session: %d %q", rec.Code, rec.Body)
		}
	})

	t.Run("cookie from another app is ignored", func(t *testing.T) {
		uid := e.user("user")
		tok := e.session(uid, time.Minute, week, nil)
		req := httptest.NewRequest("GET", "/api/whoami", nil)
		req = req.WithContext(httpx.WithApp(req.Context(), apps["admin"]))
		req.AddCookie(&http.Cookie{Name: "web.session_token", Value: Sign(tok, secret)})
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("%d", rec.Code)
		}
	})

	t.Run("expired session is unauthorized", func(t *testing.T) {
		uid := e.user("user")
		tok := e.session(uid, 8*24*time.Hour, week, nil)
		if rec := e.do("web", "GET", "/api/whoami", tok, ""); rec.Code != 401 {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		rec := e.do("web", "GET", "/api/auth/get-session", tok, "")
		if rec.Body.String() != "null\n" {
			t.Fatalf("get-session: %q", rec.Body)
		}
		if c := rec.Header().Get("Set-Cookie"); !strings.Contains(c, "web.session_token=;") || !strings.Contains(c, "Max-Age=0") {
			t.Errorf("stale cookie not cleared: %q", c)
		}
	})

	t.Run("sign-out deletes row and expires cookie", func(t *testing.T) {
		uid := e.user("user")
		tok := e.session(uid, time.Minute, week, nil)
		rec := e.do("organizer", "POST", "/api/auth/sign-out", tok, "")
		if rec.Code != 200 || rec.Body.String() != "{\"success\":true}\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		if n := e.count(`select count(*) from session where token = $1`, tok); n != 0 {
			t.Errorf("session row remains")
		}
		c := rec.Header().Get("Set-Cookie")
		for _, want := range []string{"__Secure-organizer.session_token=;", "Max-Age=0", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
			if !strings.Contains(c, want) {
				t.Errorf("cookie %q missing %q", c, want)
			}
		}
	})

	t.Run("old session is extended, cookie re-set", func(t *testing.T) {
		uid := e.user("user")
		tok := e.session(uid, 2*24*time.Hour, week, nil)
		rec := e.do("web", "GET", "/api/auth/get-session", tok, "")
		if rec.Code != 200 {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		c := rec.Header().Get("Set-Cookie")
		for _, want := range []string{"web.session_token=" + Sign(tok, secret), "Max-Age=604800", "Path=/", "HttpOnly", "SameSite=Lax"} {
			if !strings.Contains(c, want) {
				t.Errorf("cookie %q missing %q", c, want)
			}
		}
		if strings.Contains(c, "Secure") {
			t.Errorf("http app cookie should not be Secure: %q", c)
		}
		var exp time.Time
		e.pool.QueryRow(context.Background(), `select expires_at from session where token = $1`, tok).Scan(&exp)
		if d := time.Until(exp); d < week-time.Minute || d > week+time.Minute {
			t.Errorf("expires_at not extended: %v", d)
		}
		var got struct{ Session struct{ ExpiresAt time.Time } }
		json.Unmarshal(rec.Body.Bytes(), &got)
		if !got.Session.ExpiresAt.Truncate(time.Millisecond).Equal(exp.Truncate(time.Millisecond)) {
			t.Errorf("response expiresAt %v != db %v", got.Session.ExpiresAt, exp)
		}
	})

	t.Run("revoke-session cannot delete another user's session", func(t *testing.T) {
		me, other := e.user("user"), e.user("user")
		myTok, mine2 := e.session(me, time.Minute, week, nil), e.session(me, time.Hour, week, nil)
		otherTok := e.session(other, time.Minute, week, nil)
		rec := e.do("web", "POST", "/api/auth/revoke-session", myTok, `{"token":"`+otherTok+`"}`)
		if rec.Code != 200 || rec.Body.String() != "{\"status\":true}\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		if e.count(`select count(*) from session where token = $1`, otherTok) != 1 {
			t.Fatal("deleted another user's session")
		}
		e.do("web", "POST", "/api/auth/revoke-session", myTok, `{"token":"`+mine2+`"}`)
		if e.count(`select count(*) from session where token = $1`, mine2) != 0 {
			t.Fatal("own session not revoked")
		}
		e.expect(e.do("web", "POST", "/api/auth/revoke-session", "", `{"token":"x"}`), 401, unauthBody)
		e.expect(e.do("web", "POST", "/api/auth/revoke-session", myTok, `{}`), 400,
			`{"message":"[body.token] Invalid input: expected string, received undefined","code":"VALIDATION_ERROR"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/revoke-session", myTok, `{"token":1}`), 400,
			`{"message":"[body.token] Invalid input: expected string, received number","code":"VALIDATION_ERROR"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/revoke-session", "", ``), 400,
			`{"message":"[body] Invalid input: expected object, received undefined","code":"VALIDATION_ERROR"}`+"\n")
	})

	t.Run("revoke-other-sessions keeps current", func(t *testing.T) {
		e.expect(e.do("web", "POST", "/api/auth/revoke-other-sessions", "", ""), 401, unauthBody)
		me, other := e.user("user"), e.user("user")
		cur := e.session(me, time.Minute, week, nil)
		e.session(me, time.Hour, week, nil)
		e.session(me, 2*time.Hour, week, nil)
		otherTok := e.session(other, time.Minute, week, nil)
		rec := e.do("web", "POST", "/api/auth/revoke-other-sessions", cur, "")
		if rec.Code != 200 || rec.Body.String() != "{\"status\":true}\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		if n := e.count(`select count(*) from session where user_id = $1`, me); n != 1 {
			t.Errorf("remaining own sessions = %d", n)
		}
		if e.count(`select count(*) from session where token = $1`, cur) != 1 || e.count(`select count(*) from session where token = $1`, otherTok) != 1 {
			t.Error("wrong session deleted")
		}
	})

	t.Run("list-sessions returns own unexpired sessions", func(t *testing.T) {
		me, other := e.user("user"), e.user("user")
		cur := e.session(me, time.Minute, week, &org)
		older := e.session(me, time.Hour, week, nil)
		e.session(me, 8*24*time.Hour, week, nil)
		e.session(other, time.Minute, week, nil)
		e.expect(e.do("organizer", "GET", "/api/auth/list-sessions", "", ""), 401, unauthBody)
		rec := e.do("organizer", "GET", "/api/auth/list-sessions", cur, "")
		if rec.Code != 200 {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		golden.Assert(t, "../../testdata/golden/organizer/list-sessions.json", rec.Body.Bytes())
		var got []map[string]any
		json.Unmarshal(rec.Body.Bytes(), &got)
		if len(got) != 2 || got[0]["token"] != older || got[1]["token"] != cur {
			t.Fatalf("got %s", rec.Body)
		}
		if _, ok := got[0]["activeOrganizationId"]; !ok {
			t.Error("organizer sessions must carry activeOrganizationId (null)")
		}
	})

	t.Run("update-user", func(t *testing.T) {
		me := e.user("user")
		tok := e.session(me, time.Minute, week, nil)
		rec := e.do("admin", "POST", "/api/auth/update-user", tok, `{"name":"New Name","image":null,"role":"super_admin"}`)
		if rec.Code != 200 || rec.Body.String() != "{\"status\":true}\n" {
			t.Fatalf("%d %q", rec.Code, rec.Body)
		}
		var name, role string
		var image *string
		e.pool.QueryRow(context.Background(), `select name, image, role from "user" where id = $1`, me).Scan(&name, &image, &role)
		if name != "New Name" || image != nil || role != "user" {
			t.Errorf("got name=%q image=%v role=%q", name, image, role)
		}
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `{}`), 400, `{"message":"No fields to update"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `{"email":"x@y.z","name":"a"}`), 400,
			`{"message":"Email can not be updated","code":"EMAIL_CAN_NOT_BE_UPDATED"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/update-user", tok, `[]`), 400,
			`{"message":"[body] Invalid input: expected record, received array","code":"VALIDATION_ERROR"}`+"\n")
		e.expect(e.do("web", "POST", "/api/auth/update-user", "", `{"name":"x"}`), 401, unauthBody)
	})

	t.Run("CreateSession and SetSessionCookie", func(t *testing.T) {
		uid := e.user("user")
		s, err := e.a.CreateSession(context.Background(), uid, "1.2.3.4", "ua", &org)
		if err != nil {
			t.Fatal(err)
		}
		if len(s.ID) != 32 || len(s.Token) != 32 || s.UserID != uid || *s.ActiveOrganizationID != org {
			t.Fatalf("%+v", s)
		}
		if d := time.Until(s.ExpiresAt.Time); d < week-time.Minute || d > week {
			t.Errorf("expires in %v", d)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req = req.WithContext(httpx.WithApp(req.Context(), apps["organizer"]))
		e.a.SetSessionCookie(rec, req, s)
		c := rec.Header().Get("Set-Cookie")
		if !strings.HasPrefix(c, "__Secure-organizer.session_token="+Sign(s.Token, secret)+";") || !strings.Contains(c, "Max-Age=604800") {
			t.Errorf("cookie %q", c)
		}
		if rec := e.do("organizer", "GET", "/api/whoami", s.Token, ""); rec.Body.String() != uid {
			t.Errorf("created session not usable: %d %q", rec.Code, rec.Body)
		}
	})
}
