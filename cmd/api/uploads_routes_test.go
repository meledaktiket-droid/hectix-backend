package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/config"
	"hectix/backend/internal/testdb"
)

func uploadCfg(t *testing.T) config.Config {
	cfg := routeCfg()
	cfg.Apps["admin.test"] = config.App{Name: "admin", Host: "admin.test", BaseURL: "http://admin.test"}
	cfg.UploadDir = t.TempDir()
	cfg.AuthSecret = "test-secret"
	return cfg
}

func TestUploadRoutes(t *testing.T) {
	h := newServer(uploadCfg(t), nil)

	for _, host := range []string{"web.test", "organizer.test", "admin.test"} {
		if rec := do(h, host, "POST", "/api/upload", `{}`); rec.Code != 401 {
			t.Errorf("%s upload: %d %q", host, rec.Code, rec.Body)
		}
		if rec := do(h, host, "GET", "/api/files/verification/u1/a.pdf", ""); rec.Code != 401 {
			t.Errorf("%s files: %d %q", host, rec.Code, rec.Body)
		}
	}
	for _, host := range []string{"web.test", "organizer.test"} {
		if rec := do(h, host, "PUT", "/api/upload/put?t=x.y", "data"); rec.Code != 403 ||
			rec.Body.String() != "{\"error\":\"Invalid or expired upload token\"}\n" {
			t.Errorf("%s put: %d %q", host, rec.Code, rec.Body)
		}
	}
	if rec := do(h, "admin.test", "PUT", "/api/upload/put?t=x.y", "data"); rec.Code != 401 {
		t.Errorf("admin put: %d %q", rec.Code, rec.Body)
	}
}

func TestAdminStaffGate(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	cfg := uploadCfg(t)
	h := newServer(cfg, pool)
	a := &auth.Auth{DB: pool, Secret: cfg.AuthSecret}

	if _, err := pool.Exec(ctx, `insert into "user"(id,name,email,email_verified,role,created_at,updated_at) values
		('u1','User','u@x.id',true,'user',now(),now()),('s1','Staff','s@x.id',true,'staff',now(),now())`); err != nil {
		t.Fatal(err)
	}
	cookie := func(userID string) string {
		s, err := a.CreateSession(ctx, userID, "", "", nil)
		if err != nil {
			t.Fatal(err)
		}
		return auth.CookieName("admin", false, "session_token") + "=" + auth.Sign(s.Token, cfg.AuthSecret)
	}
	send := func(c, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "admin.test"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://admin.test")
		req.Header.Set("Cookie", c)
		h.ServeHTTP(rec, req)
		return rec
	}
	upload := `{"filename":"a.png","contentType":"image/png","size":10}`

	user := cookie("u1")
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/upload", upload},
		{"GET", "/api/files/verification/u1/a.pdf", ""},
	} {
		if rec := send(user, c.method, c.path, c.body); rec.Code != 403 || rec.Body.String() != "{\"error\":\"Forbidden\"}\n" {
			t.Errorf("non-staff %s: %d %q", c.path, rec.Code, rec.Body)
		}
	}
	if rec := send(user, "GET", "/api/auth/get-session", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"u1"`) {
		t.Errorf("non-staff get-session: %d %q", rec.Code, rec.Body)
	}

	staff := cookie("s1")
	if rec := send(staff, "POST", "/api/upload", upload); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"key":"banners/`) {
		t.Errorf("staff upload: %d %q", rec.Code, rec.Body)
	}
	if rec := send(staff, "GET", "/api/files/verification/u1/a.pdf", ""); rec.Code != 404 {
		t.Errorf("staff files (missing file): %d %q", rec.Code, rec.Body)
	}
}
