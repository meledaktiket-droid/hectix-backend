package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOrder(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	seedOrder(t, env, "ord1", "u1", "e1", "paid", 2)
	assertError(t, env.Do(t, "GET", "/api/orders/ord1", nil, nil), 401, "Unauthorized")
	assertError(t, env.Do(t, "GET", "/api/orders/ord1", nil, env.Login(t, "u2", nil)), 404, "Not found")
	assertError(t, env.Do(t, "GET", "/api/orders/nope", nil, env.Login(t, "u1", nil)), 404, "Not found")

	rec := env.Do(t, "GET", "/api/orders/ord1", nil, env.Login(t, "u1", nil))
	assertGolden(t, rec, "orders-{orderId}.json")
	got := decode[map[string]any](t, rec)
	if got["total"] != 211000.0 || got["eventName"] != "Konser Senja" || len(got["items"].([]any)) != 1 || got["sendEticket"] != true {
		t.Fatalf("got %s", rec.Body)
	}
}

func TestCancelOrder(t *testing.T) {
	env, fc := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into voucher(id,event_id,code,type,amount,usage_count,created_at) values('v1','e1','HEMAT','percent',10,1,now())`)
	seedOrder(t, env, "ord1", "u1", "e1", "pending_payment", 2)
	env.Exec(t, `update "order" set voucher_code='HEMAT' where id='ord1'`)
	seedOrder(t, env, "paid1", "u1", "e1", "paid", 1)
	u1 := env.Login(t, "u1", nil)

	assertError(t, env.Do(t, "DELETE", "/api/orders/ord1", nil, nil), 401, "Unauthorized")
	assertError(t, env.Do(t, "DELETE", "/api/orders/ord1", nil, env.Login(t, "u2", nil)), 404, "Not found")
	assertError(t, env.Do(t, "DELETE", "/api/orders/paid1", nil, u1), 400, "Cannot cancel")
	if len(fc.calls) != 0 {
		t.Fatalf("midtrans called: %v", fc.calls)
	}

	fc.err = errors.New("412")
	assertError(t, env.Do(t, "DELETE", "/api/orders/ord1", nil, u1), 409, "Pesanan tidak bisa dibatalkan, mungkin sudah dibayar.")
	if s := one[string](t, env, `select status from "order" where id='ord1'`); s != "pending_payment" {
		t.Fatalf("status after midtrans failure: %s", s)
	}

	fc.err = nil
	if rec := env.Do(t, "DELETE", "/api/orders/ord1", nil, u1); rec.Code != 200 || rec.Body.String() != "{\"ok\":true}\n" {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if s := one[string](t, env, `select status from "order" where id='ord1'`); s != "cancelled" {
		t.Fatalf("status %s", s)
	}
	if sold := one[int](t, env, `select sold from ticket_type where id='tt1'`); sold != 0 {
		t.Fatalf("sold %d", sold)
	}
	if n := one[int](t, env, `select usage_count from voucher where id='v1'`); n != 0 {
		t.Fatalf("voucher usage %d", n)
	}
	if len(fc.calls) != 2 || fc.calls[1] != "ord1" {
		t.Fatalf("calls %v", fc.calls)
	}
	assertError(t, env.Do(t, "DELETE", "/api/orders/ord1", nil, u1), 400, "Cannot cancel")
}

func TestCancelOrderRaceDoesNotDoubleRelease(t *testing.T) {
	env, fc := newEnv(t)
	seedBase(t, env)
	seedOrder(t, env, "ord1", "u1", "e1", "pending_payment", 2)
	fc.hook = func() {
		env.Exec(t, `update "order" set status='expired' where id='ord1'`)
		env.Exec(t, `update ticket_type set sold=5 where id='tt1'`)
	}
	if rec := env.Do(t, "DELETE", "/api/orders/ord1", nil, env.Login(t, "u1", nil)); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if s := one[string](t, env, `select status from "order" where id='ord1'`); s != "expired" {
		t.Fatalf("status %s", s)
	}
	if sold := one[int](t, env, `select sold from ticket_type where id='tt1'`); sold != 5 {
		t.Fatalf("sold %d", sold)
	}
}

func TestMidtransCancel(t *testing.T) {
	var gotPath, gotUser string
	respond := func(status int, body string) *midtrans {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.Method + " " + r.URL.Path
			gotUser, _, _ = r.BasicAuth()
			w.WriteHeader(status)
			w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return &midtrans{ServerKey: "SB-key", BaseURL: srv.URL}
	}
	ctx := context.Background()
	if err := respond(200, `{"status_code":"200","transaction_status":"cancel"}`).Cancel(ctx, "ord-1"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /v2/ord-1/cancel" || gotUser != "SB-key" {
		t.Fatalf("%s %s", gotPath, gotUser)
	}
	if err := respond(200, `{"status_code":"407","status_message":"expired"}`).Cancel(ctx, "x"); err != nil {
		t.Fatalf("407 should pass: %v", err)
	}
	if err := respond(200, `{"status_code":"412","status_message":"cannot be modified"}`).Cancel(ctx, "x"); err == nil {
		t.Fatal("412 body should fail")
	}
	if err := respond(404, `{"status_code":"404"}`).Cancel(ctx, "x"); err == nil {
		t.Fatal("HTTP 404 should fail")
	}
}
