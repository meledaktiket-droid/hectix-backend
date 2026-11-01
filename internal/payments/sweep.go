package payments

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/dbx"
)

func Sweep(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var ids []string
	err := dbx.Tx(ctx, pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `update "order" set status='expired', updated_at=now()
			where status='pending_payment' and expires_at < now() - interval '2 minutes' returning id`)
		if err != nil {
			return err
		}
		if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || len(ids) == 0 {
			return err
		}
		if _, err := tx.Exec(ctx, `select id from ticket_type
			where id in (select ticket_type_id from order_item where order_id = any($1)) order by id for update`, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `select v.id from voucher v
			where (v.event_id, v.code) in (select event_id, voucher_code from "order" where id = any($1) and voucher_code is not null)
			and v.deleted_at is null order by v.id for update`, ids); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update ticket_type tt set sold = greatest(0, tt.sold - agg.qty)
			from (select oi.ticket_type_id, sum(oi.quantity) as qty from order_item oi
			      where oi.order_id = any($1) group by oi.ticket_type_id) agg
			where tt.id = agg.ticket_type_id`, ids); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			update voucher v set usage_count = greatest(0, v.usage_count - cnt.n)
			from (select o.event_id, o.voucher_code, count(*)::int as n from "order" o
			      where o.id = any($1) and o.voucher_code is not null group by o.event_id, o.voucher_code) cnt
			where v.event_id = cnt.event_id and v.code = cnt.voucher_code and v.deleted_at is null`, ids)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(ids), nil
}

func PruneJobs(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `delete from job where status in ('done','failed') and updated_at < now() - interval '30 days'`)
	return tag.RowsAffected(), err
}

func RunSweeper(ctx context.Context, pool *pgxpool.Pool) {
	runSweeper(ctx, pool, time.Minute)
}

func runSweeper(ctx context.Context, pool *pgxpool.Pool, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		sweepTick(ctx, pool)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func sweepTick(ctx context.Context, pool *pgxpool.Pool) {
	if n, err := Sweep(ctx, pool); err != nil {
		slog.Error("sweep pending orders", "err", err)
	} else if n > 0 {
		slog.Info(fmt.Sprintf("swept %d expired pending order(s)", n))
	}
	if _, err := PruneJobs(ctx, pool); err != nil {
		slog.Error("prune jobs", "err", err)
	}
}
