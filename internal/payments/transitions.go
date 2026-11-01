package payments

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/jobs"
)

func hxNumber(year int) string {
	n := func() int64 {
		v, err := rand.Int(rand.Reader, big.NewInt(10000))
		if err != nil {
			panic(err)
		}
		return v.Int64()
	}
	return fmt.Sprintf("HX-%d-%04d-%04d", year, n(), n())
}

func barcode() string {
	b := make([]byte, 12)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func Release(ctx context.Context, tx pgx.Tx, orderID string) error {
	if _, err := tx.Exec(ctx, `select id from ticket_type
		where id in (select ticket_type_id from order_item where order_id=$1) order by id for update`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `select v.id from voucher v join "order" o on v.event_id=o.event_id and v.code=o.voucher_code
		where o.id=$1 and v.deleted_at is null order by v.id for update of v`, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		update ticket_type tt set sold = greatest(0, tt.sold - oi.qty)
		from (select ticket_type_id, sum(quantity) as qty from order_item where order_id=$1 group by ticket_type_id) oi
		where tt.id = oi.ticket_type_id`, orderID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		update voucher v set usage_count = greatest(0, v.usage_count - 1)
		from "order" o
		where o.id=$1 and o.voucher_code is not null and v.event_id=o.event_id and v.code=o.voucher_code and v.deleted_at is null`, orderID)
	return err
}

func ApplyStatus(ctx context.Context, db *pgxpool.Pool, orderID, target string) error {
	switch target {
	case "paid":
		return dbx.Tx(ctx, db, func(tx pgx.Tx) error { return markPaid(ctx, tx, orderID) })
	case "expired", "cancelled":
		return dbx.Tx(ctx, db, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `update "order" set status=$1, updated_at=now() where id=$2 and status='pending_payment'`, target, orderID)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			return Release(ctx, tx, orderID)
		})
	default:
		return fmt.Errorf("payments: unknown order status %q", target)
	}
}

func markPaid(ctx context.Context, tx pgx.Tx, orderID string) error {
	tag, err := tx.Exec(ctx, `update "order" set status='paid', updated_at=now() where id=$1 and status in ('pending_payment','paid')`, orderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var status string
		err := tx.QueryRow(ctx, `select status from "order" where id=$1`, orderID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status == "expired" || status == "cancelled" {
			slog.Error("paid after expiry", "order", orderID, "status", status)
		}
		return nil
	}
	var exists bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from ticket where order_id=$1)`, orderID).Scan(&exists); err != nil || exists {
		return err
	}
	year := time.Now().UTC().Year()
	if _, err := tx.Exec(ctx, `update "order" set reference_number=$1, barcode=$2 where id=$3 and reference_number is null`,
		hxNumber(year), barcode(), orderID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `select oi.id, oi.ticket_type_id, oi.quantity, o.event_id, u.name
		from order_item oi join "order" o on o.id=oi.order_id join "user" u on u.id=o.user_id where oi.order_id=$1`, orderID)
	if err != nil {
		return err
	}
	type item struct {
		ID, TicketTypeID string
		Qty              int
		EventID, Buyer   string
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
		var i item
		return i, r.Scan(&i.ID, &i.TicketTypeID, &i.Qty, &i.EventID, &i.Buyer)
	})
	if err != nil {
		return err
	}
	for _, it := range items {
		for range it.Qty {
			if _, err := tx.Exec(ctx, `insert into ticket(id,order_id,order_item_id,event_id,ticket_type_id,ticket_number,barcode,status,attendee_name,created_at)
				values($1,$2,$3,$4,$5,$6,$7,'valid',$8,now())`,
				dbx.UUID(), orderID, it.ID, it.EventID, it.TicketTypeID, hxNumber(year), barcode(), it.Buyer); err != nil {
				return err
			}
		}
	}
	var send bool
	if err := tx.QueryRow(ctx, `select eticket_sent_at is null from "order" where id=$1`, orderID).Scan(&send); err != nil {
		return err
	}
	if send {
		return jobs.Enqueue(ctx, tx, "eticket.send", map[string]string{"orderId": orderID})
	}
	return nil
}
