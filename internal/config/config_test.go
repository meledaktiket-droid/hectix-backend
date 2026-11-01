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
