package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"hectix/backend/internal/testdb"
)

func TestVoucherCodeUniqueOnlyAmongLive(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`insert into organization(id, name, created_at) values('o1', 'Org', now())`)
	mustExec(`insert into event(id, organization_id, name, starts_at, created_at, updated_at)
		values('e1', 'o1', 'Event', now(), now(), now())`)
	insert := `insert into voucher(id, event_id, code, amount, created_at, deleted_at) values($1, 'e1', 'PROMO', 10, now(), $2)`

	mustExec(insert, "v1", "2026-01-01")
	mustExec(insert, "v2", nil)

	_, err := pool.Exec(ctx, insert, "v3", nil)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("want unique violation, got %v", err)
	}
}

func TestJobTableExists(t *testing.T) {
	pool := testdb.New(t)
	var id int64
	err := pool.QueryRow(context.Background(),
		`insert into job(kind, payload) values('k', '{}') returning id`).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
}
