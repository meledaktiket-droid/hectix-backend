package config

import "testing"

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("AUTH_SECRET", "s")
	t.Setenv("GOOGLE_CLIENT_ID", "g")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gs")
	t.Setenv("MIDTRANS_SERVER_KEY", "m")
	t.Setenv("RESEND_API_KEY", "r")
	t.Setenv("WEB_BASE_URL", "https://dev.hectix.id")
	t.Setenv("ORGANIZER_BASE_URL", "https://organizer.dev.hectix.id/")
	t.Setenv("ADMIN_BASE_URL", "http://localhost:3002")
}

func TestLoadMapsHostsToApps(t *testing.T) {
	setRequired(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"dev.hectix.id":           "web",
		"organizer.dev.hectix.id": "organizer",
		"localhost:3002":          "admin",
	}
	for host, name := range cases {
		if got := c.Apps[host].Name; got != name {
			t.Errorf("host %s: got %q want %q", host, got, name)
		}
	}
	if c.Apps["organizer.dev.hectix.id"].BaseURL != "https://organizer.dev.hectix.id" {
		t.Errorf("trailing slash not trimmed")
	}
	if !c.Apps["dev.hectix.id"].Secure || c.Apps["localhost:3002"].Secure {
		t.Errorf("Secure flag wrong")
	}
}

func TestLoadFailsWithoutSecret(t *testing.T) {
	setRequired(t)
	t.Setenv("AUTH_SECRET", "")
	if _, err := Load(); err == nil {
		t.Fatal("want error")
	}
}

func TestAppByName(t *testing.T) {
	setRequired(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	app, ok := c.AppByName("organizer")
	if !ok || app.BaseURL != "https://organizer.dev.hectix.id" {
		t.Fatalf("expected organizer app with BaseURL, got ok=%v app=%+v", ok, app)
	}
	_, okMissing := c.AppByName("unknown")
	if okMissing {
		t.Fatal("expected false for unknown app")
	}
}

func TestLoadOptionalScannerBaseURL(t *testing.T) {
	setRequired(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.AppByName("scanner"); ok {
		t.Fatal("expected scanner app to be absent when SCANNER_BASE_URL is unset")
	}

	t.Setenv("SCANNER_BASE_URL", "https://scanner.hectix.id/")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	app, ok := c.AppByName("scanner")
	if !ok || app.Host != "scanner.hectix.id" || app.BaseURL != "https://scanner.hectix.id" || !app.Secure {
		t.Fatalf("unexpected scanner app: ok=%v app=%+v", ok, app)
	}

	t.Setenv("SCANNER_BASE_URL", "not-a-url")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid SCANNER_BASE_URL")
	}
}
