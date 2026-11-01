package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type profile struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

type fakeGoogle struct {
	t     *testing.T
	srv   *httptest.Server
	mu    sync.Mutex
	codes map[string]fakeCode
}

type fakeCode struct {
	challenge, redirect string
	p                   profile
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	g := &fakeGoogle{t: t, codes: map[string]fakeCode{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		g.mu.Lock()
		c, ok := g.codes[r.PostForm.Get("code")]
		delete(g.codes, r.PostForm.Get("code"))
		g.mu.Unlock()
		if !ok || r.PostForm.Get("client_id") != "gid" || r.PostForm.Get("client_secret") != "gsecret" ||
			r.PostForm.Get("grant_type") != "authorization_code" ||
			r.PostForm.Get("redirect_uri") != c.redirect ||
			oauth2.S256ChallengeFromVerifier(r.PostForm.Get("code_verifier")) != c.challenge {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		b, _ := json.Marshal(c.p)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at." + string(b), "refresh_token": "rt-" + c.p.Sub, "id_token": "idt-" + c.p.Sub,
			"expires_in": 3599, "token_type": "Bearer", "scope": "openid https://www.googleapis.com/auth/userinfo.email",
		})
	})
	mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		at, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer at.")
		if !ok {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(at))
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGoogle) authorize(authURL string, p profile) (code, state string) {
	g.t.Helper()
	u, err := url.Parse(authURL)
	if err != nil || !strings.HasPrefix(authURL, g.srv.URL+"/auth?") {
		g.t.Fatalf("auth url %q", authURL)
	}
	q := u.Query()
	for k, want := range map[string]string{"response_type": "code", "client_id": "gid", "code_challenge_method": "S256"} {
		if q.Get(k) != want {
			g.t.Fatalf("auth url %s=%q want %q", k, q.Get(k), want)
		}
	}
	scopes := strings.Fields(q.Get("scope"))
	for _, s := range []string{"openid", "email", "profile"} {
		if !strings.Contains(" "+strings.Join(scopes, " ")+" ", " "+s+" ") {
			g.t.Fatalf("scope %q missing %s", q.Get("scope"), s)
		}
	}
	code = dbx.ID()
	g.mu.Lock()
	g.codes[code] = fakeCode{q.Get("code_challenge"), q.Get("redirect_uri"), p}
	g.mu.Unlock()
	return code, q.Get("state")
}

func (e *env) on(t *testing.T) *env { c := *e; c.t = t; return &c }

func (e *env) withGoogle(t *testing.T) *fakeGoogle {
	g := newFakeGoogle(t)
	e.a.GoogleClientID, e.a.GoogleClientSecret = "gid", "gsecret"
	e.a.GoogleAuthURL = g.srv.URL + "/auth"
	e.a.GoogleTokenURL = g.srv.URL + "/token"
	e.a.GoogleUserinfoURL = g.srv.URL + "/userinfo"
	return g
}

func (e *env) req(app, method, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req = req.WithContext(httpx.WithApp(req.Context(), apps[app]))
	asSameOrigin(req, body)
	req.Header.Set("User-Agent", "test-browser")
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

type flow struct {
	signIn   *httptest.ResponseRecorder
	callback *httptest.ResponseRecorder
	state    string
}

func (e *env) login(g *fakeGoogle, app, body string, p profile) flow {
	e.t.Helper()
	rec := e.req(app, "POST", "/api/auth/sign-in/social", body)
	if rec.Code != 200 {
		e.t.Fatalf("sign-in: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		URL      string `json:"url"`
		Redirect bool   `json:"redirect"`
	}
	json.Unmarshal(rec.Body.Bytes(), &out)
	code, state := g.authorize(out.URL, p)
	sc := cookieNamed(rec, CookieName(app, apps[app].Secure, "state"))
	if sc == nil {
		e.t.Fatalf("no state cookie: %v", rec.Header()["Set-Cookie"])
	}
	cb := e.req(app, "GET", "/api/auth/callback/google?code="+code+"&state="+state, "", sc)
	return flow{rec, cb, state}
}

func TestGoogleOAuth(t *testing.T) {
	e := newEnv(t)
	g := e.withGoogle(t)

	t.Run("new user end to end", func(t *testing.T) {
		e := e.on(t)
		p := profile{Sub: "g-new", Email: "New.User@Example.com", EmailVerified: true, Name: "New User", Picture: "https://img/new.jpg"}
		f := e.login(g, "web", `{"provider":"google","callbackURL":"/events/1","newUserCallbackURL":"/welcome"}`, p)

		if b := f.signIn.Body.String(); !strings.HasPrefix(b, `{"url":"http`) || !strings.HasSuffix(b, `","redirect":true}`+"\n") ||
			f.signIn.Header().Get("Location") != mustURL(t, f.signIn) {
			t.Errorf("sign-in body %s", b)
		}
		u, _ := url.Parse(mustURL(t, f.signIn))
		if got := u.Query().Get("redirect_uri"); got != "http://web.test/api/auth/callback/google" {
			t.Errorf("redirect_uri %q", got)
		}
		sc := cookieNamed(f.signIn, "web.state")
		if !sc.HttpOnly || sc.MaxAge != 300 || sc.Path != "/" || sc.SameSite != http.SameSiteLaxMode {
			t.Errorf("state cookie %+v", sc)
		}
		if v, ok := Unsign(sc.Value, secret); !ok || v != f.state {
			t.Errorf("state cookie value %q", sc.Value)
		}

		cb := f.callback
		if cb.Code != http.StatusFound || cb.Header().Get("Location") != "/welcome" {
			t.Fatalf("callback %d %q %s", cb.Code, cb.Header().Get("Location"), cb.Body)
		}
		if c := cookieNamed(cb, "web.state"); c == nil || c.MaxAge != -1 {
			t.Errorf("state cookie not cleared: %v", cb.Header()["Set-Cookie"])
		}
		sess := cookieNamed(cb, "web.session_token")
		if sess == nil {
			t.Fatalf("no session cookie: %v", cb.Header()["Set-Cookie"])
		}

		var uid, email, name, role string
		var verified bool
		var image *string
		e.pool.QueryRow(context.Background(), `select id, email, name, role, email_verified, image from "user" where email = 'new.user@example.com'`).
			Scan(&uid, &email, &name, &role, &verified, &image)
		if len(uid) != 32 || name != "New User" || role != "user" || !verified || image == nil || *image != p.Picture {
			t.Fatalf("user %q %q %q %q %v %v", uid, email, name, role, verified, image)
		}
		var accID, at, rt, idt, scope string
		var exp time.Time
		err := e.pool.QueryRow(context.Background(), `select account_id, access_token, refresh_token, id_token, scope, access_token_expires_at
			from account where user_id = $1 and provider_id = 'google'`, uid).Scan(&accID, &at, &rt, &idt, &scope, &exp)
		if err != nil || accID != "g-new" || !strings.HasPrefix(at, "at.") || rt != "rt-g-new" || idt != "idt-g-new" ||
			scope != "openid,https://www.googleapis.com/auth/userinfo.email" || time.Until(exp) < 59*time.Minute {
			t.Fatalf("account %v %q %q %q %q %q %v", err, accID, at, rt, idt, scope, exp)
		}
		var ip, ua string
		e.pool.QueryRow(context.Background(), `select ip_address, user_agent from session where user_id = $1`, uid).Scan(&ip, &ua)
		if ip != "203.0.113.9" || ua != "test-browser" {
			t.Errorf("session ip/ua %q %q", ip, ua)
		}
		if n := e.count(`select count(*) from verification where identifier = $1`, f.state); n != 0 {
			t.Errorf("verification not consumed")
		}

		gs := e.req("web", "GET", "/api/auth/get-session", "", sess)
		var got struct{ User struct{ ID, Email string } }
		json.Unmarshal(gs.Body.Bytes(), &got)
		if got.User.ID != uid || got.User.Email != "new.user@example.com" {
			t.Fatalf("get-session %s", gs.Body)
		}

		p.Name = "Renamed"
		f2 := e.login(g, "web", `{"provider":"google","callbackURL":"/events/1","newUserCallbackURL":"/welcome"}`, p)
		if f2.callback.Header().Get("Location") != "/events/1" {
			t.Fatalf("second login %d %q", f2.callback.Code, f2.callback.Header().Get("Location"))
		}
		if e.count(`select count(*) from "user" where email = 'new.user@example.com'`) != 1 ||
			e.count(`select count(*) from account where user_id = $1`, uid) != 1 ||
			e.count(`select count(*) from session where user_id = $1`, uid) != 2 {
			t.Error("second login duplicated rows")
		}
	})

	t.Run("existing email, unverified at google: not linked", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		f := e.login(g, "web", `{"provider":"google","callbackURL":"/","errorCallbackURL":"/login?x=1"}`,
			profile{Sub: "g-unv", Email: uid + "@example.com", EmailVerified: false, Name: "X"})
		if f.callback.Code != http.StatusFound || f.callback.Header().Get("Location") != "/login?x=1&error=account_not_linked" {
			t.Fatalf("%d %q", f.callback.Code, f.callback.Header().Get("Location"))
		}
		if e.count(`select count(*) from account where user_id = $1`, uid) != 0 || cookieNamed(f.callback, "web.session_token") != nil {
			t.Error("linked or signed in anyway")
		}
	})

	t.Run("existing email, verified: linked", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		f := e.login(g, "web", `{"provider":"google","callbackURL":"/home","newUserCallbackURL":"/welcome"}`,
			profile{Sub: "g-link", Email: strings.ToUpper(uid) + "@EXAMPLE.com", EmailVerified: true, Name: "X"})
		if f.callback.Header().Get("Location") != "/home" {
			t.Fatalf("%d %q", f.callback.Code, f.callback.Header().Get("Location"))
		}
		if e.count(`select count(*) from account where user_id = $1 and account_id = 'g-link'`, uid) != 1 {
			t.Error("not linked")
		}
	})

	t.Run("organizer session gets oldest membership as active org", func(t *testing.T) {
		e := e.on(t)
		uid := e.user("user")
		ctx := context.Background()
		for i, org := range []string{"org-new", "org-old"} {
			e.pool.Exec(ctx, `insert into organization (id, name, created_at) values ($1, $1, now())`, org)
			e.pool.Exec(ctx, `insert into member (id, organization_id, user_id, role, created_at) values ($1, $2, $3, 'owner', $4)`,
				dbx.ID(), org, uid, time.Now().Add(-time.Duration(i)*time.Hour))
		}
		e.pool.Exec(ctx, `insert into account (id, account_id, provider_id, user_id, created_at, updated_at) values ($1, 'g-org', 'google', $2, now(), now())`, dbx.ID(), uid)
		f := e.login(g, "organizer", `{"provider":"google","callbackURL":"https://org.test/dashboard"}`,
			profile{Sub: "g-org", Email: uid + "@example.com", EmailVerified: true})
		if f.callback.Header().Get("Location") != "https://org.test/dashboard" {
			t.Fatalf("%d %q", f.callback.Code, f.callback.Header().Get("Location"))
		}
		var org *string
		e.pool.QueryRow(ctx, `select active_organization_id from session where user_id = $1`, uid).Scan(&org)
		if org == nil || *org != "org-old" {
			t.Fatalf("active org %v", org)
		}
		sc := cookieNamed(f.callback, "__Secure-organizer.session_token")
		if sc == nil || !sc.Secure {
			t.Fatal("secure session cookie missing")
		}
	})

	startFlow := func(t *testing.T) (code, state string, sc *http.Cookie) {
		e := e.on(t)
		rec := e.req("web", "POST", "/api/auth/sign-in/social", `{"provider":"google","callbackURL":"/","errorCallbackURL":"/oops"}`)
		var out struct{ URL string }
		json.Unmarshal(rec.Body.Bytes(), &out)
		code, state = g.authorize(out.URL, profile{Sub: "g-x", Email: "x@example.com", EmailVerified: true})
		return code, state, cookieNamed(rec, "web.state")
	}

	t.Run("state cookie mismatch", func(t *testing.T) {
		e := e.on(t)
		code, state, _ := startFlow(t)
		_, _, other := startFlow(t)
		rec := e.req("web", "GET", "/api/auth/callback/google?code="+code+"&state="+state, "", other)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "http://web.test?error=state_mismatch" {
			t.Fatalf("%d %q", rec.Code, rec.Header().Get("Location"))
		}
		rec = e.req("web", "GET", "/api/auth/callback/google?code="+code+"&state="+state, "")
		if rec.Header().Get("Location") != "http://web.test?error=state_mismatch" {
			t.Fatalf("no cookie: %q", rec.Header().Get("Location"))
		}
	})

	t.Run("replayed state", func(t *testing.T) {
		e := e.on(t)
		code, state, sc := startFlow(t)
		if rec := e.req("web", "GET", "/api/auth/callback/google?code="+code+"&state="+state, "", sc); rec.Header().Get("Location") != "/" {
			t.Fatalf("first: %q", rec.Header().Get("Location"))
		}
		rec := e.req("web", "GET", "/api/auth/callback/google?code="+code+"&state="+state, "", sc)
		if rec.Header().Get("Location") != "http://web.test?error=please_restart_the_process" || cookieNamed(rec, "web.session_token") != nil {
			t.Fatalf("replay: %q", rec.Header().Get("Location"))
		}
	})

	t.Run("expired state", func(t *testing.T) {
		e := e.on(t)
		code, state, sc := startFlow(t)
		e.pool.Exec(context.Background(), `update verification set expires_at = $1 where identifier = $2`, time.Now().UTC().Add(-time.Second), state)
		rec := e.req("web", "GET", "/api/auth/callback/google?code="+code+"&state="+state, "", sc)
		if rec.Header().Get("Location") != "http://web.test?error=please_restart_the_process" {
			t.Fatalf("%q", rec.Header().Get("Location"))
		}
	})

	t.Run("callback errors go to errorCallbackURL", func(t *testing.T) {
		e := e.on(t)
		_, state, sc := startFlow(t)
		rec := e.req("web", "GET", "/api/auth/callback/google?error=access_denied&state="+state, "", sc)
		if rec.Header().Get("Location") != "/oops?error=access_denied" {
			t.Fatalf("%q", rec.Header().Get("Location"))
		}
		_, state, sc = startFlow(t)
		rec = e.req("web", "GET", "/api/auth/callback/google?code=bogus&state="+state, "", sc)
		if rec.Header().Get("Location") != "/oops?error=invalid_code" {
			t.Fatalf("%q", rec.Header().Get("Location"))
		}
		rec = e.req("web", "GET", "/api/auth/callback/google?code=x", "")
		if rec.Header().Get("Location") != "http://web.test?state=state_not_found" {
			t.Fatalf("%q", rec.Header().Get("Location"))
		}
	})

	t.Run("sign-in validation", func(t *testing.T) {
		e := e.on(t)
		cases := []struct{ body, status, want string }{
			{`{"provider":"google","callbackURL":"https://evil.com/x"}`, "403", `{"message":"Invalid callbackURL","code":"INVALID_CALLBACK_URL"}`},
			{`{"provider":"google","callbackURL":"//evil.com"}`, "403", `{"message":"Invalid callbackURL","code":"INVALID_CALLBACK_URL"}`},
			{`{"provider":"google","callbackURL":"/","errorCallbackURL":"https://evil.com"}`, "403", `{"message":"Invalid errorCallbackURL","code":"INVALID_ERROR_CALLBACK_URL"}`},
			{`{"provider":"google","callbackURL":"/","newUserCallbackURL":"https://web.test.evil.com/"}`, "403", `{"message":"Invalid newUserCallbackURL","code":"INVALID_NEW_USER_CALLBACK_URL"}`},
			{`{"provider":"github","callbackURL":"/"}`, "404", `{"message":"Provider not found","code":"PROVIDER_NOT_FOUND"}`},
		}
		for _, c := range cases {
			rec := e.req("web", "POST", "/api/auth/sign-in/social", c.body)
			if got := rec.Body.String(); itoa(rec.Code) != c.status || got != c.want+"\n" {
				t.Errorf("%s: %d %s", c.body, rec.Code, got)
			}
		}
		rec := e.req("web", "POST", "/api/auth/sign-in/social", `{"provider":"google","disableRedirect":true}`)
		var out struct {
			URL      string
			Redirect *bool
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 || out.Redirect == nil || *out.Redirect || rec.Header().Get("Location") != "" {
			t.Errorf("disableRedirect: %d %s", rec.Code, rec.Body)
		}
		var v string
		e.pool.QueryRow(context.Background(), `select value from verification order by created_at desc limit 1`).Scan(&v)
		var sd map[string]any
		json.Unmarshal([]byte(v), &sd)
		if sd["callbackURL"] != "http://web.test" || sd["codeVerifier"] == "" || sd["expiresAt"] == nil {
			t.Errorf("default callbackURL / state data: %s", v)
		}
	})
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func mustURL(t *testing.T, rec *httptest.ResponseRecorder) string {
	var out struct{ URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.URL
}

func TestSafeRedirect(t *testing.T) {
	base := "https://dev.hectix.id"
	cases := map[string]bool{
		"/":                          true,
		"/dashboard":                 true,
		"/events/1?tab=a&b=c":        true,
		"/a/b.c-d_e+f@g":             true,
		"https://dev.hectix.id":      true,
		"https://dev.hectix.id/":     true,
		"https://dev.hectix.id/x?y":  true,
		"HTTPS://DEV.HECTIX.ID/x":    true,
		"//evil.com":                 false,
		"/\\evil.com":                false,
		"/%2fevil.com":               false,
		"/%5Cevil.com":               false,
		"/a b":                       false,
		"/x#frag":                    false,
		"https://dev.hectix.id.evil": false,
		"https://evil.com/https://dev.hectix.id/": false,
		"https://dev.hectix.id@evil.com/":         false,
		"http://dev.hectix.id/":                   false,
		"javascript:alert(1)":                     false,
		"evil.com":                                false,
		"":                                        false,
	}
	for in, want := range cases {
		if got := safeRedirect(in, base); got != want {
			t.Errorf("safeRedirect(%q) = %v, want %v", in, got, want)
		}
	}
}
