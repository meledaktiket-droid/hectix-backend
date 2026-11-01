package organizer

import (
	"net/url"
	"testing"
	"time"
)

func TestParseOrderParams(t *testing.T) {
	cases := []struct {
		q    string
		want orderParams
	}{
		{"", orderParams{Page: 1, PageSize: 25}},
		{"status=paid&from=2026-02-28&to=2026-02-30&q=%20ana%20&eventId=%20e1&page=3x&pageSize=500",
			orderParams{Status: "paid", From: "2026-02-28", Q: "ana", EventID: "e1", Page: 3, PageSize: 100}},
		{"status=bogus&page=-4&pageSize=0", orderParams{Page: 1, PageSize: 25}},
		{"page=0&pageSize=-3", orderParams{Page: 1, PageSize: 1}},
		{"page=&pageSize=abc", orderParams{Page: 1, PageSize: 25}},
		{"page=99999999999999999999999&pageSize=100", orderParams{Page: maxPage, PageSize: 100}},
		{"page=100001", orderParams{Page: maxPage, PageSize: 25}},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		if got := parseOrderParams(q); got != c.want {
			t.Errorf("%q: got %+v want %+v", c.q, got, c.want)
		}
	}
	if got := wibStart("2026-05-01"); !got.Equal(time.Date(2026, 4, 30, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("wibStart %v", got)
	}
	if got := wibEndExclusive("2026-05-31"); !got.Equal(time.Date(2026, 5, 31, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("wibEndExclusive %v", got)
	}
}

func TestListOrders(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/orders", nil)
	expect(t, f.do(t, "GET", "/api/orders", nil, f.member), 200,
		`{"orders":[],"summary":{"revenue":0,"counts":{},"voucherCount":0,"discountTotal":0},"total":0}`)

	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 100, 0, 0)
	f.ticket(t, "t2", "e2", 1000, 100, 0, 0)
	f.ticket(t, "tx", "x1", 1000, 100, 0, 0)
	f.Exec(t, `update "user" set name = 'Ana Buyer' where id = 'buyer'`)
	f.SeedUser(t, "zed", "user")
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 2, "2026-04-30 17:00")
	f.order(t, "o2", "e1", "buyer", "paid", "t1", 1000, 1, "2026-05-01 10:00")
	f.order(t, "o3", "e2", "zed", "expired", "t1", 1000, 1, "2026-05-02 10:00")
	f.order(t, "o4", "e2", "buyer", "paid", "t2", 1000, 3, "2026-04-30 16:59")
	f.order(t, "ox", "x1", "buyer", "paid", "tx", 1000, 3, "2026-05-01 10:00")
	f.order(t, "o5", "e2", "zed", "cancelled", "t2", 1000, 1, "2026-01-01")
	f.Exec(t, `update "order" set voucher_code = 'HEMAT', voucher_discount = 500, reference_number = null where id = 'o2'`)
	f.Exec(t, `update "order" set bank = 'bca', payment_method = 'va_bank' where id = 'o1'`)

	got := ok200(t, f.do(t, "GET", "/api/orders", nil, f.member), "orders.json")
	if got["total"] != 5.0 || len(got["orders"].([]any)) != 5 {
		t.Fatalf("all %v", got)
	}
	s := got["summary"].(map[string]any)
	if s["revenue"] != 6000.0 || s["voucherCount"] != 1.0 || s["discountTotal"] != 500.0 ||
		s["counts"].(map[string]any)["paid"] != 3.0 || s["counts"].(map[string]any)["expired"] != 1.0 {
		t.Fatalf("summary %v", s)
	}
	first := got["orders"].([]any)[0].(map[string]any)
	if first["id"] != "o3" || first["eventName"] != "Event e2" || first["referenceNumber"] != "HX-o3" {
		t.Fatalf("first %v", first)
	}
	o2 := got["orders"].([]any)[1].(map[string]any)
	if o2["referenceNumber"] != "o2" || o2["voucherCode"] != "HEMAT" || len(o2["items"].([]any)) != 1 {
		t.Fatalf("o2 %v", o2)
	}

	got = ok200(t, f.do(t, "GET", "/api/orders?status=expired", nil, f.member), "")
	if got["total"] != 1.0 || got["summary"].(map[string]any)["counts"].(map[string]any)["paid"] != 3.0 {
		t.Fatalf("status %v", got)
	}
	got = ok200(t, f.do(t, "GET", "/api/orders?from=2026-05-01&to=2026-05-01", nil, f.member), "")
	if got["total"] != 2.0 {
		t.Fatalf("range %v", got)
	}
	got = ok200(t, f.do(t, "GET", "/api/orders?q=ANA", nil, f.member), "")
	if got["total"] != 3.0 {
		t.Fatalf("q %v", got)
	}
	if got = ok200(t, f.do(t, "GET", "/api/orders?eventId=e2", nil, f.member), ""); got["total"] != 3.0 {
		t.Fatalf("eventId %v", got)
	}
	if got = ok200(t, f.do(t, "GET", "/api/orders?eventId=x1", nil, f.member), ""); got["total"] != 5.0 {
		t.Fatalf("foreign eventId %v", got)
	}
	got = ok200(t, f.do(t, "GET", "/api/orders?page=9223372036854775807", nil, f.member), "")
	if got["total"] != 5.0 || len(got["orders"].([]any)) != 0 {
		t.Fatalf("huge page %v", got)
	}
	got = ok200(t, f.do(t, "GET", "/api/orders?page=2&pageSize=3", nil, f.member), "")
	if o := got["orders"].([]any); got["total"] != 5.0 || len(o) != 2 || o[0].(map[string]any)["id"] != "o4" {
		t.Fatalf("page %v", got)
	}
}

func TestEventOrders(t *testing.T) {
	f := setup(t)
	f.event(t, "e1", "org1")
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 100, 0, 0)
	f.ticket(t, "t2", "e1", 500, 50, 0, 1)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 2, "2026-05-01")
	f.order(t, "o2", "e1", "buyer", "expired", "t1", 1000, 7, "2026-05-02")
	authCases(t, f, "GET", "/api/events/e1/orders", nil)
	expect(t, f.do(t, "GET", "/api/events/x1/orders", nil, f.owner), 404, errBody("Not found"))

	got := ok200(t, f.do(t, "GET", "/api/events/e1/orders", nil, f.member), "events-{id}-orders.json")
	st := got["stats"].([]any)
	if len(st) != 2 {
		t.Fatalf("stats %v", st)
	}
	if s := st[0].(map[string]any); s["id"] != "t1" || s["sold"] != 2.0 || s["revenue"] != 2000.0 || s["capacity"] != 100.0 {
		t.Fatalf("t1 %v", s)
	}
	if s := st[1].(map[string]any); s["sold"] != 0.0 || s["revenue"] != 0.0 {
		t.Fatalf("t2 %v", s)
	}
	b := got["buyers"].([]any)
	if len(b) != 2 || b[0].(map[string]any)["orderId"] != "o2" || len(b[0].(map[string]any)["items"].([]any)) != 1 {
		t.Fatalf("buyers %v", b)
	}
}
