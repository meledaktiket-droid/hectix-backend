package organizer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"hectix/backend/internal/golden"
	"hectix/backend/internal/testutil"
)

const goldenDir = "../../testdata/golden/organizer/routes/"

type fx struct {
	*testutil.Env
	owner, admin, member, other, stale, noOrg *http.Cookie
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := testutil.New(t, "organizer")
	Mount(e.Router, e.Deps)
	for _, u := range []string{"owner", "adm", "mem", "other", "stale", "buyer", "loner"} {
		e.SeedUser(t, u, "user")
	}
	e.Exec(t, `insert into organization(id,name,slug,created_at,status) values
		('org1','Org One','org-one',now(),'active'), ('org2','Org Two','org-two',now(),'active')`)
	e.Exec(t, `insert into member(id,organization_id,user_id,role,created_at) values
		('m1','org1','owner','owner',now()), ('m2','org1','adm','admin',now()),
		('m3','org1','mem','member',now()), ('m4','org2','other','owner',now())`)
	org1, org2 := "org1", "org2"
	return &fx{Env: e,
		owner: e.Login(t, "owner", &org1), admin: e.Login(t, "adm", &org1), member: e.Login(t, "mem", &org1),
		other: e.Login(t, "other", &org2), stale: e.Login(t, "stale", &org1), noOrg: e.Login(t, "loner", nil),
	}
}

func (f *fx) event(t *testing.T, id, org string) {
	t.Helper()
	f.Exec(t, `insert into event(id,organization_id,name,description,venue_name,address,city,banner_url,categories,
		starts_at,ends_at,status,visibility,attendee_form,created_at,updated_at)
		values($1,$2,'Event '||$1,'<p>d</p>','Venue','Addr','Malang','https://x/b.jpg','["musik"]',
		now() + interval '30 days', now() + interval '31 days','draft','public',false,now(),now())`, id, org)
}

func (f *fx) ticket(t *testing.T, id, event string, price, qty, sold, order int) {
	t.Helper()
	f.Exec(t, `insert into ticket_type(id,event_id,name,price,quantity,sold,benefits,display_order,created_at,updated_at)
		values($1,$2,'Ticket '||$1,$3,$4,$5,'["a"]',$6,now(),now())`, id, event, price, qty, sold, order)
}

func (f *fx) order(t *testing.T, id, event, user, status string, ticketType string, price, qty int, createdAt string) {
	t.Helper()
	f.Exec(t, `insert into "order"(id,user_id,event_id,status,payment_method,subtotal,tax,service_fee,total,
		reference_number,expires_at,created_at,updated_at)
		values($1,$2,$3,$4,'qris',$5,0,0,$5,'HX-'||$1,now(),$6::timestamp,now())`, id, user, event, status, price*qty, createdAt)
	f.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity)
		values($1||'-i',$1,$2,'Ticket '||$2,$3,$4)`, id, ticketType, price, qty)
}

func (f *fx) do(t *testing.T, method, path string, body any, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return f.Do(t, method, path, body, c)
}

func (f *fx) scalar(t *testing.T, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := f.DB.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return v
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("status %d, want %d: %s", rec.Code, code, rec.Body)
	}
	if body != "" && rec.Body.String() != body+"\n" {
		t.Fatalf("body %s, want %s", rec.Body, body)
	}
}

func errBody(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

func ok200(t *testing.T, rec *httptest.ResponseRecorder, goldenFile string) map[string]any {
	t.Helper()
	expect(t, rec, 200, "")
	if goldenFile != "" {
		golden.Assert(t, goldenDir+goldenFile, rec.Body.Bytes())
	}
	return decode(t, rec)
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("%v: %s", err, rec.Body)
	}
	return m
}

func authCases(t *testing.T, f *fx, method, path string, body any) {
	t.Helper()
	expect(t, f.do(t, method, path, body, nil), 401, errBody("Unauthorized"))
	expect(t, f.do(t, method, path, body, f.noOrg), 400, errBody("No active organization"))
	expect(t, f.do(t, method, path, body, f.stale), 403, errBody("Forbidden"))
}

func goldenPart(t *testing.T, file string, path ...any) []byte {
	t.Helper()
	raw, err := os.ReadFile(goldenDir + file)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, p := range path {
		switch k := p.(type) {
		case string:
			v = v.(map[string]any)[k]
		case int:
			v = v.([]any)[k]
		}
	}
	b, _ := json.Marshal(v)
	return b
}

func assertShape(t *testing.T, want []byte, got any) {
	t.Helper()
	b, _ := json.Marshal(got)
	if d := golden.Diff(want, b); d != "" {
		t.Fatalf("shape mismatch:\n%s\ngot: %s", d, b)
	}
}

var voucherShape = []byte(`{"id":"","eventId":"","code":"","type":"","amount":0,"usageLimit":null,"usageCount":0,
	"expiresAt":null,"deletedAt":null,"createdAt":""}`)
