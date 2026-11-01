package dbx

import (
	"context"
	"regexp"
	"testing"
	"time"
)

func TestID(t *testing.T) {
	a, b := ID(), ID()
	if !regexp.MustCompile(`^[A-Za-z0-9]{32}$`).MatchString(a) {
		t.Fatalf("bad id %q", a)
	}
	if a == b {
		t.Fatal("ids should differ")
	}
}

func TestUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	for range 100 {
		if u := UUID(); !re.MatchString(u) {
			t.Fatalf("bad uuid %q", u)
		}
	}
}

func TestMigrateRejectsKeyValueDSN(t *testing.T) {
	if err := Migrate("host=x user=y"); err == nil {
		t.Fatal("want error")
	}
}

func TestOpenFailsOnUnreachableDB(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if pool, err := Open(ctx, "postgres://x:y@127.0.0.1:1/z?connect_timeout=1"); err == nil {
		pool.Close()
		t.Fatal("want error")
	}
}
