package orders

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/testdb"
)

type fakeCharger struct {
	mu    sync.Mutex
	err   error
	calls []string
}

func (f *fakeCharger) Charge(_ context.Context, orderID string, total int, method, bank string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s %d %s %s", orderID, total, method, bank))
	if f.err != nil {
		return "", "", f.err
	}
	if method == "qris" {
		return "", "https://qr/" + orderID, nil
	}
	return "8800123", "", nil
}

func exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func one[T any](t *testing.T, db *pgxpool.Pool, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func seed(t *testing.T) (*pgxpool.Pool, *fakeCharger, *Checkout) {
	t.Helper()
	db := testdb.New(t)
	for _, s := range []string{
		`insert into "user"(id,name,email,email_verified,created_at,updated_at) values('u1','Budi','budi@example.com',true,now(),now())`,
		`insert into organization(id,name,created_at) values('o1','Org',now())`,
		`insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values('e1','o1','Konser','published',now()+interval '7 days',now(),now())`,
		`insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values('e2','o1','Lain','published',now()+interval '7 days',now(),now())`,
		`insert into ticket_type(id,event_id,name,price,quantity,created_at,updated_at) values('tt1','e1','Reguler',100000,10,now(),now())`,
		`insert into ticket_type(id,event_id,name,price,quantity,created_at,updated_at) values('tt2','e2','Lain',50000,10,now(),now())`,
		`insert into voucher(id,event_id,code,type,amount,created_at) values('v1','e1','HEMAT','percent',10,now())`,
	} {
		exec(t, db, s)
	}
	fc := &fakeCharger{}
	return db, fc, &Checkout{DB: db, Midtrans: fc}
}

func post(t *testing.T, c *Checkout, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c.create(w, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(body)), "u1")
	return w
}

func expect(t *testing.T, w *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if w.Code != code || (body != "" && w.Body.String() != body+"\n") {
		t.Fatalf("got %d %q, want %d %q", w.Code, w.Body, code, body)
	}
}

func orderID(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var r struct{ OrderID string }
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil || r.OrderID == "" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	return r.OrderID
}

const vaBody = `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":2}],"paymentMethod":"va_bank","bank":"bca","voucherCode":" hemat ","sendEticket":true}`

func TestCheckoutTotalsAndVoucher(t *testing.T) {
	db, fc, c := seed(t)
	w := post(t, c, vaBody)
	expect(t, w, 200, "")
	id := orderID(t, w)
	var (
		status, method, bank, code, va  string
		subtotal, disc, tax, fee, total int
		send                            bool
		qris                            *string
		mins                            float64
	)
	err := db.QueryRow(context.Background(), `select status,payment_method,bank,voucher_code,va_number,qris_url,subtotal,voucher_discount,tax,service_fee,total,send_eticket,
		extract(epoch from expires_at-created_at)/60 from "order" where id=$1 and user_id='u1' and event_id='e1'`, id).
		Scan(&status, &method, &bank, &code, &va, &qris, &subtotal, &disc, &tax, &fee, &total, &send, &mins)
	if err != nil {
		t.Fatal(err)
	}
	if status != "pending_payment" || method != "va_bank" || bank != "bca" || code != "HEMAT" || va != "8800123" || qris != nil || !send {
		t.Fatal(status, method, bank, code, va, qris, send)
	}
	if subtotal != 180000 || disc != 20000 || tax != 18000 || fee != 7120 || total != 205120 {
		t.Fatal(subtotal, disc, tax, fee, total)
	}
	if mins < 14.9 || mins > 15.1 {
		t.Fatalf("expires in %v min", mins)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 2 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 1 {
		t.Fatalf("usage=%d", n)
	}
	if s := one[string](t, db, `select ticket_type_name||':'||price||':'||quantity from order_item where order_id=$1`, id); s != "Reguler:100000:2" {
		t.Fatal(s)
	}
	if len(fc.calls) != 1 || fc.calls[0] != id+" 205120 va_bank bca" {
		t.Fatal(fc.calls)
	}
}

func TestCheckoutQRISAndSettings(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `insert into site_setting(key,value,updated_at) values('service_fee','4000',now()),('tax_percent','7',now())`)
	w := post(t, c, `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris","bank":"bca","sendEticket":false}`)
	id := orderID(t, w)
	if s := one[string](t, db, `select coalesce(va_number,'-')||' '||qris_url||' '||coalesce(bank,'-')||' '||tax||' '||total from "order" where id=$1`, id); s != "- https://qr/"+id+" - 7000 111000" {
		t.Fatal(s)
	}
}

func TestCheckoutDuplicateItemsReservedSeparately(t *testing.T) {
	db, _, c := seed(t)
	w := post(t, c, `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1},{"ticketTypeId":"tt1","quantity":2}],"paymentMethod":"qris","sendEticket":false}`)
	id := orderID(t, w)
	if n := one[int](t, db, `select count(*) from order_item where order_id=$1`, id); n != 2 {
		t.Fatalf("items=%d", n)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 3 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select subtotal from "order" where id=$1`, id); n != 300000 {
		t.Fatalf("subtotal=%d", n)
	}
}

func TestCheckoutValidation(t *testing.T) {
	db, fc, c := seed(t)
	exec(t, db, `insert into event(id,organization_id,name,status,starts_at,ends_at,created_at,updated_at) values('past','o1','Lalu','published',now()-interval '2 days',now()-interval '1 day',now(),now())`)
	exec(t, db, `insert into event(id,organization_id,name,status,starts_at,created_at,updated_at) values('started','o1','Mulai','published',now()-interval '1 minute',now(),now())`)
	exec(t, db, `insert into event(id,organization_id,name,status,visibility,starts_at,ends_at,created_at,updated_at) values('ongoing','o1','Jalan','published','private',now()-interval '1 hour',now()+interval '1 hour',now(),now())`)
	exec(t, db, `insert into event(id,organization_id,name,starts_at,created_at,updated_at) values('draft','o1','Draf',now()+interval '7 days',now(),now())`)
	exec(t, db, `insert into event(id,organization_id,name,status,starts_at,deleted_at,created_at,updated_at) values('gone','o1','Hapus','published',now()+interval '7 days',now(),now(),now())`)
	exec(t, db, `insert into ticket_type(id,event_id,name,price,quantity,created_at,updated_at) values('ttd','draft','X',1000,10,now(),now()),('ttg','gone','X',1000,10,now(),now())`)
	exec(t, db, `insert into ticket_type(id,event_id,name,price,quantity,created_at,updated_at) values('tt3','ongoing','X',1000,10,now(),now())`)
	cases := []struct {
		name, body string
		code       int
		want       string
	}{
		{"bad json", `{`, 400, `{"error":"Invalid body"}`},
		{"bad method on missing event", `{"eventId":"nope","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"x"}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"no items", `{"eventId":"e1","items":[],"paymentMethod":"qris"}`, 400, `{"error":"No items"}`},
		{"missing items", `{"eventId":"e1","paymentMethod":"qris"}`, 400, `{"error":"No items"}`},
		{"negative qty", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":-1}],"paymentMethod":"qris"}`, 400, `{"error":"Invalid quantity"}`},
		{"zero qty", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":0}],"paymentMethod":"qris"}`, 400, `{"error":"Invalid quantity"}`},
		{"fractional qty", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1.5}],"paymentMethod":"qris"}`, 400, `{"error":"Invalid quantity"}`},
		{"missing event", `{"eventId":"nope","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris"}`, 404, `{"error":"Acara tidak ditemukan"}`},
		{"ended event", `{"eventId":"past","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris"}`, 400, `{"error":"Acara telah berakhir, tiket tidak bisa dibeli."}`},
		{"started, no end", `{"eventId":"started","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris"}`, 400, `{"error":"Acara telah berakhir, tiket tidak bisa dibeli."}`},
		{"other event's ticket type", `{"eventId":"e1","items":[{"ticketTypeId":"tt2","quantity":1}],"paymentMethod":"qris"}`, 400, `{"error":"Ticket type not found"}`},
		{"unknown ticket type", `{"eventId":"e1","items":[{"ticketTypeId":"zzz","quantity":1}],"paymentMethod":"qris"}`, 400, `{"error":"Ticket type not found"}`},
		{"draft event", `{"eventId":"draft","items":[{"ticketTypeId":"ttd","quantity":1}],"paymentMethod":"qris"}`, 404, `{"error":"Acara tidak ditemukan"}`},
		{"deleted event", `{"eventId":"gone","items":[{"ticketTypeId":"ttg","quantity":1}],"paymentMethod":"qris"}`, 404, `{"error":"Acara tidak ditemukan"}`},
		{"missing method", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}]}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"unknown method", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"cash"}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"va without bank", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"va_bank"}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"va unknown bank", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"va_bank","bank":"permata"}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"va uppercase bank", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"va_bank","bank":"BCA"}`, 400, `{"error":"Metode pembayaran tidak valid"}`},
		{"over stock", `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":11}],"paymentMethod":"qris"}`, 409, `{"error":"Tiket habis"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { expect(t, post(t, c, tc.body), tc.code, tc.want) })
	}
	if n := one[int](t, db, `select count(*) from "order"`); n != 0 {
		t.Fatalf("orders=%d", n)
	}
	if n := one[int](t, db, `select sum(sold) from ticket_type`); n != 0 {
		t.Fatalf("sold=%d", n)
	}
	if len(fc.calls) != 0 {
		t.Fatal(fc.calls)
	}
	orderID(t, post(t, c, `{"eventId":"ongoing","items":[{"ticketTypeId":"tt3","quantity":1}],"paymentMethod":"qris"}`))
}

func TestCheckoutConcurrentBuyers(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `update ticket_type set quantity=5 where id='tt1'`)
	codes := make([]int, 20)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			c.create(w, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(
				`{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris"}`)), "u1")
			codes[i] = w.Code
		}()
	}
	wg.Wait()
	got := map[int]int{}
	for _, code := range codes {
		got[code]++
	}
	if got[200] != 5 || got[409] != 15 {
		t.Fatalf("codes=%v", got)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 5 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select count(*) from "order"`); n != 5 {
		t.Fatalf("orders=%d", n)
	}
}

func TestCheckoutChargeFailureReleases(t *testing.T) {
	db, fc, c := seed(t)
	fc.err = errors.New("midtrans down")
	expect(t, post(t, c, vaBody), 502, `{"error":"Gagal membuat pembayaran"}`)
	if s := one[string](t, db, `select status from "order"`); s != "cancelled" {
		t.Fatal(s)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 0 {
		t.Fatalf("sold=%d", n)
	}
	if n := one[int](t, db, `select usage_count from voucher where id='v1'`); n != 0 {
		t.Fatalf("usage=%d", n)
	}
}

func TestCheckoutVoucherRules(t *testing.T) {
	cases := []struct {
		name, setup string
		code        string
		disc, usage int
	}{
		{"expired", `update voucher set expires_at=now()-interval '1 minute'`, "", 0, 0},
		{"limit reached", `update voucher set usage_limit=3, usage_count=3`, "", 0, 3},
		{"under limit", `update voucher set usage_limit=3, usage_count=2`, "HEMAT", 20000, 3},
		{"limit 0 is unlimited", `update voucher set usage_limit=0, usage_count=99`, "HEMAT", 20000, 100},
		{"deleted", `update voucher set deleted_at=now()`, "", 0, 0},
		{"flat amount", `update voucher set type='flat', amount=5000`, "HEMAT", 5000, 1},
		{"not yet expired", `update voucher set expires_at=now()+interval '1 day'`, "HEMAT", 20000, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, _, c := seed(t)
			exec(t, db, tc.setup)
			id := orderID(t, post(t, c, vaBody))
			if s := one[string](t, db, `select coalesce(voucher_code,'') from "order" where id=$1`, id); s != tc.code {
				t.Fatalf("code=%q", s)
			}
			if n := one[int](t, db, `select voucher_discount from "order" where id=$1`, id); n != tc.disc {
				t.Fatalf("disc=%d", n)
			}
			if n := one[int](t, db, `select usage_count from voucher`); n != tc.usage {
				t.Fatalf("usage=%d", n)
			}
		})
	}
}

func TestCheckoutVoucherOtherEventIgnored(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `insert into voucher(id,event_id,code,type,amount,created_at) values('v2','e2','LAIN','percent',50,now())`)
	id := orderID(t, post(t, c, `{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris","voucherCode":"LAIN"}`))
	if n := one[int](t, db, `select voucher_discount from "order" where id=$1`, id); n != 0 {
		t.Fatalf("disc=%d", n)
	}
}

func TestCheckoutVoucherLimitUnderConcurrency(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `update voucher set usage_limit=3`)
	exec(t, db, `update ticket_type set quantity=100 where id='tt1'`)
	type res struct {
		code int
		body string
	}
	out := make([]res, 10)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			c.create(w, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(
				`{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris","voucherCode":"HEMAT"}`)), "u1")
			out[i] = res{w.Code, w.Body.String()}
		}()
	}
	wg.Wait()
	ok, full := 0, 0
	for _, r := range out {
		switch {
		case r.code == 200:
			ok++
		case r.code == 409 && r.body == "{\"error\":\"Kuota voucher habis\"}\n":
			full++
		default:
			t.Fatalf("unexpected %d %q", r.code, r.body)
		}
	}
	withVoucher := one[int](t, db, `select count(*) from "order" where voucher_code='HEMAT' and voucher_discount=10000`)
	without := one[int](t, db, `select count(*) from "order" where voucher_code is null and voucher_discount=0`)
	if withVoucher != 3 || ok != 3+without || ok+full != 10 {
		t.Fatalf("withVoucher=%d without=%d ok=%d full=%d", withVoucher, without, ok, full)
	}
	if full == 0 {
		t.Log("no request raced past the initial read this run")
	}
	if n := one[int](t, db, `select usage_count from voucher`); n != 3 {
		t.Fatalf("usage=%d", n)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != ok {
		t.Fatalf("sold=%d", n)
	}
}

func TestCheckoutFlatDiscountCappedAtSubtotal(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `update voucher set type='flat', amount=500000`)
	id := orderID(t, post(t, c, vaBody))
	var subtotal, disc, tax, total int
	if err := db.QueryRow(context.Background(), `select subtotal, voucher_discount, tax, total from "order" where id=$1`, id).
		Scan(&subtotal, &disc, &tax, &total); err != nil {
		t.Fatal(err)
	}
	if subtotal != 0 || disc != 200000 || tax != 0 || total != 7120 {
		t.Fatal(subtotal, disc, tax, total)
	}
}

func TestCheckoutOppositeItemOrderNoDeadlock(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `insert into ticket_type(id,event_id,name,price,quantity,created_at,updated_at) values('tt1b','e1','VIP',200000,100,now(),now())`)
	exec(t, db, `update ticket_type set quantity=100 where id='tt1'`)
	bodies := []string{
		`{"eventId":"e1","items":[{"ticketTypeId":"tt1","quantity":1},{"ticketTypeId":"tt1b","quantity":1}],"paymentMethod":"qris"}`,
		`{"eventId":"e1","items":[{"ticketTypeId":"tt1b","quantity":1},{"ticketTypeId":"tt1","quantity":1}],"paymentMethod":"qris"}`,
	}
	codes := make([]int, 40)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			c.create(w, httptest.NewRequest(http.MethodPost, "/api/orders", strings.NewReader(bodies[i%2])), "u1")
			codes[i] = w.Code
		}()
	}
	wg.Wait()
	for i, code := range codes {
		if code != 200 {
			t.Fatalf("request %d: %d", i, code)
		}
	}
	if n := one[int](t, db, `select sum(sold) from ticket_type`); n != 80 {
		t.Fatalf("sold=%d", n)
	}
}

func TestCheckoutVoucherLimitHitInsideTx(t *testing.T) {
	db, _, c := seed(t)
	exec(t, db, `update voucher set usage_limit=1`)
	exec(t, db, `create function fill_voucher() returns trigger language plpgsql as $$
		begin update voucher set usage_count = usage_limit where id='v1'; return new; end $$`)
	exec(t, db, `create trigger fill before insert on "order" for each row execute function fill_voucher()`)
	expect(t, post(t, c, vaBody), 409, `{"error":"Kuota voucher habis"}`)
	if n := one[int](t, db, `select count(*) from "order"`); n != 0 {
		t.Fatalf("orders=%d", n)
	}
	if n := one[int](t, db, `select sold from ticket_type where id='tt1'`); n != 0 {
		t.Fatalf("sold=%d", n)
	}
}
