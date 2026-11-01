package payments

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedOrder(t *testing.T, db *pgxpool.Pool, sendEticket bool) string {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`insert into "user"(id,name,email,email_verified,role,created_at,updated_at) values('u1','Budi','budi@example.com',true,'user',now(),now())`,
		`insert into organization(id,name,created_at) values('o1','Org',now())`,
		`insert into event(id,organization_id,name,starts_at,created_at,updated_at) values('e1','o1','Konser',now()+interval '7 days',now(),now())`,
		`insert into ticket_type(id,event_id,name,price,quantity,sold,created_at,updated_at) values('tt1','e1','Reguler',100000,10,2,now(),now())`,
		`insert into voucher(id,event_id,code,type,amount,usage_count,created_at) values('v1','e1','VIP10','percent',10,1,now())`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.Exec(ctx, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,
		voucher_code,voucher_discount,send_eticket,expires_at,created_at,updated_at)
		values('ord1','u1','e1','pending_payment','qris',180000,18000,7120,205120,'VIP10',20000,$1,now()+interval '15 minutes',now(),now())`, sendEticket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values('oi1','ord1','tt1','Reguler',100000,2)`); err != nil {
		t.Fatal(err)
	}
	return "ord1"
}

func one[T any](t *testing.T, db *pgxpool.Pool, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
