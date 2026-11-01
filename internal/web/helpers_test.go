package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hectix/backend/internal/golden"
	"hectix/backend/internal/testutil"
)

type fakeCancel struct {
	err   error
	calls []string
	hook  func()
}

func (f *fakeCancel) cancel(_ context.Context, orderID string) error {
	f.calls = append(f.calls, orderID)
	if f.hook != nil {
		f.hook()
	}
	return f.err
}

func newEnv(t *testing.T) (*testutil.Env, *fakeCancel) {
	t.Helper()
	env := testutil.New(t, "web")
	fc := &fakeCancel{}
	mount(env.Router, &handlers{db: env.DB, cancelCharge: fc.cancel})
	return env, fc
}

func seedBase(t *testing.T, env *testutil.Env) {
	t.Helper()
	env.SeedUser(t, "u1", "user")
	env.SeedUser(t, "u2", "user")
	for _, s := range []string{
		`update "user" set image='https://img/u1.jpg', gender='male', instagram='ig1' where id='u1'`,
		`insert into organization(id,name,created_at) values('o1','Org Satu',now())`,
		`insert into event(id,organization_id,name,description,venue_name,address,city,banner_url,status,starts_at,ends_at,categories,created_at,updated_at)
			values('e1','o1','Konser Senja','<p>x</p>','GOR','Jl. A','Bandar Lampung','https://img/e1.jpg','published',now()+interval '7 days',now()+interval '7 days 8 hours','["music"]',now(),now())`,
		`insert into ticket_type(id,event_id,name,price,quantity,sold,benefits,display_order,created_at,updated_at)
			values('tt1','e1','Reguler',100000,10,2,'["Merch"]',1,now(),now()),('tt2','e1','VIP',250000,5,5,null,0,now(),now())`,
		`insert into performer(id,event_id,name,image_url,display_order,created_at,updated_at) values('p1','e1','Band','https://img/p1.jpg',0,now(),now())`,
	} {
		env.Exec(t, s)
	}
}

func seedOrder(t *testing.T, env *testutil.Env, id, user, event, status string, qty int) {
	t.Helper()
	env.Exec(t, `insert into "order"(id,user_id,event_id,status,payment_method,bank,va_number,qris_url,reference_number,barcode,subtotal,tax,service_fee,total,voucher_code,voucher_discount,send_eticket,expires_at,created_at,updated_at)
		values($1,$2,$3,$4,'qris',null,null,'https://qr/x',
			case when $4='paid' then 'HX-2026-0000-'||$1 end, case when $4='paid' then 'BC-'||$1 end,
			100000*$5,7000,4000,100000*$5+11000,null,0,true,now()+interval '15 minutes',now(),now())`, id, user, event, status, qty)
	env.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1||'-i',$1,'tt1','Reguler',100000,$2)`, id, qty)
}

func goldenFile(name string) string {
	return filepath.Join("..", "..", "testdata", "golden", "web", "routes", name)
}

func assertGolden(t *testing.T, rec *httptest.ResponseRecorder, name string) {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	golden.Assert(t, goldenFile(name), rec.Body.Bytes())
}

func assertError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	want := `{"error":"` + msg + `"}` + "\n"
	if rec.Code != status || rec.Body.String() != want {
		t.Fatalf("got %d %q, want %d %q", rec.Code, rec.Body.String(), status, want)
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return v
}

func one[T any](t *testing.T, env *testutil.Env, sql string, args ...any) T {
	t.Helper()
	var v T
	if err := env.DB.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
