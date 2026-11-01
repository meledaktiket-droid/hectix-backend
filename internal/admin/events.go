package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/httpx"
)

const pageLimit = 20

type eventRow struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Status           string      `json:"status"`
	StartsAt         httpx.Time  `json:"startsAt"`
	EndsAt           *httpx.Time `json:"endsAt"`
	City             *string     `json:"city"`
	VenueName        *string     `json:"venueName"`
	OrganizationID   string      `json:"organizationId"`
	OrganizationName string      `json:"organizationName"`
	CreatedAt        httpx.Time  `json:"createdAt"`
}

type page[T any] struct {
	Data       []T   `json:"data"`
	Total      int64 `json:"total"`
	Page       int   `json:"page"`
	TotalPages int64 `json:"totalPages"`
}

func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := pageParam(r, "page")
	status := r.URL.Query().Get("status")
	if status != "" && status != "draft" && status != "published" {
		httpx.Error(w, http.StatusBadRequest, "Invalid status")
		return
	}
	search := jsTrim(r.URL.Query().Get("q"))

	var c conds
	if status != "" {
		c.add("e.status = ?", status)
	}
	if search != "" {
		c.add(`e.name ilike ? escape '\'`, likeContains(search))
	}
	where, args := c.where(), c.args

	rows, err := list[eventRow](ctx, h.d.DB, `select e.id, e.name, e.status, e.starts_at, e.ends_at, e.city, e.venue_name,
		e.organization_id, o.name, e.created_at
		from event e join organization o on e.organization_id = o.id`+where+`
		order by e.created_at desc limit `+itoa(pageLimit)+` offset `+itoa((p-1)*pageLimit), args...)
	if err != nil {
		serverError(w, "events", err)
		return
	}
	var total int64
	if err := h.d.DB.QueryRow(ctx, `select count(*) from event e`+where, args...).Scan(&total); err != nil {
		serverError(w, "events", err)
		return
	}
	httpx.JSON(w, http.StatusOK, page[eventRow]{rows, total, p, totalPages(total, pageLimit)})
}

type eventDetail struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Status           string      `json:"status"`
	StartsAt         httpx.Time  `json:"startsAt"`
	EndsAt           *httpx.Time `json:"endsAt"`
	VenueName        *string     `json:"venueName"`
	City             *string     `json:"city"`
	OrganizationID   string      `json:"organizationId"`
	OrganizationName string      `json:"organizationName"`
}

type ticketStat struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Capacity int64  `json:"capacity"`
	Sold     int64  `json:"sold"`
	Revenue  int64  `json:"revenue"`
}

type orderItemSummary struct {
	OrderID        string `json:"orderId"`
	TicketTypeName string `json:"ticketTypeName"`
	Quantity       int64  `json:"quantity"`
}

type buyer struct {
	OrderID    string             `json:"orderId"`
	Status     string             `json:"status"`
	CreatedAt  httpx.Time         `json:"createdAt"`
	BuyerName  string             `json:"buyerName"`
	BuyerEmail string             `json:"buyerEmail"`
	Items      []orderItemSummary `json:"items"`
}

func (h *handlers) getEvent(w http.ResponseWriter, r *http.Request) {
	ctx, db, id := r.Context(), h.d.DB, chi.URLParam(r, "id")
	ev, err := one[eventDetail](ctx, db, `select e.id, e.name, e.status, e.starts_at, e.ends_at, e.venue_name, e.city,
		e.organization_id, o.name
		from event e join organization o on e.organization_id = o.id
		where e.id = $1 and e.deleted_at is null`, id)
	if err != nil {
		serverError(w, "event", err)
		return
	}
	if ev == nil {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	stats, err := list[ticketStat](ctx, db, `select t.id, t.name, t.price, t.quantity,
		coalesce(s.sold, 0), coalesce(s.revenue, 0)
		from ticket_type t
		left join (select oi.ticket_type_id, sum(oi.quantity) sold, sum(oi.quantity * oi.price) revenue
			from order_item oi join "order" o on oi.order_id = o.id and o.status = 'paid'
			where oi.ticket_type_id in (select id from ticket_type where event_id = $1)
			group by oi.ticket_type_id) s on s.ticket_type_id = t.id
		where t.event_id = $1 order by t.display_order`, id)
	if err != nil {
		serverError(w, "event", err)
		return
	}
	type orderRow struct {
		OrderID    string
		Status     string
		CreatedAt  httpx.Time
		BuyerName  string
		BuyerEmail string
	}
	orders, err := list[orderRow](ctx, db, `select o.id, o.status, o.created_at, u.name, u.email
		from "order" o join "user" u on o.user_id = u.id
		where o.event_id = $1 order by o.created_at desc`, id)
	if err != nil {
		serverError(w, "event", err)
		return
	}
	ids := make([]string, len(orders))
	for i, o := range orders {
		ids[i] = o.OrderID
	}
	items, err := itemsByOrder(r, h, ids)
	if err != nil {
		serverError(w, "event", err)
		return
	}
	buyers := make([]buyer, len(orders))
	for i, o := range orders {
		buyers[i] = buyer{o.OrderID, o.Status, o.CreatedAt, o.BuyerName, o.BuyerEmail, items.of(o.OrderID)}
	}
	httpx.JSON(w, http.StatusOK, struct {
		Event  *eventDetail `json:"event"`
		Stats  []ticketStat `json:"stats"`
		Buyers []buyer      `json:"buyers"`
	}{ev, stats, buyers})
}

type itemMap map[string][]orderItemSummary

func (m itemMap) of(id string) []orderItemSummary {
	if v := m[id]; v != nil {
		return v
	}
	return []orderItemSummary{}
}

func itemsByOrder(r *http.Request, h *handlers, ids []string) (itemMap, error) {
	m := itemMap{}
	if len(ids) == 0 {
		return m, nil
	}
	items, err := list[orderItemSummary](r.Context(), h.d.DB, `select order_id, ticket_type_name, quantity
		from order_item where order_id = any($1)`, ids)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		m[it.OrderID] = append(m[it.OrderID], it)
	}
	return m, nil
}
