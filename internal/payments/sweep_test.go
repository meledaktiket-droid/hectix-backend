package payments

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/testdb"
)

func addOrder(t *testing.T, db *pgxpool.Pool, id, status, expiresIn string, qty int) {
	t.Helper()
	exec(t, db, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,
		voucher_code,voucher_discount,expires_at,created_at,updated_at)
		values($1,'u1','e1',$2,'qris',0,0,0,0,'VIP10',0,now()+$3::interval,now(),now())`, id, status, expiresIn)
	exec(t, db, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1,$2,'tt1','Reguler',100000,$3)`, "oi-"+id, id, qty)
}

func TestSweepExpiresStaleOrder(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update "order" set expires_at=now()-interval '3 minutes' where id=$1`, id)
	n, err := Sweep(context.Background(), db)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "expired" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 0 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 0 {
		t.Fatalf("usage=%d", n)
	}
	if n, err := Sweep(context.Background(), db); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestSweepLeavesFreshOrder(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update "order" set expires_at=now()-interval '90 seconds' where id=$1`, id)
	n, err := Sweep(context.Background(), db)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "pending_payment" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 2 {
		t.Fatalf("sold=%d", n)
	}
}

func TestSweepSumsAcrossOrders(t *testing.T) {
	db := testdb.New(t)
	seedOrder(t, db, false)
	exec(t, db, `update ticket_type set sold=10 where id='tt1'`)
	exec(t, db, `update voucher set usage_count=5 where id='v1'`)
	addOrder(t, db, "ord2", "pending_payment", "-3 minutes", 3)
	addOrder(t, db, "ord3", "pending_payment", "-2 hours", 4)
	n, err := Sweep(context.Background(), db)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 3 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 3 {
		t.Fatalf("usage=%d", n)
	}
	if s := one[string](t, db, `select status from "order" where id='ord1'`); s != "pending_payment" {
		t.Fatal(s)
	}
}

func TestSweepSkipsPaidPastExpiry(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update "order" set status='paid', expires_at=now()-interval '1 hour' where id=$1`, id)
	n, err := Sweep(context.Background(), db)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "paid" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 2 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 1 {
		t.Fatalf("usage=%d", n)
	}
}

func TestPruneJobs(t *testing.T) {
	db := testdb.New(t)
	exec(t, db, `insert into job(kind,payload,status,updated_at) values
		('old-done','{}','done',now()-interval '31 days'),
		('old-failed','{}','failed',now()-interval '40 days'),
		('old-pending','{}','pending',now()-interval '40 days'),
		('old-running','{}','running',now()-interval '40 days'),
		('new-done','{}','done',now()-interval '29 days'),
		('new-failed','{}','failed',now())`)
	n, err := PruneJobs(context.Background(), db)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if left := one[string](t, db, `select string_agg(kind, ',' order by kind) from job`); left != "new-done,new-failed,old-pending,old-running" {
		t.Fatal(left)
	}
}

func TestRunSweeperSweepsAtStartup(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update "order" set expires_at=now()-interval '3 minutes' where id=$1`, id)
	exec(t, db, `insert into job(kind,payload,status,updated_at) values('old','{}','done',now()-interval '31 days')`)
	stop := startSweeper(t, db, time.Hour)
	defer stop()
	waitFor(t, func() bool {
		return one[string](t, db, `select status from "order" where id=$1`, id) == "expired" &&
			one[int](t, db, `select count(*) from job`) == 0
	})
}

func TestRunSweeperTicks(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	stop := startSweeper(t, db, 50*time.Millisecond)
	defer stop()
	exec(t, db, `update "order" set expires_at=now()-interval '3 minutes' where id=$1`, id)
	waitFor(t, func() bool { return one[string](t, db, `select status from "order" where id=$1`, id) == "expired" })
}

func startSweeper(t *testing.T, db *pgxpool.Pool, every time.Duration) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runSweeper(ctx, db, every); close(done) }()
	return func() { cancel(); <-done }
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 60s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestConcurrentReleaseNoDeadlock(t *testing.T) {
	db := testdb.New(t)
	seedOrder(t, db, false)
	exec(t, db, `insert into ticket_type(id,event_id,name,price,quantity,sold,created_at,updated_at) values('tt0','e1','VIP',1,1000,0,now(),now())`)
	exec(t, db, `update ticket_type set quantity=1000, sold=0`)
	exec(t, db, `update voucher set usage_count=0`)
	exec(t, db, `insert into voucher(id,event_id,code,type,amount,usage_count,created_at) values('v0','e1','AAA','percent',10,0,now())`)
	const n = 60
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("o%02d", i)
		code := []string{"VIP10", "AAA"}[i%2]
		exec(t, db, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,voucher_code,expires_at,created_at,updated_at)
			values($1,'u1','e1','pending_payment','qris',0,0,0,0,$2,now()-interval '1 hour',now(),now())`, id, code)
		first, second := "tt1", "tt0"
		if i%2 == 1 {
			first, second = second, first
		}
		exec(t, db, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1,$2,$3,'x',1,1),($4,$2,$5,'x',1,1)`,
			id+"a", id, first, id+"b", second)
		exec(t, db, `update ticket_type set sold=sold+1 where id in ('tt0','tt1')`)
		exec(t, db, `update voucher set usage_count=usage_count+1 where code=$1`, code)
	}
	ctx := context.Background()
	errs := make(chan error, n+4)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- ApplyStatus(ctx, db, fmt.Sprintf("o%02d", i), []string{"expired", "cancelled"}[i%2])
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Sweep(ctx, db)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s := one[string](t, db, `select string_agg(id||'='||sold, ',' order by id) from ticket_type`); s != "tt0=0,tt1=0" {
		t.Fatal(s)
	}
	if s := one[string](t, db, `select string_agg(code||'='||usage_count, ',' order by code) from voucher`); s != "AAA=0,VIP10=0" {
		t.Fatal(s)
	}
}
