package admin

import (
	"fmt"
	"testing"
)

func TestListEvents(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/events", nil)
	e.seedOrg(t, "o1", "Org One", "active")
	for i := range 23 {
		status := "draft"
		if i%2 == 0 {
			status = "published"
		}
		e.seedEvent(t, fmt.Sprintf("e%02d", i), "o1", fmt.Sprintf("Event %02d", i), status)
		e.Exec(t, `update event set created_at = now() - make_interval(mins => $1) where id = $2`, 100-i, fmt.Sprintf("e%02d", i))
	}

	rec := e.do(t, "GET", "/api/events", nil, e.staff)
	assertGolden(t, "events.json", rec)
	p := decode[page[eventRow]](t, rec)
	if p.Total != 23 || p.Page != 1 || p.TotalPages != 2 || len(p.Data) != 20 || p.Data[0].ID != "e22" || p.Data[0].OrganizationName != "Org One" {
		t.Fatalf("page 1: %+v", p)
	}
	p = decode[page[eventRow]](t, e.do(t, "GET", "/api/events?page=2x", nil, e.staff))
	if p.Page != 2 || len(p.Data) != 3 || p.Data[2].ID != "e00" {
		t.Fatalf("page 2: %+v", p)
	}
	for _, q := range []string{"page=abc", "page=-4", "page=0"} {
		if p = decode[page[eventRow]](t, e.do(t, "GET", "/api/events?"+q, nil, e.staff)); p.Page != 1 {
			t.Fatalf("%s: page %d", q, p.Page)
		}
	}
	p = decode[page[eventRow]](t, e.do(t, "GET", "/api/events?status=published&q=%20event%201%20", nil, e.staff))
	if p.Total != 5 || len(p.Data) != 5 {
		t.Fatalf("filtered: %+v", p)
	}
	want(t, e.do(t, "GET", "/api/events?status=archived", nil, e.staff), 400, `{"error":"Invalid status"}`)
	want(t, e.do(t, "GET", "/api/events?page=9", nil, e.staff), 200, `{"data":[],"total":23,"page":9,"totalPages":2}`)
}

func TestGetEvent(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/events/e1", nil)
	e.seedOrg(t, "o1", "Org One", "active")
	e.seedEvent(t, "e1", "o1", "Gig", "published")
	e.seedEvent(t, "gone", "o1", "Gone", "published")
	e.Exec(t, `update event set deleted_at = now() where id = 'gone'`)
	e.seedTicketType(t, "vip", "e1", 200000, 10, 1)
	e.seedTicketType(t, "reg", "e1", 50000, 100, 0)
	e.seedOrder(t, "a", "usr", "e1", "reg", "paid", "qris", 2, 50000, "3 hours")
	e.seedOrder(t, "b", "usr", "e1", "vip", "paid", "qris", 1, 200000, "2 hours")
	e.seedOrder(t, "c", "usr", "e1", "vip", "expired", "qris", 3, 200000, "1 hour")
	e.Exec(t, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values('a2','a','vip','vip',200000,1)`)

	rec := e.do(t, "GET", "/api/events/e1", nil, e.staff)
	assertGolden(t, "events-{id}.json", rec)
	got := decode[struct {
		Event  eventDetail
		Stats  []ticketStat
		Buyers []buyer
	}](t, rec)
	if got.Event.OrganizationName != "Org One" || got.Event.EndsAt != nil {
		t.Fatalf("event %+v", got.Event)
	}
	if len(got.Stats) != 2 || got.Stats[0] != (ticketStat{"reg", "reg", 50000, 100, 2, 100000}) ||
		got.Stats[1] != (ticketStat{"vip", "vip", 200000, 10, 2, 400000}) {
		t.Fatalf("stats %+v", got.Stats)
	}
	if len(got.Buyers) != 3 || got.Buyers[0].OrderID != "c" || got.Buyers[2].OrderID != "a" ||
		len(got.Buyers[2].Items) != 2 || got.Buyers[0].BuyerEmail != "usr@example.com" {
		t.Fatalf("buyers %+v", got.Buyers)
	}

	want(t, e.do(t, "GET", "/api/events/gone", nil, e.staff), 404, `{"error":"Not found"}`)
	want(t, e.do(t, "GET", "/api/events/nope", nil, e.staff), 404, `{"error":"Not found"}`)

	e.seedEvent(t, "empty", "o1", "Empty", "draft")
	rec = e.do(t, "GET", "/api/events/empty", nil, e.staff)
	if got := decode[map[string]any](t, rec); fmt.Sprint(got["stats"], got["buyers"]) != "[] []" {
		t.Fatalf("empty: %s", rec.Body)
	}
}
