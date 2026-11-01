package admin

import "testing"

func TestDashboard(t *testing.T) {
	e := setup(t)
	e.guarded(t, "GET", "/api/dashboard", nil)

	e.Exec(t, `insert into site_setting(key,value,updated_at) values('midtrans_va_fee','4000',now()),('midtrans_qris_percent','0.7',now())`)
	e.seedOrg(t, "o1", "Org", "active")
	e.seedEvent(t, "e1", "o1", "Big Gig", "published")
	e.seedEvent(t, "e2", "o1", "Small Gig", "published")
	e.seedTicketType(t, "t1", "e1", 100000, 100, 0)
	e.seedTicketType(t, "t2", "e2", 50000, 100, 0)
	e.seedOrder(t, "a", "usr", "e1", "t1", "paid", "va_bank", 2, 100000, "1 day")
	e.seedOrder(t, "b", "usr", "e1", "t1", "paid", "qris", 1, 100000, "40 days")
	e.seedOrder(t, "c", "usr", "e2", "t2", "paid", "qris", 1, 50000, "2 days")
	e.seedOrder(t, "d", "usr", "e2", "t2", "expired", "va_bank", 1, 50000, "1 day")
	e.Exec(t, `insert into payout_request(id,organization_id,amount,bank_name,account_number,account_holder_name,status,created_at,platform_fee)
		values('p1','o1',1,'BNI','1','X','processed',now(),7000),('p2','o1',1,'BNI','1','X','pending',now(),500)`)

	rec := e.do(t, "GET", "/api/dashboard", nil, e.staff)
	assertGolden(t, "dashboard.json", rec)
	type day struct {
		Date    string
		Revenue int64
	}
	got := decode[struct {
		Stats          dashStats
		RevenueByDay   []day
		OrdersByStatus []statusCount
		TopEvents      []eventRevenue
	}](t, rec)
	s := got.Stats
	if s.TotalOrgs != 1 || s.TotalEvents != 2 || s.TotalUsers != 4 || s.TotalOrders != 3 ||
		s.TotalGmv != 388000 || s.TicketSales != 350000 || s.TaxCollected != 35000 || s.ServiceFeeProfit != 3000 ||
		s.PlatformFeeProfit != 7000 {
		t.Fatalf("stats %+v", s)
	}
	if s.EstimatedMidtransCost != 5169 {
		t.Fatalf("midtrans %d", s.EstimatedMidtransCost)
	}
	if len(got.RevenueByDay) != 2 || got.RevenueByDay[0].Revenue != 56000 || got.RevenueByDay[1].Revenue != 221000 {
		t.Fatalf("revenueByDay %+v", got.RevenueByDay)
	}
	if len(got.TopEvents) != 2 || got.TopEvents[0].EventName != "Big Gig" || got.TopEvents[0].Revenue != 332000 {
		t.Fatalf("topEvents %+v", got.TopEvents)
	}
	if len(got.OrdersByStatus) != 2 {
		t.Fatalf("ordersByStatus %+v", got.OrdersByStatus)
	}
}

func TestDashboardEmpty(t *testing.T) {
	e := setup(t)
	want(t, e.do(t, "GET", "/api/dashboard", nil, e.super), 200,
		`{"stats":{"totalOrgs":0,"totalEvents":0,"totalUsers":4,"totalOrders":0,"totalGmv":0,"ticketSales":0,"taxCollected":0,"serviceFeeProfit":0,"platformFeeProfit":0,"estimatedMidtransCost":0},"revenueByDay":[],"ordersByStatus":[],"topEvents":[]}`)
}
