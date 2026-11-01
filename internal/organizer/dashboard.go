package organizer

import (
	"net/http"
	"time"

	"hectix/backend/internal/httpx"
)

type dashStats struct {
	TotalRevenue     int64 `json:"totalRevenue"`
	TotalOrders      int64 `json:"totalOrders"`
	TotalEvents      int64 `json:"totalEvents"`
	TotalTicketsSold int64 `json:"totalTicketsSold"`
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

type upcomingEvent struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	StartsAt httpx.Time `json:"startsAt"`
	City     *string    `json:"city"`
	Sold     int64      `json:"sold"`
	Capacity int64      `json:"capacity"`
}

type dashboardResp struct {
	Stats          dashStats       `json:"stats"`
	RevenueByDay   []dayRevenue    `json:"revenueByDay"`
	OrdersByStatus []statusCount   `json:"ordersByStatus"`
	TopEvents      []eventRevenue  `json:"topEvents"`
	UpcomingEvents []upcomingEvent `json:"upcomingEvents"`
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	resp := dashboardResp{
		RevenueByDay: []dayRevenue{}, OrdersByStatus: []statusCount{},
		TopEvents: []eventRevenue{}, UpcomingEvents: []upcomingEvent{},
	}
	var anyEvents bool
	if err := h.db.QueryRow(ctx, `select exists(select 1 from event where organization_id = $1)`, c.orgID).Scan(&anyEvents); err != nil {
		serverError(w, "dashboard", err)
		return
	}
	if !anyEvents {
		httpx.JSON(w, http.StatusOK, resp)
		return
	}
	now := time.Now().UTC()
	thirtyDaysAgo := now.AddDate(0, 0, -30)
	const orgOrders = `o.event_id in (select id from event where organization_id = $1)`

	err := h.db.QueryRow(ctx, `select
		coalesce((select sum(o.subtotal) from "order" o where `+orgOrders+` and o.status = 'paid'), 0),
		(select count(*) from "order" o where `+orgOrders+` and o.status = 'paid'),
		(select count(*) from event where organization_id = $1 and deleted_at is null),
		coalesce((select sum(oi.quantity) from order_item oi join "order" o on o.id = oi.order_id
			where o.status = 'paid' and `+orgOrders+`), 0)`, c.orgID).
		Scan(&resp.Stats.TotalRevenue, &resp.Stats.TotalOrders, &resp.Stats.TotalEvents, &resp.Stats.TotalTicketsSold)
	if err != nil {
		serverError(w, "dashboard stats", err)
		return
	}

	rows, err := h.db.Query(ctx, `select date_trunc('day', o.created_at)::date::text, coalesce(sum(o.subtotal), 0)
		from "order" o where o.status = 'paid' and `+orgOrders+` and o.created_at >= $2
		group by date_trunc('day', o.created_at) order by date_trunc('day', o.created_at)`, c.orgID, thirtyDaysAgo)
	if err != nil {
		serverError(w, "dashboard revenueByDay", err)
		return
	}
	for rows.Next() {
		var d dayRevenue
		if err := rows.Scan(&d.Date, &d.Revenue); err != nil {
			rows.Close()
			serverError(w, "dashboard revenueByDay", err)
			return
		}
		resp.RevenueByDay = append(resp.RevenueByDay, d)
	}
	rows.Close()

	rows, err = h.db.Query(ctx, `select o.status, count(*) from "order" o where `+orgOrders+` group by o.status`, c.orgID)
	if err != nil {
		serverError(w, "dashboard ordersByStatus", err)
		return
	}
	for rows.Next() {
		var s statusCount
		if err := rows.Scan(&s.Status, &s.Count); err != nil {
			rows.Close()
			serverError(w, "dashboard ordersByStatus", err)
			return
		}
		resp.OrdersByStatus = append(resp.OrdersByStatus, s)
	}
	rows.Close()

	rows, err = h.db.Query(ctx, `select e.name, coalesce(sum(o.subtotal), 0) from "order" o join event e on e.id = o.event_id
		where o.status = 'paid' and `+orgOrders+` group by e.name order by sum(o.subtotal) desc limit 5`, c.orgID)
	if err != nil {
		serverError(w, "dashboard topEvents", err)
		return
	}
	for rows.Next() {
		var t eventRevenue
		if err := rows.Scan(&t.EventName, &t.Revenue); err != nil {
			rows.Close()
			serverError(w, "dashboard topEvents", err)
			return
		}
		resp.TopEvents = append(resp.TopEvents, t)
	}
	rows.Close()

	rows, err = h.db.Query(ctx, `select e.id, e.name, e.starts_at, e.city,
			coalesce((select sum(oi.quantity) from order_item oi join "order" o on o.id = oi.order_id
				where o.event_id = e.id and o.status = 'paid'), 0),
			(select sum(tt.quantity) from ticket_type tt where tt.event_id = e.id)
		from event e where e.organization_id = $1 and e.deleted_at is null and e.starts_at > $2
		order by e.starts_at limit 5`, c.orgID, now)
	if err != nil {
		serverError(w, "dashboard upcoming", err)
		return
	}
	for rows.Next() {
		var u upcomingEvent
		var capacity *int64
		if err := rows.Scan(&u.ID, &u.Name, &u.StartsAt, &u.City, &u.Sold, &capacity); err != nil {
			rows.Close()
			serverError(w, "dashboard upcoming", err)
			return
		}
		if capacity == nil {
			u.Sold = 0
		} else {
			u.Capacity = *capacity
		}
		resp.UpcomingEvents = append(resp.UpcomingEvents, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, "dashboard upcoming", err)
		return
	}
	httpx.JSON(w, http.StatusOK, resp)
}
