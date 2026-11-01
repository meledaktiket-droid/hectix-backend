package admin

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/httpx"
)

type userRow struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	Image      *string    `json:"image"`
	Role       string     `json:"role"`
	CreatedAt  httpx.Time `json:"createdAt"`
	OrderCount int64      `json:"orderCount"`
	TotalSpent int64      `json:"totalSpent"`
}

func (h *handlers) listUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := pageParam(r, "page")
	var c conds
	if q := jsTrim(r.URL.Query().Get("q")); q != "" {
		like := likeContains(q)
		c.add(`(u.name ilike ? escape '\' or u.email ilike ? escape '\')`, like, like)
	}
	var total int64
	if err := h.d.DB.QueryRow(ctx, `select count(*) from "user" u`+c.where(), c.args...).Scan(&total); err != nil {
		serverError(w, "users", err)
		return
	}
	rows, err := list[userRow](ctx, h.d.DB, `select u.id, u.name, u.email, u.image, u.role, u.created_at,
		count(o.id) filter (where o.status = 'paid'), coalesce(sum(o.total) filter (where o.status = 'paid'), 0)
		from "user" u left join "order" o on o.user_id = u.id`+c.where()+`
		group by u.id order by u.created_at desc limit `+itoa(pageLimit)+` offset `+itoa((p-1)*pageLimit), c.args...)
	if err != nil {
		serverError(w, "users", err)
		return
	}
	httpx.JSON(w, http.StatusOK, page[userRow]{rows, total, p, totalPages(total, pageLimit)})
}

const userOrdersLimit = 10

type userDetail struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	Image     *string    `json:"image"`
	Role      string     `json:"role"`
	Location  *string    `json:"location"`
	Gender    *string    `json:"gender"`
	Instagram *string    `json:"instagram"`
	Tiktok    *string    `json:"tiktok"`
	CreatedAt httpx.Time `json:"createdAt"`
}

type userStats struct {
	TotalOrders   int64 `json:"totalOrders"`
	PaidOrders    int64 `json:"paidOrders"`
	PendingOrders int64 `json:"pendingOrders"`
	TotalSpent    int64 `json:"totalSpent"`
}

type userOrder struct {
	ID              string     `json:"id"`
	ReferenceNumber string     `json:"referenceNumber"`
	Status          string     `json:"status"`
	Total           int64      `json:"total"`
	CreatedAt       httpx.Time `json:"createdAt"`
	EventName       string     `json:"eventName"`
	OrgName         string     `json:"orgName"`
}

func (h *handlers) getUser(w http.ResponseWriter, r *http.Request) {
	ctx, db, id := r.Context(), h.d.DB, chi.URLParam(r, "id")
	ordersPage := pageParam(r, "page")
	u, err := one[userDetail](ctx, db, `select id, name, email, image, role, location, gender, instagram, tiktok, created_at
		from "user" where id = $1`, id)
	if err != nil {
		serverError(w, "user", err)
		return
	}
	if u == nil {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	var s userStats
	if err := db.QueryRow(ctx, `select count(*),
		count(*) filter (where status = 'paid'), count(*) filter (where status = 'pending_payment'),
		coalesce(sum(total) filter (where status = 'paid'), 0)
		from "order" where user_id = $1`, id).Scan(&s.TotalOrders, &s.PaidOrders, &s.PendingOrders, &s.TotalSpent); err != nil {
		serverError(w, "user", err)
		return
	}
	orders, err := list[userOrder](ctx, db, `select o.id, coalesce(o.reference_number, o.id), o.status, o.total, o.created_at,
		e.name, org.name
		from "order" o join event e on o.event_id = e.id join organization org on e.organization_id = org.id
		where o.user_id = $1 order by o.created_at desc
		limit `+itoa(userOrdersLimit)+` offset `+itoa((ordersPage-1)*userOrdersLimit), id)
	if err != nil {
		serverError(w, "user", err)
		return
	}
	httpx.JSON(w, http.StatusOK, struct {
		User             *userDetail `json:"user"`
		Stats            userStats   `json:"stats"`
		Orders           []userOrder `json:"orders"`
		OrdersPage       int         `json:"ordersPage"`
		OrdersTotalPages int64       `json:"ordersTotalPages"`
	}{u, s, orders, ordersPage, totalPages(s.TotalOrders, userOrdersLimit)})
}
