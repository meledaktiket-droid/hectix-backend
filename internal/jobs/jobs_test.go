package jobs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/testdb"
)

func TestRunsJobAndMarksDone(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, "echo", map[string]string{"x": "1"}); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	w := &Worker{DB: pool, Handlers: map[string]Handler{
		"echo": func(ctx context.Context, p []byte) error { got <- string(p); return nil },
	}}
	ran, err := w.RunOne(ctx)
	if err != nil || !ran {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if p := <-got; p != `{"x": "1"}` && p != `{"x":"1"}` {
		t.Fatalf("payload %s", p)
	}
	var status string
	pool.QueryRow(ctx, `select status from job`).Scan(&status)
	if status != "done" {
		t.Fatalf("status %s", status)
	}
}

func TestFailureRetriesWithBackoffThenFails(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, "boom", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update job set max_attempts = 2`); err != nil {
		t.Fatal(err)
	}
	w := &Worker{DB: pool, Handlers: map[string]Handler{
		"boom": func(context.Context, []byte) error { return errors.New("nope") },
	}}

	if ran, err := w.RunOne(ctx); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	var status, lastErr string
	var delay float64
	if err := pool.QueryRow(ctx, `select status, last_error, extract(epoch from run_at - now())::float8 from job`).
		Scan(&status, &lastErr, &delay); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || lastErr != "nope" || delay < 8 || delay > 10.5 {
		t.Fatalf("after 1st: %s %s delay=%.2fs", status, lastErr, delay)
	}

	if _, err := pool.Exec(ctx, `update job set run_at = now()`); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunOne(ctx); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if err := pool.QueryRow(ctx, `select status from job`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("after 2nd: %s", status)
	}
}

func TestUnknownKindIsAFailure(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, "mystery", struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update job set max_attempts = 2`); err != nil {
		t.Fatal(err)
	}
	w := &Worker{DB: pool}
	var status, lastErr string

	if ran, err := w.RunOne(ctx); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if err := pool.QueryRow(ctx, `select status, last_error from job`).Scan(&status, &lastErr); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || lastErr != `no handler for "mystery"` {
		t.Fatalf("after 1st: %s %q", status, lastErr)
	}

	if _, err := pool.Exec(ctx, `update job set run_at = now()`); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunOne(ctx); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	if err := pool.QueryRow(ctx, `select status from job`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("after 2nd: %s", status)
	}
}

func TestRecoverStale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into job(kind, payload, status, attempts, max_attempts, last_error, updated_at) values
		('retry',     '{}', 'running', 1, 8, 'prev', now() - interval '11 minutes'),
		('exhausted', '{}', 'running', 8, 8, 'prev', now() - interval '11 minutes'),
		('fresh',     '{}', 'running', 8, 8, null,   now())`); err != nil {
		t.Fatal(err)
	}
	n, err := (&Worker{DB: pool}).RecoverStale(ctx)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := map[string][2]string{
		"retry":     {"pending", "prev"},
		"exhausted": {"failed", "stale: worker died"},
		"fresh":     {"running", ""},
	}
	rows, err := pool.Query(ctx, `select kind, status, coalesce(last_error, '') from job`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind, status, lastErr string
		if err := rows.Scan(&kind, &status, &lastErr); err != nil {
			t.Fatal(err)
		}
		if got := [2]string{status, lastErr}; got != want[kind] {
			t.Errorf("%s: got %v want %v", kind, got, want[kind])
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestNothingToRun(t *testing.T) {
	w := &Worker{DB: testdb.New(t)}
	if ran, err := w.RunOne(context.Background()); ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
}

func TestPanicIsRecordedAndRetried(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	Enqueue(ctx, pool, "panics", struct{}{})
	w := &Worker{DB: pool, Handlers: map[string]Handler{
		"panics": func(context.Context, []byte) error { panic("kaboom") },
	}}
	if ran, err := w.RunOne(ctx); !ran || err != nil {
		t.Fatalf("ran=%v err=%v", ran, err)
	}
	var status, lastErr string
	pool.QueryRow(ctx, `select status, last_error from job`).Scan(&status, &lastErr)
	if status != "pending" || !strings.Contains(lastErr, "panic") {
		t.Fatalf("%s %q", status, lastErr)
	}
}

func TestEnqueueIsTransactional(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	rollback := errors.New("rollback")
	err := dbx.Tx(ctx, pool, func(tx pgx.Tx) error {
		if err := Enqueue(ctx, tx, "k", struct{}{}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `select count(*) from job`).Scan(&n)
	if n != 0 {
		t.Fatalf("rolled-back enqueue left %d rows", n)
	}

	if err := dbx.Tx(ctx, pool, func(tx pgx.Tx) error { return Enqueue(ctx, tx, "k", struct{}{}) }); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `select count(*) from job`).Scan(&n)
	if n != 1 {
		t.Fatalf("committed enqueue: %d rows", n)
	}
}

func TestConcurrentWorkersRunEachJobOnce(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	const jobs = 5
	for range jobs {
		Enqueue(ctx, pool, "count", struct{}{})
	}
	var runs atomic.Int32
	w := &Worker{DB: pool, Handlers: map[string]Handler{
		"count": func(context.Context, []byte) error {
			runs.Add(1)
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				ran, err := w.RunOne(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if !ran {
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	var done int
	pool.QueryRow(ctx, `select count(*) from job where status='done'`).Scan(&done)
	if runs.Load() != jobs || done != jobs {
		t.Fatalf("runs=%d done=%d", runs.Load(), done)
	}
}

func TestRunRecoversStaleRunningJobs(t *testing.T) {
	pool := testdb.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Exec(ctx, `insert into job(kind, payload, status, updated_at) values
		('stale', '{}', 'running', now() - interval '11 minutes'),
		('fresh', '{}', 'running', now())`)
	ran := make(chan string, 2)
	h := func(kind string) Handler {
		return func(context.Context, []byte) error { ran <- kind; return nil }
	}
	w := &Worker{DB: pool, Handlers: map[string]Handler{"stale": h("stale"), "fresh": h("fresh")}}
	wait := w.Run(ctx, 1)

	select {
	case k := <-ran:
		if k != "stale" {
			t.Fatalf("ran %s", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale job not recovered")
	}
	cancel()
	wait()

	var fresh string
	pool.QueryRow(context.Background(), `select status from job where kind='fresh'`).Scan(&fresh)
	if fresh != "running" {
		t.Fatalf("fresh running job touched: %s", fresh)
	}
}

func TestShutdownLetsInFlightJobFinish(t *testing.T) {
	pool := testdb.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	Enqueue(ctx, pool, "slow", struct{}{})
	started := make(chan struct{})
	finished := make(chan struct{})
	w := &Worker{DB: pool, Handlers: map[string]Handler{
		"slow": func(ctx context.Context, _ []byte) error {
			close(started)
			time.Sleep(300 * time.Millisecond)
			close(finished)
			return ctx.Err()
		},
	}}
	wait := w.Run(ctx, 2)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job never started")
	}
	cancel()
	wait()

	select {
	case <-finished:
	default:
		t.Fatal("wait returned before in-flight handler finished")
	}
	var status string
	var lastErr *string
	pool.QueryRow(context.Background(), `select status, last_error from job`).Scan(&status, &lastErr)
	if status != "done" || lastErr != nil {
		t.Fatalf("in-flight job not completed on shutdown: %s %v", status, lastErr)
	}
}
