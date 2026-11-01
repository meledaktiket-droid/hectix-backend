package organizer

import (
	"testing"
)

func TestDashboard(t *testing.T) {
	f := setup(t)
	authCases(t, f, "GET", "/api/dashboard", nil)

	got := ok200(t, f.do(t, "GET", "/api/dashboard", nil, f.member), "")
	if s := got["stats"].(map[string]any); s["totalEvents"] != 0.0 || s["totalRevenue"] != 0.0 {
		t.Fatalf("empty stats %v", s)
	}

	f.event(t, "e1", "org1")
	f.event(t, "e2", "org1")
	f.event(t, "gone", "org1")
	f.Exec(t, `update event set deleted_at = now() where id = 'gone'`)
	f.event(t, "x1", "org2")
	f.ticket(t, "t1", "e1", 1000, 10, 5, 0)
	f.ticket(t, "t2", "e1", 500, 5, 0, 1)
	f.ticket(t, "tx", "x1", 1000, 10, 0, 0)
	f.order(t, "o1", "e1", "buyer", "paid", "t1", 1000, 2, "now")
	f.order(t, "o2", "e1", "buyer", "paid", "t1", 1000, 3, "now")
	f.order(t, "o3", "e1", "buyer", "expired", "t1", 1000, 1, "now")
	f.order(t, "o4", "e1", "buyer", "paid", "t2", 500, 1, "2020-01-01")
	f.order(t, "ox", "x1", "buyer", "paid", "tx", 1000, 9, "now")

	got = ok200(t, f.do(t, "GET", "/api/dashboard", nil, f.member), "dashboard.json")
	s := got["stats"].(map[string]any)
	if s["totalRevenue"] != 5500.0 || s["totalOrders"] != 3.0 || s["totalEvents"] != 2.0 || s["totalTicketsSold"] != 6.0 {
		t.Fatalf("stats %v", s)
	}
	if rbd := got["revenueByDay"].([]any); len(rbd) != 1 || rbd[0].(map[string]any)["revenue"] != 5000.0 {
		t.Fatalf("revenueByDay %v", rbd)
	}
	if obs := got["ordersByStatus"].([]any); len(obs) != 2 {
		t.Fatalf("ordersByStatus %v", obs)
	}
	if te := got["topEvents"].([]any); len(te) != 1 || te[0].(map[string]any)["eventName"] != "Event e1" {
		t.Fatalf("topEvents %v", te)
	}
	up := got["upcomingEvents"].([]any)
	if len(up) != 2 {
		t.Fatalf("upcoming %v", up)
	}
	for _, u := range up {
		u := u.(map[string]any)
		switch u["id"] {
		case "e1":
			if u["sold"] != 6.0 || u["capacity"] != 15.0 {
				t.Fatalf("e1 %v", u)
			}
		case "e2":
			if u["sold"] != 0.0 || u["capacity"] != 0.0 {
				t.Fatalf("e2 %v", u)
			}
		default:
			t.Fatalf("unexpected %v", u)
		}
	}
}
