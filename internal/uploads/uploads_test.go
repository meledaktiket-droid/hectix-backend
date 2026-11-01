package uploads

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/config"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/testdb"
)

var apps = map[string]config.App{
	"web":       {Name: "web", BaseURL: "https://dev.hectix.id"},
	"organizer": {Name: "organizer", BaseURL: "https://organizer.dev.hectix.id"},
	"admin":     {Name: "admin", BaseURL: "https://admin.dev.hectix.id"},
}

var (
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 40)...)
	pdfBytes = []byte("%PDF-1.4\n" + strings.Repeat("x", 30))
	t0       = time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
)

func newU(t *testing.T, db *pgxpool.Pool) *Uploads {
	return &Uploads{
		Dir: t.TempDir(), Secret: "secret", DB: db,
		IsStaff: func(role string) bool { return role == "staff" },
		now:     func() time.Time { return t0 },
	}
}

func withApp(r *http.Request, app string) *http.Request {
	return r.WithContext(httpx.WithApp(r.Context(), apps[app]))
}

func post(u *Uploads, app, userID, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := withApp(httptest.NewRequest(http.MethodPost, "/api/upload", strings.NewReader(body)), app)
	u.upload(w, r, &auth.User{ID: userID})
	return w
}

type grant struct{ URL, Key, PublicURL string }

func mustGrant(t *testing.T, u *Uploads, app, userID, body string) grant {
	t.Helper()
	w := post(u, app, userID, body)
	if w.Code != 200 {
		t.Fatalf("POST %s: %d %s", body, w.Code, w.Body)
	}
	var g struct {
		URL       string `json:"url"`
		Key       string `json:"key"`
		PublicURL string `json:"publicUrl"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	return grant{g.URL, g.Key, g.PublicURL}
}

func put(u *Uploads, url, ct string, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	u.Put(w, r)
	return w
}

func TestUploadValidation(t *testing.T) {
	u := newU(t, nil)
	cases := []struct {
		app, body string
		code      int
		err       string
	}{
		{"organizer", `{"filename":"a.exe","contentType":"application/x-msdownload","size":10}`, 400, "File type not allowed"},
		{"organizer", `{"filename":"a.png","size":10}`, 400, "File type not allowed"},
		{"web", `{"filename":"a.pdf","contentType":"application/pdf","size":10}`, 400, "File type not allowed"},
		{"admin", `{"filename":"a.pdf","contentType":"application/pdf","size":10}`, 400, "File type not allowed"},
		{"organizer", `{"filename":"a.pdf","contentType":"application/pdf","size":10485761}`, 400, "File too large (max 10MB)"},
		{"organizer", `{"filename":"a.pdf","contentType":"application/pdf","size":0}`, 400, "Invalid file size"},
		{"organizer", `{"filename":"a.pdf","contentType":"application/pdf","size":-5}`, 400, "Invalid file size"},
		{"organizer", `{"filename":"a.pdf","contentType":"application/pdf","size":1.5}`, 400, "Invalid file size"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"../etc"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"avatars/../../x"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"/abs"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"a\\b"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"a b"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"a//b"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":""}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"verification/u2"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"verification"}`, 400, "Invalid folder"},
		{"organizer", `not json`, 400, "Invalid request body"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"a/b/c/d/e"}`, 400, "Invalid folder"},
		{"organizer", `{"filename":"a.png","contentType":"image/png","size":10,"folder":"` + strings.Repeat("a", 101) + `"}`, 400, "Invalid folder"},
	}
	for _, c := range cases {
		w := post(u, c.app, "u1", c.body)
		if w.Code != c.code || !strings.Contains(w.Body.String(), `"error":"`+c.err+`"`) {
			t.Errorf("%s %s: got %d %s", c.app, c.body, w.Code, w.Body)
		}
	}
}

func TestUploadKeysAndURLs(t *testing.T) {
	u := newU(t, nil)
	cases := []struct {
		app, body, keyPrefix, ext, publicPrefix string
	}{
		{"organizer", `{"filename":"KTP.PDF","contentType":"application/pdf","size":10}`, "verification/u1/", ".pdf", "https://organizer.dev.hectix.id/api/files/verification/u1/"},
		{"organizer", `{"filename":"ktp.jpeg","contentType":"image/jpeg","size":10,"folder":"verification/u1"}`, "verification/u1/", ".jpeg", "https://organizer.dev.hectix.id/api/files/verification/u1/"},
		{"organizer", `{"filename":"b.webp","contentType":"image/webp","size":10,"folder":"events/banners"}`, "events/banners/", ".webp", "https://organizer.dev.hectix.id/uploads/events/banners/"},
		{"web", `{"filename":"me.png","contentType":"image/png","size":10}`, "profile/u1/", ".png", "https://dev.hectix.id/uploads/profile/u1/"},
		{"admin", `{"filename":"x.jpg","contentType":"image/jpeg","size":10}`, "banners/", ".jpg", "https://admin.dev.hectix.id/uploads/banners/"},
		{"organizer", `{"filename":"evil.html","contentType":"image/png","size":10,"folder":"avatars"}`, "avatars/", ".png", ""},
		{"organizer", `{"filename":"noext","contentType":"image/jpeg","size":10,"folder":"avatars"}`, "avatars/", ".jpg", ""},
		{"organizer", `{"contentType":"application/pdf","size":10,"folder":"payout-proofs"}`, "payout-proofs/", ".pdf", ""},
	}
	for _, c := range cases {
		g := mustGrant(t, u, c.app, "u1", c.body)
		if !strings.HasPrefix(g.Key, c.keyPrefix) || !strings.HasSuffix(g.Key, c.ext) {
			t.Errorf("%s: key %q", c.body, g.Key)
		}
		id := strings.TrimSuffix(strings.TrimPrefix(g.Key, c.keyPrefix), c.ext)
		if len(id) != 36 || strings.Count(id, "-") != 4 {
			t.Errorf("%s: not a uuid: %q", c.body, id)
		}
		if c.publicPrefix != "" && g.PublicURL != c.publicPrefix+id+c.ext {
			t.Errorf("%s: publicUrl %q", c.body, g.PublicURL)
		}
		if !strings.HasPrefix(g.URL, "/api/upload/put?t=") {
			t.Errorf("%s: url %q", c.body, g.URL)
		}
	}
}

func TestPutHappyPathPublicAndPrivate(t *testing.T) {
	u := newU(t, nil)
	pub := mustGrant(t, u, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":48,"folder":"avatars"}`)
	if w := put(u, pub.URL, "image/png", pngBytes); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	got, err := os.ReadFile(filepath.Join(u.Dir, "public", pub.Key))
	if err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("public file: %v", err)
	}
	fi, _ := os.Stat(filepath.Join(u.Dir, "public", pub.Key))
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Join(u.Dir, "public", "avatars"))
	if di.Mode().Perm() != 0o755 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}

	priv := mustGrant(t, u, "organizer", "u1", `{"filename":"ktp.pdf","contentType":"application/pdf","size":39}`)
	if w := put(u, priv.URL, "application/pdf", pdfBytes); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if got, err := os.ReadFile(filepath.Join(u.Dir, "private", priv.Key)); err != nil || !bytes.Equal(got, pdfBytes) {
		t.Fatalf("private file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(u.Dir, "public", priv.Key)); !os.IsNotExist(err) {
		t.Errorf("private file leaked into public: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(u.Dir, "private", "verification", "u1"))
	if len(entries) != 1 {
		t.Errorf("unexpected files: %v", entries)
	}
	if entries, _ := os.ReadDir(filepath.Join(u.Dir, ".tmp")); len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}

	if w := put(u, priv.URL, "application/pdf", pdfBytes); w.Code != 409 {
		t.Errorf("overwrite: %d %s", w.Code, w.Body)
	}
}

func TestPutRejections(t *testing.T) {
	u := newU(t, nil)
	newGrant := func() grant {
		return mustGrant(t, u, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":48,"folder":"avatars"}`)
	}
	tok := func(g grant) string { return strings.TrimPrefix(g.URL, "/api/upload/put?t=") }

	g := newGrant()
	payload, sig, _ := strings.Cut(tok(g), ".")
	flipped := []byte(sig)
	flipped[0] ^= 1
	cases := []struct {
		name, url, ct string
		body          []byte
		code          int
		err           string
	}{
		{"no token", "/api/upload/put", "image/png", pngBytes, 403, "Invalid or expired upload token"},
		{"garbage", "/api/upload/put?t=abc", "image/png", pngBytes, 403, "Invalid or expired upload token"},
		{"bad sig", "/api/upload/put?t=" + payload + "." + string(flipped), "image/png", pngBytes, 403, "Invalid or expired upload token"},
		{"wrong ct", g.URL, "image/jpeg", pngBytes, 400, "Content-Type does not match"},
		{"missing ct", g.URL, "", pngBytes, 400, "Content-Type does not match"},
		{"oversize", g.URL, "image/png", append(pngBytes, 0), 413, "File too large"},
		{"undersize", g.URL, "image/png", pngBytes[:47], 400, "File size does not match"},
		{"sniff mismatch", g.URL, "image/png", []byte("<html><script>alert(1)</script>" + strings.Repeat(" ", 17)), 400, "File content does not match its type"},
	}
	var claims map[string]any
	raw, _ := b64.DecodeString(payload)
	json.Unmarshal(raw, &claims)
	claims["size"] = 1 << 20
	forged, _ := json.Marshal(claims)
	cases = append(cases, struct {
		name, url, ct string
		body          []byte
		code          int
		err           string
	}{"tampered payload", "/api/upload/put?t=" + b64.EncodeToString(forged) + "." + sig, "image/png", pngBytes, 403, "Invalid or expired upload token"})

	for _, c := range cases {
		w := put(u, c.url, c.ct, c.body)
		if w.Code != c.code || !strings.Contains(w.Body.String(), c.err) {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(u.Dir, "public", g.Key)); !os.IsNotExist(err) {
		t.Errorf("rejected upload left a file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(u.Dir, "public")); !os.IsNotExist(err) {
		t.Errorf("public dir created for rejected uploads: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(u.Dir, ".tmp")); len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}

	g = newGrant()
	u.now = func() time.Time { return t0.Add(301 * time.Second) }
	if w := put(u, g.URL, "image/png", pngBytes); w.Code != 403 {
		t.Errorf("expired: %d %s", w.Code, w.Body)
	}
	u.now = func() time.Time { return t0.Add(299 * time.Second) }
	if w := put(u, g.URL, "image/png", pngBytes); w.Code != 200 {
		t.Errorf("not yet expired: %d %s", w.Code, w.Body)
	}

	other := newU(t, nil)
	other.Secret = "other"
	g2 := mustGrant(t, other, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":48,"folder":"avatars"}`)
	if w := put(u, g2.URL, "image/png", pngBytes); w.Code != 403 {
		t.Errorf("foreign secret: %d", w.Code)
	}
}

func get(u *Uploads, app string, user *auth.User, key string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := withApp(httptest.NewRequest(http.MethodGet, "/api/files/"+key, nil), app)
	u.file(w, r, user, key)
	return w
}

func TestFileAccess(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	u := newU(t, db)

	ktp := mustGrant(t, u, "organizer", "u1", `{"filename":"ktp.pdf","contentType":"application/pdf","size":39}`)
	if w := put(u, ktp.URL, "application/pdf", pdfBytes); w.Code != 200 {
		t.Fatal(w.Code)
	}
	stray := mustGrant(t, u, "organizer", "u4", `{"filename":"ktp.pdf","contentType":"application/pdf","size":39}`)
	if w := put(u, stray.URL, "application/pdf", pdfBytes); w.Code != 200 {
		t.Fatal(w.Code)
	}
	pub := mustGrant(t, u, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":48,"folder":"avatars"}`)
	if w := put(u, pub.URL, "image/png", pngBytes); w.Code != 200 {
		t.Fatal(w.Code)
	}

	vd, _ := json.Marshal(map[string]string{"ktpFileKey": ktp.Key, "strayKey": stray.Key})
	for _, s := range []string{
		`insert into "user"(id,name,email,email_verified,created_at,updated_at) values
			('u1','A','a@x.id',true,now(),now()),('u2','B','b@x.id',true,now(),now()),
			('u3','C','c@x.id',true,now(),now()),('u4','D','d@x.id',true,now(),now())`,
		`insert into organization(id,name,created_at,verification_data) values('o1','Org',now(),'` + string(vd) + `')`,
		`insert into member(id,organization_id,user_id,role,created_at) values('m1','o1','u1','owner',now()),('m2','o1','u2','member',now())`,
	} {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}

	owner := &auth.User{ID: "u1"}
	member := &auth.User{ID: "u2"}
	stranger := &auth.User{ID: "u3"}
	staff := &auth.User{ID: "s1", Role: "staff"}

	w := get(u, "organizer", owner, ktp.Key)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), pdfBytes) {
		t.Fatalf("owner: %d", w.Code)
	}
	h := w.Header()
	if h.Get("Content-Type") != "application/pdf" || h.Get("Cache-Control") != "private, no-store" ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Disposition") != "inline" ||
		h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("headers: %v", h)
	}

	for _, c := range []struct {
		name string
		app  string
		user *auth.User
		key  string
		code int
	}{
		{"stranger", "organizer", stranger, ktp.Key, 404},
		{"staff on admin", "admin", staff, ktp.Key, 200},
		{"staff on web", "web", staff, ktp.Key, 404},
		{"staff on organizer", "organizer", staff, ktp.Key, 404},
		{"non-staff on admin", "admin", stranger, ktp.Key, 404},
		{"org member", "organizer", member, ktp.Key, 200},
		{"org member, key owner not in org", "organizer", member, stray.Key, 404},
		{"owner, missing file", "organizer", owner, "verification/u1/nope.pdf", 404},
		{"public key", "organizer", owner, pub.Key, 404},
		{"public key, staff", "admin", staff, pub.Key, 404},
		{"traversal", "admin", staff, "verification/../public/" + pub.Key, 404},
		{"traversal2", "organizer", owner, "verification/u1/../../../etc/passwd", 404},
		{"dir", "organizer", owner, "verification/u1", 404},
		{"dir2", "organizer", owner, "verification/u1/", 404},
	} {
		w := get(u, c.app, c.user, c.key)
		if w.Code != c.code {
			t.Errorf("%s: got %d", c.name, w.Code)
		}
		if c.code == 200 && !bytes.Equal(w.Body.Bytes(), pdfBytes) {
			t.Errorf("%s: wrong bytes", c.name)
		}
	}
}

func TestPutAcceptsRealHeaders(t *testing.T) {
	u := newU(t, nil)
	for _, c := range []struct {
		ct   string
		body []byte
	}{
		{"image/jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")},
		{"image/png", pngBytes},
		{"image/webp", []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00")},
		{"application/pdf", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")},
	} {
		body := fmt.Sprintf(`{"filename":"f","contentType":%q,"size":%d,"folder":"avatars"}`, c.ct, len(c.body))
		g := mustGrant(t, u, "organizer", "u1", body)
		if w := put(u, g.URL, c.ct, c.body); w.Code != 200 {
			t.Errorf("%s: %d %s", c.ct, w.Code, w.Body)
		}
	}
	g := mustGrant(t, u, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":39,"folder":"avatars"}`)
	if w := put(u, g.URL, "image/png", pdfBytes); w.Code != 400 {
		t.Errorf("pdf as png: %d", w.Code)
	}
	g = mustGrant(t, u, "organizer", "u1", `{"filename":"a.png","contentType":"image/png","size":8,"folder":"avatars"}`)
	if w := put(u, g.URL, "image/png", []byte{0, 1, 2, 3, 4, 5, 6, 7}); w.Code != 400 {
		t.Errorf("octet-stream: %d", w.Code)
	}
}

func TestUploadRateLimit(t *testing.T) {
	u := newU(t, nil)
	now := t0
	u.now = func() time.Time { return now }
	body := `{"filename":"a.png","contentType":"image/png","size":10,"folder":"avatars"}`
	for i := range 60 {
		now = t0.Add(time.Duration(i) * time.Second)
		if w := post(u, "organizer", "u1", body); w.Code != 200 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w := post(u, "organizer", "u1", body)
	if w.Code != 429 || !strings.Contains(w.Body.String(), `"error":"Too many uploads"`) {
		t.Fatalf("61st: %d %s", w.Code, w.Body)
	}
	if w := post(u, "organizer", "u2", body); w.Code != 200 {
		t.Errorf("other user: %d", w.Code)
	}
	now = t0.Add(time.Hour + 500*time.Millisecond)
	if w := post(u, "organizer", "u1", body); w.Code != 200 {
		t.Errorf("after window: %d", w.Code)
	}
	if w := post(u, "organizer", "u1", body); w.Code != 429 {
		t.Errorf("window still full: %d", w.Code)
	}
	now = t0.Add(5 * time.Hour)
	post(u, "organizer", "u3", body)
	u.mu.Lock()
	n := len(u.hits)
	u.mu.Unlock()
	if n != 1 {
		t.Errorf("hits not swept: %d users", n)
	}
}

func TestCleanStaleTemp(t *testing.T) {
	u := newU(t, nil)
	dir := filepath.Join(u.Dir, ".tmp")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "old"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "new"), []byte("x"), 0o644)
	os.Chtimes(filepath.Join(dir, "old"), t0.Add(-2*time.Hour), t0.Add(-2*time.Hour))
	os.Chtimes(filepath.Join(dir, "new"), t0.Add(-time.Minute), t0.Add(-time.Minute))
	u.CleanStaleTemp()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "new" {
		t.Errorf("got %v", entries)
	}
}
