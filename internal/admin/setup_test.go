package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/golden"
	"hectix/backend/internal/mail"
	"hectix/backend/internal/testutil"
)

type sent struct{ To, Subject, HTML, Text string }

type fakeMail struct {
	mu   sync.Mutex
	msgs []sent
	err  error
}

func (f *fakeMail) Send(_ context.Context, to, subject, html, text string, _ ...mail.Attachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, sent{to, subject, html, text})
	return f.err
}

func (f *fakeMail) all() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.msgs...)
}

func staffGate(next http.Handler) http.Handler {
	gated := RequireStaff(nil)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == AcceptPath {
			next.ServeHTTP(w, r)
			return
		}
		gated.ServeHTTP(w, r)
	})
}

type env struct {
	*testutil.Env
	mail                      *fakeMail
	super, admin, staff, user *http.Cookie
}

func setup(t *testing.T) *env {
	t.Helper()
	e := testutil.New(t, "admin")
	fm := &fakeMail{}
	e.Deps.Mail = fm
	e.Router.Group(func(r chi.Router) {
		r.Use(staffGate)
		Mount(r, e.Deps)
	})
	for id, role := range map[string]string{"super": "super_admin", "adm": "admin", "stf": "staff", "usr": "user"} {
		e.SeedUser(t, id, role)
	}
	return &env{Env: e, mail: fm,
		super: e.Login(t, "super", nil), admin: e.Login(t, "adm", nil),
		staff: e.Login(t, "stf", nil), user: e.Login(t, "usr", nil)}
}

func (e *env) do(t *testing.T, method, path string, body any, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return e.Do(t, method, path, body, c)
}

func want(t *testing.T, rec *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	got := rec.Body.String()
	if len(got) > 0 && got[len(got)-1] == '\n' {
		got = got[:len(got)-1]
	}
	if rec.Code != code || (body != "" && got != body) {
		t.Fatalf("got %d %s, want %d %s", rec.Code, got, code, body)
	}
}

func (e *env) guarded(t *testing.T, method, path string, body any, denied ...*http.Cookie) {
	t.Helper()
	want(t, e.do(t, method, path, body, nil), 401, `{"error":"Unauthorized"}`)
	for _, c := range append([]*http.Cookie{e.user}, denied...) {
		want(t, e.do(t, method, path, body, c), 403, `{"error":"Forbidden"}`)
	}
}

func goldenFile(name string) string {
	return filepath.Join("..", "..", "testdata", "golden", "admin", "routes", name)
}

func assertGolden(t *testing.T, name string, rec *httptest.ResponseRecorder) {
	t.Helper()
	want(t, rec, 200, "")
	golden.Assert(t, goldenFile(name), rec.Body.Bytes())
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v: %s", err, rec.Body.String())
	}
	return v
}

func (e *env) scalar(t *testing.T, dst any, sql string, args ...any) {
	t.Helper()
	if err := e.DB.QueryRow(context.Background(), sql, args...).Scan(dst); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func (e *env) seedOrg(t *testing.T, id, name, status string) {
	t.Helper()
	e.Exec(t, `insert into organization(id,name,slug,created_at,status) values($1,$2,$1,now(),$3)`, id, name, status)
}

func (e *env) seedMember(t *testing.T, org, user, role string) {
	t.Helper()
	e.Exec(t, `insert into member(id,organization_id,user_id,role,created_at) values($1||$2,$1,$2,$3,now())`, org, user, role)
}

func (e *env) seedEvent(t *testing.T, id, org, name, status string) {
	t.Helper()
	e.Exec(t, `insert into event(id,organization_id,name,venue_name,city,starts_at,status,created_at,updated_at)
		values($1,$2,$3,'Venue','Malang',now()+interval '30 days',$4,now(),now())`, id, org, name, status)
}

func (e *env) seedTicketType(t *testing.T, id, event string, price, qty, order int) {
	t.Helper()
	e.Exec(t, `insert into ticket_type(id,event_id,name,price,quantity,display_order,created_at,updated_at)
		values($1,$2,$1,$3,$4,$5,now(),now())`, id, event, price, qty, order)
}

func (e *env) seedOrder(t *testing.T, id, user, event, ticketType, status, method string, qty, price int, age string) {
	t.Helper()
	subtotal := qty * price
	e.Exec(t, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,
		reference_number,expires_at,created_at,updated_at)
		values($1,$2,$3,$4,$5,$6::int,$7::int,1000,$6::int+$7::int+1000,'HX-'||$1,now(),now()-$8::interval,now())`,
		id, user, event, status, method, subtotal, subtotal/10, age)
	e.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity)
		values($1||'-i',$1,$2,$2,$3,$4)`, id, ticketType, price, qty)
}
