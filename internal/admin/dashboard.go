package admin

import (
	"net/http"
	"time"

	"hectix/backend/internal/httpx"
	"hectix/backend/internal/settings"
)

type dashStats struct {
	TotalOrgs             int64 `json:"totalOrgs"`
	TotalEvents           int64 `json:"totalEvents"`
	TotalUsers            int64 `json:"totalUsers"`
	TotalOrders           int64 `json:"totalOrders"`
	TotalGmv              int64 `json:"totalGmv"`
	TicketSales           int64 `json:"ticketSales"`
	TaxCollected          int64 `json:"taxCollected"`
	ServiceFeeProfit      int64 `json:"serviceFeeProfit"`
	PlatformFeeProfit     int64 `json:"platformFeeProfit"`
	EstimatedMidtransCost int64 `json:"estimatedMidtransCost"`
}

type dayRevenue struct {
	Date    string `json:"date"`
	Revenue int64  `json:"revenue"`
}

type statusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type eventRevenue struct {
	EventName string `json:"eventName"`
	Revenue   int64  `json:"revenue"`
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	var s dashStats
	var vaOrderCount, qrisTotal int64
	err := db.QueryRow(ctx, `select
		(select count(*) from organization),
		(select count(*) from event),
		(select count(*) from "user"),
		count(*) filter (where status = 'paid'),
		coalesce(sum(total) filter (where status = 'paid'), 0),
		coalesce(sum(subtotal) filter (where status = 'paid'), 0),
		coalesce(sum(tax) filter (where status = 'paid'), 0),
		coalesce(sum(service_fee) filter (where status = 'paid'), 0),
		count(*) filter (where status = 'paid' and payment_method = 'va_bank'),
		coalesce(sum(total) filter (where status = 'paid' and payment_method = 'qris'), 0),
		(select coalesce(sum(platform_fee), 0) from payout_request where status = 'processed')
		from "order"`).Scan(&s.TotalOrgs, &s.TotalEvents, &s.TotalUsers, &s.TotalOrders, &s.TotalGmv,
		&s.TicketSales, &s.TaxCollected, &s.ServiceFeeProfit, &vaOrderCount, &qrisTotal, &s.PlatformFeeProfit)
	if err != nil {
		serverError(w, "dashboard", err)
		return
	}

	thirtyDaysAgo := time.Now().UTC().AddDate(0, 0, -30)
	byDay, err := list[dayRevenue](ctx, db, `select date_trunc('day', created_at)::date::text, coalesce(sum(total), 0)
		from "order" where status = 'paid' and created_at >= $1
		group by date_trunc('day', created_at) order by date_trunc('day', created_at)`, thirtyDaysAgo)
	if err != nil {
		serverError(w, "dashboard", err)
		return
	}
	byStatus, err := list[statusCount](ctx, db, `select status, count(*) from "order" group by status`)
	if err != nil {
		serverError(w, "dashboard", err)
		return
	}
	top, err := list[eventRevenue](ctx, db, `select e.name, coalesce(sum(o.total), 0) from "order" o
		join event e on o.event_id = e.id where o.status = 'paid'
		group by o.event_id, e.name order by sum(o.total) desc limit 5`)
	if err != nil {
		serverError(w, "dashboard", err)
		return
	}
	st, err := settings.Get(ctx, db)
	if err != nil {
		serverError(w, "dashboard", err)
		return
	}
	s.EstimatedMidtransCost = int64(jsRound(float64(vaOrderCount)*st.MidtransVaFee + float64(qrisTotal)*st.MidtransQrisPercent/100))

	httpx.JSON(w, http.StatusOK, struct {
		Stats          dashStats      `json:"stats"`
		RevenueByDay   []dayRevenue   `json:"revenueByDay"`
		OrdersByStatus []statusCount  `json:"ordersByStatus"`
		TopEvents      []eventRevenue `json:"topEvents"`
	}{s, byDay, byStatus, top})
}
