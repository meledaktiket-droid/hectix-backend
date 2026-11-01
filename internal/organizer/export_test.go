package organizer

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hectix/backend/internal/config"
)

func TestBuildFilterLabel(t *testing.T) {
	cases := []struct {
		q, event, want string
	}{
		{"", "", "All orders"},
		{"status=paid&from=2026-05-01&to=2026-05-31", "", "Paid · 1 May 2026 – 31 May 2026"},
		{"status=pending_payment&from=2026-05-01&q=ana", "Konser", `Pending · Konser · From 1 May 2026 · "ana"`},
		{"to=2026-12-09", "", "Until 9 Dec 2026"},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		if got := buildFilterLabel(parseOrderParams(q), c.event); got != c.want {
			t.Errorf("%q: got %q want %q", c.q, got, c.want)
		}
	}
}

var pngLogo = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 20)...)

func TestExportOrders(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/orders/export", nil)

	rec := f.do(t, "GET", "/api/orders/export", nil, f.member)
	expect(t, rec, 200, "")
	stamp := time.Now().UTC().Format("2006-01-02")
	if rec.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="orders-`+stamp+`.xlsx"` ||
		!bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatalf("xlsx headers %v", rec.Header())
	}

	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 100, 0, 0)
	f.ticket(t, "tx", "x1", 1000, 100, 0, 0)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 2, "2026-05-01")
	f.order(t, "o2", "e1", "buyer", "expired", "t1", 1000, 1, "2026-05-02")
	f.order(t, "ox", "x1", "buyer", "paid", "tx", 1000, 1, "2026-05-02")
	f.Exec(t, `update "user" set name = 'Buyer Unik' where id = 'buyer'`)

	rec = f.do(t, "GET", "/api/orders/export?format=pdf&status=paid", nil, f.member)
	expect(t, rec, 200, "")
	if rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="orders-`+stamp+`.pdf"` ||
		!bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF")) {
		t.Fatalf("pdf headers %v", rec.Header())
	}
	rec = f.do(t, "GET", "/api/orders/export?format=xlsx", nil, f.member)
	expect(t, rec, 200, "")
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("PK")) {
		t.Fatal("not xlsx")
	}
}

func TestLogoDataURL(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "public", "org"), 0o755)
	os.WriteFile(filepath.Join(dir, "public", "org", "logo.png"), pngLogo, 0o644)
	os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o644)
	h := &handlers{uploadDir: dir, apps: map[string]config.App{"organizer.test": {Name: "organizer"}}}
	ctx := context.Background()
	s := func(v string) *string { return &v }

	got := h.logoDataURL(ctx, s("https://organizer.test/uploads/org/logo.png"))
	if got == nil || !strings.HasPrefix(*got, "data:image/png;base64,") {
		t.Fatalf("local logo %v", got)
	}
	for _, bad := range []string{"https://organizer.test/uploads/../secret", "https://organizer.test/uploads/missing.png",
		"file:///etc/passwd", "", "not a url"} {
		if got := h.logoDataURL(ctx, s(bad)); got != nil {
			t.Errorf("%q: got %v", bad, *got)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngLogo)
	}))
	defer srv.Close()
	if got := h.logoDataURL(ctx, s(srv.URL+"/logo.png")); got != nil {
		t.Fatal("loopback fetch allowed")
	}
	h.logoClient = srv.Client()
	if got := h.logoDataURL(ctx, s(srv.URL+"/logo.png")); got == nil || !strings.HasPrefix(*got, "data:image/png;base64,") {
		t.Fatalf("remote logo %v", got)
	}
}

func TestPublicIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "0.1.2.3",
		"100.64.0.1", "100.127.255.255", "198.18.0.1", "198.19.255.255", "240.0.0.1", "255.255.255.255", "224.0.0.1",
		"239.255.255.250", "192.0.0.8", "::1", "::", "fe80::1", "fc00::1", "fd12::1", "ff02::1", "ff05::2",
		"64:ff9b::a9fe:a9fe", "64:ff9b::7f00:1", "2002:7f00:1::1", "2002:a9fe:a9fe::", "::ffff:127.0.0.1", "::ffff:10.0.0.1"}
	for _, s := range blocked {
		if publicIP(net.ParseIP(s)) {
			t.Errorf("%s allowed", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "100.128.0.1", "198.20.0.1", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if !publicIP(net.ParseIP(s)) {
			t.Errorf("%s blocked", s)
		}
	}
}
