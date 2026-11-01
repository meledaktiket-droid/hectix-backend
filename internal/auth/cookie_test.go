package auth

import (
	"os"
	"strings"
	"testing"
)

func TestSignMatchesBetterCall(t *testing.T) {
	b, err := os.ReadFile("testdata/cookie.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected >=2 fixture lines, got %d", len(lines))
	}
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) != 3 {
			t.Fatalf("bad fixture line %q", line)
		}
		value, secret, want := f[0], f[1], f[2]
		if got := Sign(value, secret); got != want {
			t.Fatalf("sign: got %s want %s", got, want)
		}
		v, ok := Unsign(want, secret)
		if !ok || v != value {
			t.Fatalf("unsign: %q %v", v, ok)
		}
		if _, ok := Unsign(want, "other-secret"); ok {
			t.Fatal("bad secret accepted")
		}
	}
}

func TestUnsignRejects(t *testing.T) {
	signed := Sign("abcDEF123tokenXYZ", "test-secret")
	if _, ok := Unsign("abcDEF123tokenXYZ", "test-secret"); ok {
		t.Fatal("value without '.' accepted")
	}
	tampered := "abcDEF123tokenXYA" + signed[strings.Index(signed, "."):]
	if _, ok := Unsign(tampered, "test-secret"); ok {
		t.Fatal("tampered value accepted")
	}
	if _, ok := Unsign("%zz", "test-secret"); ok {
		t.Fatal("bad escape accepted")
	}
}

func TestUnsignRawPlus(t *testing.T) {
	signed := Sign("abcDEF123tokenXYZ", "test-secret")
	raw := strings.NewReplacer("%2B", "+", "%2F", "/", "%3D", "=").Replace(signed)
	if !strings.Contains(raw, "+") {
		t.Fatal("fixture should contain '+'")
	}
	v, ok := Unsign(raw, "test-secret")
	if !ok || v != "abcDEF123tokenXYZ" {
		t.Fatalf("raw unsign: %q %v", v, ok)
	}
}

func TestCookieName(t *testing.T) {
	if n := CookieName("web", true, "session_token"); n != "__Secure-web.session_token" {
		t.Fatal(n)
	}
	if n := CookieName("admin", false, "session_token"); n != "admin.session_token" {
		t.Fatal(n)
	}
}
