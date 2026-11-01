package admin

import (
	"fmt"
	"testing"
)

func TestListUsers(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/users", nil)
	for i := range 21 {
		id := fmt.Sprintf("u%02d", i)
		e.SeedUser(t, id, "user")
		e.Exec(t, `update "user" set created_at = now() - make_interval(mins => $1) where id = $2`, 100-i, id)
	}
	e.Exec(t, `update "user" set created_at = now() - interval '1 day' where id in ('super','adm','stf','usr')`)
	e.Exec(t, `update "user" set name = 'Bob Buyer', email = 'bob@x.com' where id = 'u20'`)
	e.seedOrg(t, "o1", "Org", "active")
	e.seedEvent(t, "e1", "o1", "Gig", "published")
	e.seedTicketType(t, "t1", "e1", 100000, 100, 0)
	e.seedOrder(t, "a", "u20", "e1", "t1", "paid", "qris", 1, 100000, "1 hour")
	e.seedOrder(t, "b", "u20", "e1", "t1", "paid", "qris", 2, 100000, "1 hour")
	e.seedOrder(t, "c", "u20", "e1", "t1", "expired", "qris", 1, 100000, "1 hour")

	rec := e.do(t, "GET", "/api/users", nil, e.staff)
	assertGolden(t, "users.json", rec)
	p := decode[page[userRow]](t, rec)
	if p.Total != 25 || p.TotalPages != 2 || len(p.Data) != 20 || p.Data[0].ID != "u20" || p.Data[0].OrderCount != 2 || p.Data[0].TotalSpent != 332000 || p.Data[1].OrderCount != 0 {
		t.Fatalf("users %+v", p)
	}
	if p = decode[page[userRow]](t, e.do(t, "GET", "/api/users?page=2", nil, e.staff)); len(p.Data) != 5 || p.Page != 2 {
		t.Fatalf("page 2 %+v", p)
	}
	if p = decode[page[userRow]](t, e.do(t, "GET", "/api/users?q=%20BOB%20", nil, e.staff)); p.Total != 1 || p.Data[0].ID != "u20" {
		t.Fatalf("search %+v", p)
	}
	if p = decode[page[userRow]](t, e.do(t, "GET", "/api/users?q=x.com", nil, e.staff)); p.Total != 1 {
		t.Fatalf("email search %+v", p)
	}
}

func TestGetUser(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/users/usr", nil)
	e.Exec(t, `update "user" set gender = 'male', instagram = 'ig' where id = 'usr'`)
	e.seedOrg(t, "o1", "Org", "active")
	e.seedEvent(t, "e1", "o1", "Gig", "published")
	e.seedTicketType(t, "t1", "e1", 100000, 100, 0)
	for i := range 12 {
		status := "paid"
		if i == 0 {
			status = "pending_payment"
		}
		e.seedOrder(t, fmt.Sprintf("o%02d", i), "usr", "e1", "t1", status, "qris", 1, 100000, fmt.Sprintf("%d hours", i))
	}
	e.Exec(t, `update "order" set reference_number = null where id = 'o01'`)

	rec := e.do(t, "GET", "/api/users/usr", nil, e.staff)
	assertGolden(t, "users-{id}.json", rec)
	type resp struct {
		User             userDetail
		Stats            userStats
		Orders           []userOrder
		OrdersPage       int
		OrdersTotalPages int64
	}
	got := decode[resp](t, rec)
	if got.User.ID != "usr" || *got.User.Gender != "male" || got.User.Location != nil ||
		got.Stats != (userStats{12, 11, 1, 1221000}) || len(got.Orders) != 10 || got.OrdersPage != 1 || got.OrdersTotalPages != 2 ||
		got.Orders[0].ID != "o00" || got.Orders[1].ReferenceNumber != "o01" || got.Orders[0].ReferenceNumber != "HX-o00" ||
		got.Orders[0].EventName != "Gig" || got.Orders[0].OrgName != "Org" || got.Orders[0].Total != 111000 {
		t.Fatalf("user %+v", got)
	}
	if got = decode[resp](t, e.do(t, "GET", "/api/users/usr?page=2", nil, e.staff)); len(got.Orders) != 2 || got.OrdersPage != 2 {
		t.Fatalf("page 2 %+v", got)
	}
	want(t, e.do(t, "GET", "/api/users/stf", nil, e.staff), 200,
		`{"user":{"id":"stf","name":"stf","email":"stf@example.com","image":null,"role":"staff","location":null,"gender":null,"instagram":null,"tiktok":null,"createdAt":"`+
			decode[resp](t, e.do(t, "GET", "/api/users/stf", nil, e.staff)).User.CreatedAt.Format("2006-01-02T15:04:05.000Z")+
			`"},"stats":{"totalOrders":0,"paidOrders":0,"pendingOrders":0,"totalSpent":0},"orders":[],"ordersPage":1,"ordersTotalPages":1}`)
	want(t, e.do(t, "GET", "/api/users/nope", nil, e.staff), 404, `{"error":"Not found"}`)
}
