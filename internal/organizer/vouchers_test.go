package organizer

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVouchers(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.Exec(t, `insert into voucher(id,event_id,code,type,amount,created_at,deleted_at) values
		('vd','e1','OLD','fixed',1000,now(),now()), ('vx','x1','THEIRS','fixed',1000,now(),null)`)

	authCases(t, f, "GET", "/api/events/e1/vouchers", nil)
	expect(t, f.do(t, "GET", "/api/events/x1/vouchers", nil, f.owner), 404, errBody("Not found"))
	got := ok200(t, f.do(t, "GET", "/api/events/e1/vouchers", nil, f.member), "events-{id}-vouchers.json")
	if len(got["vouchers"].([]any)) != 0 {
		t.Fatalf("vouchers %v", got)
	}

	valid := map[string]any{"code": "hemat10", "type": "percent", "amount": 10, "usageLimit": 5, "expiresAt": "2099-12-31"}
	authCases(t, f, "POST", "/api/events/e1/vouchers", valid)
	expect(t, f.do(t, "POST", "/api/events/e1/vouchers", valid, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "POST", "/api/events/x1/vouchers", valid, f.owner), 404, errBody("Not found"))
	for _, c := range []struct {
		body map[string]any
		msg  string
	}{
		{map[string]any{"type": "fixed", "amount": 1}, "Code is required"},
		{map[string]any{"code": "A", "type": "bogus", "amount": 1}, "Type must be 'percent' or 'fixed'"},
		{map[string]any{"code": "A", "type": "fixed"}, "Valid amount is required"},
		{map[string]any{"code": "A", "type": "fixed", "amount": -1}, "Valid amount is required"},
		{map[string]any{"code": "A", "type": "percent", "amount": 101}, "Valid amount is required"},
		{map[string]any{"code": "A", "type": "fixed", "amount": 12.5}, "Valid amount is required"},
		{map[string]any{"code": "   ", "type": "fixed", "amount": 1}, "Code is required"},
		{map[string]any{"code": "A", "type": "fixed", "amount": 1, "usageLimit": 0}, "Usage limit must be at least 1"},
		{map[string]any{"code": "A", "type": "fixed", "amount": 1, "usageLimit": 2.5}, "Usage limit must be at least 1"},
		{map[string]any{"code": "A", "type": "fixed", "amount": 1, "usageLimit": "5"}, "Usage limit must be at least 1"},
		{map[string]any{"code": "A", "type": "fixed", "amount": 1, "expiresAt": "2020-01-01"}, msgVoucherExpired},
		{map[string]any{"code": "A", "type": "fixed", "amount": 1, "expiresAt": "soon"}, "Invalid expiresAt"},
	} {
		expect(t, f.do(t, "POST", "/api/events/e1/vouchers", c.body, f.owner), 400, errBody(c.msg))
	}
	rec := f.do(t, "POST", "/api/events/e1/vouchers", valid, f.admin)
	expect(t, rec, 201, "")
	v := decode(t, rec)["voucher"].(map[string]any)
	assertShape(t, voucherShape, v)
	if v["code"] != "HEMAT10" || v["usageLimit"] != 5.0 || v["usageCount"] != 0.0 || v["expiresAt"] != "2099-12-31T00:00:00.000Z" {
		t.Fatalf("voucher %v", v)
	}
	expect(t, f.do(t, "POST", "/api/events/e1/vouchers", valid, f.admin), 409, errBody("Kode voucher HEMAT10 sudah dipakai di event ini"))
	rec = f.do(t, "POST", "/api/events/e1/vouchers", map[string]any{"code": " old ", "type": "fixed", "amount": 500, "usageLimit": nil}, f.admin)
	expect(t, rec, 201, "")
	if v := decode(t, rec)["voucher"].(map[string]any); v["usageLimit"] != nil || v["expiresAt"] != nil || v["code"] != "OLD" {
		t.Fatalf("voucher %v", v)
	}

	del := map[string]any{"voucherId": v["id"]}
	authCases(t, f, "DELETE", "/api/events/e1/vouchers", del)
	expect(t, f.do(t, "DELETE", "/api/events/e1/vouchers", del, f.member), 403, errBody("Forbidden"))
	expect(t, f.do(t, "DELETE", "/api/events/x1/vouchers", map[string]any{"voucherId": "vx"}, f.owner), 404, errBody("Not found"))
	if f.scalar(t, `select deleted_at is null from voucher where id='vx'`) != true {
		t.Fatal("other org's voucher deleted")
	}
	expect(t, f.do(t, "DELETE", "/api/events/e1/vouchers", del, f.admin), 200, `{"ok":true}`)
	if f.scalar(t, `select deleted_at is not null from voucher where id=$1`, v["id"]) != true {
		t.Fatal("not soft deleted")
	}
	if got := ok200(t, f.do(t, "GET", "/api/events/e1/vouchers", nil, f.member), ""); len(got["vouchers"].([]any)) != 1 {
		t.Fatalf("vouchers %v", got)
	}
}

func TestVoucherExpiryWIBDay(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	var now time.Time
	h := &handlers{db: f.DB, now: func() time.Time { return now }}
	f.Router.Post("/test/events/{id}/vouchers", h.createVoucher)
	n := 0
	post := func(exp string) *httptest.ResponseRecorder {
		n++
		return f.do(t, "POST", "/test/events/e1/vouchers",
			map[string]any{"code": fmt.Sprintf("C%d", n), "type": "fixed", "amount": 1, "expiresAt": exp}, f.owner)
	}
	for _, c := range []struct {
		now                        string
		yesterday, today, tomorrow string
	}{
		{"2026-10-01T17:05:00Z", "2026-10-01", "2026-10-02", "2026-10-03"},
		{"2026-10-02T16:55:00Z", "2026-10-01", "2026-10-02", "2026-10-03"},
		{"2026-10-01T16:59:59Z", "2026-09-30", "2026-10-01", "2026-10-02"},
	} {
		now, _ = time.Parse(time.RFC3339, c.now)
		expect(t, post(c.yesterday), 400, errBody(msgVoucherExpired))
		for _, d := range []string{c.today, c.tomorrow} {
			rec := post(d)
			expect(t, rec, 201, "")
			if got := decode(t, rec)["voucher"].(map[string]any)["expiresAt"]; got != d+"T00:00:00.000Z" {
				t.Fatalf("%s stored as %v", d, got)
			}
		}
	}
	now, _ = time.Parse(time.RFC3339, "2026-10-01T17:05:00Z")
	expect(t, post("2026-10-01T16:59:00Z"), 400, errBody(msgVoucherExpired))
	expect(t, post("2026-10-01T17:00:00Z"), 201, "")
}
