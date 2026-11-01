package httpx

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"hectix/backend/internal/testdb"
)

func TestTimeMarshalMillis(t *testing.T) {
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 10, 2, 10, 17, 14, 0, time.UTC), `"2026-10-02T10:17:14.000Z"`},
		{time.Date(2026, 10, 2, 10, 17, 14, 264987000, time.UTC), `"2026-10-02T10:17:14.264Z"`},
		{time.Date(2026, 10, 2, 17, 17, 14, 50000000, time.FixedZone("WIB", 7*3600)), `"2026-10-02T10:17:14.050Z"`},
	}
	for _, c := range cases {
		b, err := json.Marshal(Time{c.in})
		if err != nil || string(b) != c.want {
			t.Errorf("%v: got %s %v, want %s", c.in, b, err, c.want)
		}
	}
	b, _ := json.Marshal(struct {
		P *Time `json:"p"`
	}{})
	if string(b) != `{"p":null}` {
		t.Errorf("nil pointer: got %s", b)
	}
}

func TestTimeScansFromPgx(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var ts Time
	var nullTS, setTS *Time
	err := pool.QueryRow(ctx, `select '2026-10-02 10:17:14.264987'::timestamp, null::timestamp, '2026-10-02 10:17:14'::timestamp`).
		Scan(&ts, &nullTS, &setTS)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(ts); string(b) != `"2026-10-02T10:17:14.264Z"` {
		t.Errorf("ts: %s", b)
	}
	if nullTS != nil {
		t.Errorf("null: got %v", nullTS)
	}
	if setTS == nil {
		t.Fatal("setTS nil")
	}
	if b, _ := json.Marshal(setTS); string(b) != `"2026-10-02T10:17:14.000Z"` {
		t.Errorf("setTS: %s", b)
	}

	var tz Time
	if err := pool.QueryRow(ctx, `select $1::timestamptz`, ts).Scan(&tz); err != nil {
		t.Fatal(err)
	}
	if !tz.Equal(ts.Time) {
		t.Errorf("roundtrip: %v != %v", tz, ts)
	}
}
