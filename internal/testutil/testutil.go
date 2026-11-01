package testutil

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/config"
	"hectix/backend/internal/deps"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/testdb"
)

const Secret = "test-secret"

var hosts = map[string]string{"web": "web.test", "organizer": "organizer.test", "admin": "admin.test"}

type Env struct {
	DB     *pgxpool.Pool
	App    config.App
	Auth   *auth.Auth
	Router chi.Router
	Deps   deps.Deps
}

func New(t *testing.T, app string) *Env {
	t.Helper()
	host := hosts[app]
	a := config.App{Name: app, Host: host, BaseURL: "https://" + host, Secure: true}
	db := testdb.New(t)
	au := &auth.Auth{DB: db, Secret: Secret}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(httpx.WithApp(req.Context(), a)))
		})
	})
	r.Use(auth.OriginCheck, au.Load)
	cfg := config.Config{AuthSecret: Secret, Apps: map[string]config.App{host: a}}
	return &Env{DB: db, App: a, Auth: au, Router: r, Deps: deps.Deps{DB: db, Cfg: cfg}}
}

func (e *Env) Exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.DB.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func (e *Env) SeedUser(t *testing.T, id, role string) {
	t.Helper()
	e.Exec(t, `insert into "user"(id,name,email,email_verified,role,created_at,updated_at)
		values($1,$1,$1||'@example.com',true,$2,now(),now())`, id, role)
}

func (e *Env) Login(t *testing.T, userID string, activeOrg *string) *http.Cookie {
	t.Helper()
	s, err := e.Auth.CreateSession(context.Background(), userID, "", "", activeOrg)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: auth.CookieName(e.App.Name, e.App.Secure, "session_token"), Value: auth.Sign(s.Token, Secret)}
}

func (e *Env) Do(t *testing.T, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = bytes.NewBufferString(b)
	case []byte:
		rd = bytes.NewBuffer(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewBuffer(buf)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = e.App.Host
	req.Header.Set("Origin", e.App.BaseURL)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.Router.ServeHTTP(rec, req)
	return rec
}
