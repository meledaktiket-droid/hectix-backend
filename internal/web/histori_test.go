package web

import (
	"bytes"
	"errors"
	"testing"

	"hectix/backend/internal/testutil"
)

type historiPage struct {
	Orders []struct {
		OrderID         string  `json:"orderId"`
		ReferenceNumber *string `json:"referenceNumber"`
		Tiers           []tier  `json:"tiers"`
		TicketCount     int     `json:"ticketCount"`
		StatusLabel     string  `json:"statusLabel"`
	} `json:"orders"`
	NextOffset *int `json:"nextOffset"`
}

func TestHistori(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	env.Exec(t, `insert into event(id,organization_id,name,status,starts_at,ends_at,created_at,updated_at) values
		('past','o1','Lalu','published',now()-interval '3 days',now()-interval '2 days',now(),now()),
		('live','o1','Jalan','published',now()-interval '1 hour',now()+interval '2 hours',now(),now())`)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	env.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values('a1-vip','a1','tt2','VIP',250000,1)`)
	seedOrder(t, env, "a2", "u1", "e1", "paid", 1)
	seedOrder(t, env, "a3", "u1", "live", "paid", 1)
	seedOrder(t, env, "s1", "u1", "past", "paid", 1)
	seedOrder(t, env, "pend", "u1", "e1", "pending_payment", 1)
	seedOrder(t, env, "other", "u2", "e1", "paid", 1)
	u1 := env.Login(t, "u1", nil)

	assertError(t, env.Do(t, "GET", "/api/histori", nil, nil), 401, "Unauthorized")

	rec := env.Do(t, "GET", "/api/histori", nil, u1)
	assertGolden(t, rec, "histori.json")
	p := decode[historiPage](t, rec)
	if len(p.Orders) != 3 || p.Orders[0].OrderID != "a1" || p.Orders[1].OrderID != "a2" || p.Orders[2].OrderID != "a3" || p.NextOffset != nil {
		t.Fatalf("aktif %s", rec.Body)
	}
	if o := p.Orders[0]; o.TicketCount != 3 || len(o.Tiers) != 2 || o.StatusLabel != "Akan Datang" || o.ReferenceNumber == nil {
		t.Fatalf("a1 %+v", o)
	}

	p = decode[historiPage](t, env.Do(t, "GET", "/api/histori?limit=2", nil, u1))
	if len(p.Orders) != 2 || p.NextOffset == nil || *p.NextOffset != 2 {
		t.Fatalf("page1 %+v", p)
	}
	p = decode[historiPage](t, env.Do(t, "GET", "/api/histori?limit=2&offset=2", nil, u1))
	if len(p.Orders) != 1 || p.Orders[0].OrderID != "a3" || p.NextOffset != nil {
		t.Fatalf("page2 %+v", p)
	}

	p = decode[historiPage](t, env.Do(t, "GET", "/api/histori?tab=selesai", nil, u1))
	if len(p.Orders) != 1 || p.Orders[0].OrderID != "s1" || p.Orders[0].StatusLabel != "Selesai" {
		t.Fatalf("selesai %+v", p)
	}
	if rec := env.Do(t, "GET", "/api/histori?tab=selesai&offset=5", nil, u1); rec.Body.String() != "{\"nextOffset\":null,\"orders\":[]}\n" {
		t.Fatalf("empty %s", rec.Body)
	}
}

func TestHistoriDetail(t *testing.T) {
	env, _ := newEnv(t)
	seedBase(t, env)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	assertError(t, env.Do(t, "GET", "/api/histori/a1", nil, nil), 401, "Unauthorized")
	assertError(t, env.Do(t, "GET", "/api/histori/a1", nil, env.Login(t, "u2", nil)), 404, "Not found")

	rec := env.Do(t, "GET", "/api/histori/a1", nil, env.Login(t, "u1", nil))
	assertGolden(t, rec, "histori-{orderId}.json")
	got := decode[map[string]any](t, rec)
	if got["buyerName"] != "u1" || got["eventCity"] != "Bandar Lampung" || got["barcode"] != "BC-a1" || len(got["items"].([]any)) != 1 {
		t.Fatalf("got %s", rec.Body)
	}
}

func TestTicketPDF(t *testing.T) {
	env := testutil.New(t, "web")
	var bannerURLs []string
	mount(env.Router, &handlers{db: env.DB, banners: func(u string) ([]byte, error) {
		bannerURLs = append(bannerURLs, u)
		return nil, errors.New("none")
	}})
	seedBase(t, env)
	seedOrder(t, env, "a1", "u1", "e1", "paid", 2)
	seedOrder(t, env, "p1", "u1", "e1", "pending_payment", 1)
	seedOrder(t, env, "c1", "u1", "e1", "paid", 1)
	env.Exec(t, `update "order" set status='cancelled' where id='c1'`)
	u1 := env.Login(t, "u1", nil)

	for _, c := range []struct {
		path   string
		cookie bool
		status int
		body   string
	}{
		{"/api/histori/a1/ticket.pdf", false, 401, "Unauthorized"},
		{"/api/histori/nope/ticket.pdf", true, 404, "Not found"},
		{"/api/histori/p1/ticket.pdf", true, 409, "Ticket not ready"},
		{"/api/histori/c1/ticket.pdf", true, 409, "Ticket not ready"},
	} {
		ck := u1
		if !c.cookie {
			ck = nil
		}
		rec := env.Do(t, "GET", c.path, nil, ck)
		if rec.Code != c.status || rec.Body.String() != c.body || rec.Header().Get("Content-Type") != "text/plain;charset=UTF-8" {
			t.Errorf("%s: %d %q %s", c.path, rec.Code, rec.Body, rec.Header().Get("Content-Type"))
		}
	}
	if rec := env.Do(t, "GET", "/api/histori/a1/ticket.pdf", nil, env.Login(t, "u2", nil)); rec.Code != 404 {
		t.Fatalf("other user: %d", rec.Code)
	}

	rec := env.Do(t, "GET", "/api/histori/a1/ticket.pdf", nil, u1)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("Content-Disposition") != `inline; filename="hectix-ticket-HX-2026-0000-a1.pdf"` ||
		!bytes.HasPrefix(rec.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("%d %v %.20q", rec.Code, rec.Header(), rec.Body.Bytes())
	}
	if len(bannerURLs) != 1 || bannerURLs[0] != "https://img/e1.jpg" {
		t.Fatalf("banner loader got %v", bannerURLs)
	}
}
