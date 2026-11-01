package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/dbx"
)

type Handler func(ctx context.Context, payload []byte) error

func Enqueue(ctx context.Context, db dbx.DB, kind string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `insert into job(kind, payload) values($1, $2)`, kind, b)
	return err
}

type Worker struct {
	DB       *pgxpool.Pool
	Handlers map[string]Handler
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt*attempt) * 10 * time.Second
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

func (w *Worker) RunOne(ctx context.Context) (bool, error) {
	var (
		id                 int64
		kind               string
		payload            []byte
		attempts, maxTries int
	)
	err := w.DB.QueryRow(ctx, `
		update job set status='running', attempts=attempts+1, updated_at=now()
		where id = (select id from job where status='pending' and run_at <= now()
		            order by run_at for update skip locked limit 1)
		returning id, kind, payload, attempts, max_attempts`).Scan(&id, &kind, &payload, &attempts, &maxTries)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	h, ok := w.Handlers[kind]
	var runErr error
	if !ok {
		runErr = fmt.Errorf("no handler for %q", kind)
	} else {
		runErr = safeRun(ctx, h, payload)
	}

	ctx = context.WithoutCancel(ctx)
	if runErr == nil {
		_, err = w.DB.Exec(ctx, `update job set status='done', last_error=null, updated_at=now() where id=$1`, id)
		return true, err
	}
	log.Printf("job %d %s attempt %d failed: %v", id, kind, attempts, runErr)
	if attempts >= maxTries {
		_, err = w.DB.Exec(ctx, `update job set status='failed', last_error=$1, updated_at=now() where id=$2`, runErr.Error(), id)
	} else {
		_, err = w.DB.Exec(ctx, `update job set status='pending', last_error=$1,
			run_at=now() + make_interval(secs => $2), updated_at=now() where id=$3`,
			runErr.Error(), backoff(attempts).Seconds(), id)
	}
	return true, err
}

func safeRun(ctx context.Context, h Handler, p []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	return h(ctx, p)
}

const staleAfter = "10 minutes"

func (w *Worker) RecoverStale(ctx context.Context) (int64, error) {
	tag, err := w.DB.Exec(ctx, `
		update job set
			status = case when attempts >= max_attempts then 'failed' else 'pending' end,
			last_error = case when attempts >= max_attempts then 'stale: worker died' else last_error end,
			updated_at = now()
		where status='running' and updated_at < now() - interval '`+staleAfter+`'`)
	return tag.RowsAffected(), err
}

func (w *Worker) Run(ctx context.Context, n int) (wait func()) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			if _, err := w.RecoverStale(ctx); err != nil && ctx.Err() == nil {
				log.Printf("worker: recover stale jobs: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastMsg string
			var lastLog time.Time
			for ctx.Err() == nil {
				ran, err := w.RunOne(ctx)
				if err != nil && ctx.Err() == nil && (err.Error() != lastMsg || time.Since(lastLog) > 30*time.Second) {
					log.Printf("worker: %v", err)
					lastMsg, lastLog = err.Error(), time.Now()
				}
				if !ran || err != nil {
					select {
					case <-ctx.Done():
					case <-time.After(time.Second):
					}
				}
			}
		}()
	}
	return wg.Wait
}
