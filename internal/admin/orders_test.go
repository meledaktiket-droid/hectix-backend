package admin

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseOrderParams(t *testing.T) {
	cases := []struct {
		q    string
		want orderParams
	}{
		{"", orderParams{Page: 1, PageSize: 25}},
		{"status=paid&orgId=%20o1%20&q=%20bob%20&page=3&pageSize=10", orderParams{Status: "paid", OrgID: "o1", Q: "bob", Page: 3, PageSize: 10}},
		{"status=refunded&page=0&pageSize=0", orderParams{Page: 1, PageSize: 25}},
		{"page=-2&pageSize=500", orderParams{Page: 1, PageSize: 100}},
		{"page=x&pageSize=-5", orderParams{Page: 1, PageSize: 1}},
		{"page=4abc&pageSize=", orderParams{Page: 4, PageSize: 25}},
		{"from=2026-02-28&to=2026-02-30", orderParams{From: "2026-02-28", Page: 1, PageSize: 25}},
		{"from=2026-2-1&to=0050-01-01", orderParams{Page: 1, PageSize: 25}},
		{"from=2024-02-29&to=2026-12-31", orderParams{From: "2024-02-29", To: "2026-12-31", Page: 1, PageSize: 25}},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.q)
		if got := parseOrderParams(q); got != c.want {
			t.Errorf("%q: got %+v want %+v", c.q, got, c.want)
		}
	}
	if got := wibStart("2026-05-01"); !got.Equal(time.Date(2026, 4, 30, 17, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
}

type ordersResp struct {
	Orders  []adminOrder
	Total   int64
	Summary ordersSummary
}

func TestListOrders(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/orders", nil)
	e.Exec(t, `insert into site_setting(key,value,updated_at) values('midtrans_va_fee','4000',now()),('midtrans_qris_percent','0.7',now())`)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedOrg(t, "o2", "Org Two", "active")
	e.seedEvent(t, "e1", "o1", "Gig One", "published")
	e.seedEvent(t, "e2", "o2", "Gig Two", "published")
	e.seedTicketType(t, "t1", "e1", 100000, 100, 0)
	e.seedTicketType(t, "t2", "e2", 100000, 100, 0)
	e.Exec(t, `insert into "user"(id,name,email,email_verified,created_at,updated_at) values('bob','Bob Buyer','bob@x.com',true,now(),now())`)
	e.seedOrder(t, "a", "bob", "e1", "t1", "paid", "qris", 1, 100000, "1 hour")
	e.seedOrder(t, "b", "usr", "e1", "t1", "paid", "va_bank", 1, 100000, "2 hours")
	e.seedOrder(t, "c", "usr", "e2", "t2", "expired", "qris", 1, 100000, "3 hours")
	e.seedOrder(t, "d", "bob", "e2", "t2", "paid", "qris", 2, 100000, "4 hours")
	e.seedOrder(t, "f", "usr", "e1", "t1", "cancelled", "va_bank", 1, 100000, "5 hours")
	e.Exec(t, `update "order" set voucher_code='V', voucher_discount=5000 where id='a'`)
	e.Exec(t, `update "order" set reference_number=null where id='d'`)

	rec := e.do(t, "GET", "/api/orders", nil, e.staff)
	assertGolden(t, "orders.json", rec)
	got := decode[ordersResp](t, rec)
	if got.Total != 5 || len(got.Orders) != 5 || got.Orders[0].ID != "a" || got.Orders[3].ReferenceNumber != "d" {
		t.Fatalf("orders %+v", got)
	}
	a, b, c := got.Orders[0], got.Orders[1], got.Orders[2]
	if a.MidtransFee != 777 || b.MidtransFee != 4000 || c.MidtransFee != 0 || a.OrgName != "Org One" || a.BuyerEmail != "bob@x.com" ||
		len(a.Items) != 1 || a.Items[0].Quantity != 1 || *a.VoucherCode != "V" {
		t.Fatalf("rows %+v", got.Orders)
	}
	s := got.Summary
	if s.GrossRevenue != 443000 || s.ServiceFeeRevenue != 3000 || s.MidtransFee != 6324 || s.NetRevenue != 443000-6324 ||
		s.Counts["paid"] != 3 || s.Counts["expired"] != 1 || s.Counts["cancelled"] != 1 || len(s.Counts) != 3 || s.VoucherCount != 1 || s.DiscountTotal != 5000 {
		t.Fatalf("summary %+v", s)
	}

	got = decode[ordersResp](t, e.do(t, "GET", "/api/orders?status=expired", nil, e.staff))
	if got.Total != 1 || len(got.Orders) != 1 || got.Summary.Counts["paid"] != 3 {
		t.Fatalf("status filter %+v", got)
	}
	got = decode[ordersResp](t, e.do(t, "GET", "/api/orders?orgId=o2&q=BOB", nil, e.staff))
	if got.Total != 1 || got.Orders[0].ID != "d" || got.Summary.Counts["paid"] != 1 || got.Summary.GrossRevenue != 221000 {
		t.Fatalf("org+q filter %+v", got)
	}
	got = decode[ordersResp](t, e.do(t, "GET", "/api/orders?pageSize=2&page=2", nil, e.staff))
	if got.Total != 5 || len(got.Orders) != 2 || got.Orders[0].ID != "c" {
		t.Fatalf("paging %+v", got)
	}

	e.Exec(t, `update "order" set created_at = '2026-04-30 16:59:59' where id = 'a'`)
	e.Exec(t, `update "order" set created_at = '2026-04-30 17:00:00' where id = 'b'`)
	e.Exec(t, `update "order" set created_at = '2026-05-01 16:59:59' where id = 'c'`)
	e.Exec(t, `update "order" set created_at = '2026-05-01 17:00:00' where id = 'd'`)
	got = decode[ordersResp](t, e.do(t, "GET", "/api/orders?from=2026-05-01&to=2026-05-01", nil, e.staff))
	if got.Total != 2 || got.Orders[0].ID != "c" || got.Orders[1].ID != "b" {
		t.Fatalf("dates %+v", got)
	}
	want(t, e.do(t, "GET", "/api/orders?from=2030-01-01", nil, e.staff), 200,
		`{"orders":[],"total":0,"summary":{"grossRevenue":0,"serviceFeeRevenue":0,"midtransFee":0,"netRevenue":0,"counts":{},"voucherCount":0,"discountTotal":0}}`)
}

func TestExportOrders(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/orders/export", nil)
	e.Exec(t, `insert into site_setting(key,value,updated_at) values('midtrans_va_fee','4000',now()),('midtrans_qris_percent','0.7',now())`)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedEvent(t, "e1", "o1", "Gig \"One\"", "published")
	e.seedTicketType(t, "t1", "e1", 100000, 100, 0)
	e.seedOrder(t, "a", "usr", "e1", "t1", "paid", "qris", 1, 100000, "1 hour")
	e.seedOrder(t, "b", "usr", "e1", "t1", "expired", "va_bank", 1, 100000, "2 hours")
	e.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values('a2','a','t1','VIP',0,2)`)
	e.Exec(t, `update "order" set created_at = '2026-05-01 03:00:00', reference_number = null, voucher_code = 'V', voucher_discount = 5000 where id = 'a'`)

	rec := e.do(t, "GET", "/api/orders/export?status=paid", nil, e.staff)
	want(t, rec, 200, "")
	if ct, cd := rec.Header().Get("Content-Type"), rec.Header().Get("Content-Disposition"); ct != "text/csv; charset=utf-8" ||
		cd != `attachment; filename="orders-`+time.Now().UTC().Format("2006-01-02")+`.csv"` {
		t.Fatalf("headers %q %q", ct, cd)
	}
	body := rec.Body.String()
	lines := strings.Split(strings.TrimPrefix(body, "\xef\xbb\xbf"), "\r\n")
	if !strings.HasPrefix(body, "\xef\xbb\xbf") || len(lines) != 2 {
		t.Fatalf("csv %q", body)
	}
	tickets := []string{`"1× t1, 2× VIP"`, `"2× VIP, 1× t1"`}
	row := `"a","Org One","usr","usr@example.com","Gig ""One""",` + "%s" +
		`,"Rp` + " " + `100.000","−Rp` + " " + `5.000","V","Rp` + " " + `10.000","Rp` + " " + `1.000","Rp` + " " + `111.000","−Rp` + " " + `777","Rp` + " " + `110.223","Paid","1 Mei 2026"`
	if lines[1] != strings.Replace(row, "%s", tickets[0], 1) && lines[1] != strings.Replace(row, "%s", tickets[1], 1) {
		t.Fatalf("row\n%s\nwant\n%s", lines[1], row)
	}
	rec = e.do(t, "GET", "/api/orders/export", nil, e.staff)
	if n := strings.Count(rec.Body.String(), "\r\n"); n != 2 {
		t.Fatalf("all orders: %d line breaks", n)
	}
}
