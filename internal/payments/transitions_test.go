package payments

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"

	"hectix/backend/internal/testdb"
)

func TestExpireReleasesStockAndVoucherOnce(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := ApplyStatus(ctx, db, id, "expired"); err != nil {
			t.Fatal(err)
		}
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
}

func TestCancelReleasesStock(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update ticket_type set sold=5 where id='tt1'`)
	if err := ApplyStatus(context.Background(), db, id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "cancelled" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 3 {
		t.Fatalf("sold=%d", n)
	}
}

func TestReleaseSkipsDeletedVoucher(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update voucher set deleted_at=now() where id='v1'`)
	if err := ApplyStatus(context.Background(), db, id, "expired"); err != nil {
		t.Fatal(err)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 1 {
		t.Fatalf("usage=%d", n)
	}
}

func TestReleaseWithoutVoucherLeavesVouchers(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	exec(t, db, `update "order" set voucher_code=null, voucher_discount=0 where id=$1`, id)
	if err := ApplyStatus(context.Background(), db, id, "expired"); err != nil {
		t.Fatal(err)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 1 {
		t.Fatalf("usage=%d", n)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 0 {
		t.Fatalf("sold=%d", n)
	}
}

func TestPaidMintsTicketsOnceAndEnqueuesEticket(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, true)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := ApplyStatus(ctx, db, id, "paid"); err != nil {
			t.Fatal(err)
		}
	}
	if n := one[int](t, db, `select count(*) from ticket where order_id=$1`, id); n != 2 {
		t.Fatalf("tickets=%d", n)
	}
	ref := one[string](t, db, `select reference_number from "order" where id=$1`, id)
	if !regexp.MustCompile(`^HX-\d{4}-\d{4}-\d{4}$`).MatchString(ref) {
		t.Fatalf("ref=%s", ref)
	}
	if n := one[int](t, db, `select count(*) from job where kind='eticket.send'`); n != 1 {
		t.Fatalf("eticket jobs=%d", n)
	}
	if p := one[string](t, db, `select payload->>'orderId' from job where kind='eticket.send'`); p != id {
		t.Fatalf("payload orderId=%s", p)
	}
	if name := one[string](t, db, `select attendee_name from ticket limit 1`); name != "Budi" {
		t.Fatal(name)
	}
	re := regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`)
	for _, q := range []string{`select barcode from "order" where id='ord1'`, `select barcode from ticket limit 1`} {
		if b := one[string](t, db, q); !re.MatchString(b) {
			t.Fatalf("barcode=%q", b)
		}
	}
	if n := one[int](t, db, `select count(*) from ticket where status='valid' and ticket_number ~ '^HX-\d{4}-\d{4}-\d{4}$'`); n != 2 {
		t.Fatalf("valid HX tickets=%d", n)
	}
}

func TestPaidAlwaysEnqueuesEticket(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	if err := ApplyStatus(context.Background(), db, id, "paid"); err != nil {
		t.Fatal(err)
	}
	if n := one[int](t, db, `select count(*) from ticket`); n != 2 {
		t.Fatalf("tickets=%d", n)
	}
	if n := one[int](t, db, `select count(*) from job where kind='eticket.send'`); n != 1 {
		t.Fatalf("jobs=%d", n)
	}
}

func TestPaidDoesNotResurrectCancelled(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, true)
	ctx := context.Background()
	if err := ApplyStatus(ctx, db, id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	err := ApplyStatus(ctx, db, id, "paid")
	slog.SetDefault(prev)
	if err != nil {
		t.Fatal(err)
	}
	if log := buf.String(); !strings.Contains(log, "level=ERROR") || !strings.Contains(log, "paid after expiry") ||
		!strings.Contains(log, "order=ord1") || !strings.Contains(log, "status=cancelled") {
		t.Fatalf("log: %q", log)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "cancelled" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select count(*) from ticket`); n != 0 {
		t.Fatalf("tickets=%d", n)
	}
	if n := one[int](t, db, `select count(*) from job`); n != 0 {
		t.Fatalf("jobs=%d", n)
	}
}

func TestExpireDoesNotTouchPaid(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	ctx := context.Background()
	if err := ApplyStatus(ctx, db, id, "paid"); err != nil {
		t.Fatal(err)
	}
	if err := ApplyStatus(ctx, db, id, "expired"); err != nil {
		t.Fatal(err)
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "paid" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 2 {
		t.Fatalf("sold=%d", n)
	}
}

func TestUnknownStatusErrors(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, false)
	if err := ApplyStatus(context.Background(), db, id, "refunded"); err == nil {
		t.Fatal("want error")
	}
	if s := one[string](t, db, `select status from "order" where id=$1`, id); s != "pending_payment" {
		t.Fatal(s)
	}
}

func TestConcurrentPaidMintsOnce(t *testing.T) {
	db := testdb.New(t)
	id := seedOrder(t, db, true)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = ApplyStatus(ctx, db, id, "paid")
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := one[int](t, db, `select count(*) from ticket where order_id=$1`, id); n != 2 {
		t.Fatalf("tickets=%d", n)
	}
	if n := one[int](t, db, `select count(*) from job where kind='eticket.send'`); n != 1 {
		t.Fatalf("eticket jobs=%d", n)
	}
}
