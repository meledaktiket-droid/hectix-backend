package organizer

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/httpx"
)

type orderSummary struct {
	Revenue       int64            `json:"revenue"`
	Counts        map[string]int64 `json:"counts"`
	VoucherCount  int64            `json:"voucherCount"`
	DiscountTotal int64            `json:"discountTotal"`
}

type orderListRow struct {
	ID              string             `json:"id"`
	Status          string             `json:"status"`
	Subtotal        int64              `json:"subtotal"`
	CreatedAt       httpx.Time         `json:"createdAt"`
	VoucherCode     *string            `json:"voucherCode"`
	VoucherDiscount int64              `json:"voucherDiscount"`
	PaymentMethod   string             `json:"paymentMethod"`
	Bank            *string            `json:"bank"`
	ReferenceNumber string             `json:"referenceNumber"`
	EventName       string             `json:"eventName"`
	BuyerName       string             `json:"buyerName"`
	BuyerEmail      string             `json:"buyerEmail"`
	Items           []orderItemSummary `json:"items"`
}

func (h *handlers) orgEvents(r *http.Request, orgID string) ([]string, map[string]string, error) {
	rows, err := h.db.Query(r.Context(), `select id, name from event where organization_id = $1`, orgID)
	if err != nil {
		return nil, nil, err
	}
	var ids []string
	names := map[string]string{}
	var id, name string
	_, err = pgx.ForEachRow(rows, []any{&id, &name}, func() error {
		ids = append(ids, id)
		names[id] = name
		return nil
	})
	return ids, names, err
}

func (h *handlers) listOrders(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	ids, names, err := h.orgEvents(r, c.orgID)
	if err != nil {
		serverError(w, "orders events", err)
		return
	}
	summary := orderSummary{Counts: map[string]int64{}}
	if len(ids) == 0 {
		httpx.JSON(w, http.StatusOK, map[string]any{"orders": []any{}, "total": 0, "summary": summary})
		return
	}
	p := parseOrderParams(r.URL.Query())
	cond := buildOrderConditions(p, ids)
	const from = ` from "order" o join "user" u on u.id = o.user_id where `

	where, args := cond.sql(false)
	rows, err := h.db.Query(ctx, `select o.status, count(*), coalesce(sum(o.subtotal), 0),
		coalesce(sum(o.voucher_discount), 0), count(*) filter (where o.voucher_discount > 0)`+from+where+
		` group by o.status`, args...)
	if err != nil {
		serverError(w, "orders summary", err)
		return
	}
	var status string
	var cnt, sum, vSum, vCnt int64
	if _, err := pgx.ForEachRow(rows, []any{&status, &cnt, &sum, &vSum, &vCnt}, func() error {
		summary.Counts[status] = cnt
		if status == "paid" {
			summary.Revenue, summary.DiscountTotal, summary.VoucherCount = sum, vSum, vCnt
		}
		return nil
	}); err != nil {
		serverError(w, "orders summary", err)
		return
	}

	where, args = cond.sql(true)
	var total int64
	if err := h.db.QueryRow(ctx, `select count(*)`+from+where, args...).Scan(&total); err != nil {
		serverError(w, "orders total", err)
		return
	}

	args = append(args, p.PageSize, (p.Page-1)*p.PageSize)
	rows, err = h.db.Query(ctx, `select o.id, o.status, o.subtotal, o.created_at, o.voucher_code, o.voucher_discount,
		o.payment_method, o.bank, coalesce(o.reference_number, o.id), o.event_id, u.name, u.email`+from+where+
		` order by o.created_at desc limit $`+strconv.Itoa(len(args)-1)+` offset $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		serverError(w, "orders", err)
		return
	}
	orders := []*orderListRow{}
	var orderIDs []string
	for rows.Next() {
		var o orderListRow
		var eventID string
		if err := rows.Scan(&o.ID, &o.Status, &o.Subtotal, &o.CreatedAt, &o.VoucherCode, &o.VoucherDiscount,
			&o.PaymentMethod, &o.Bank, &o.ReferenceNumber, &eventID, &o.BuyerName, &o.BuyerEmail); err != nil {
			rows.Close()
			serverError(w, "orders", err)
			return
		}
		o.EventName = names[eventID]
		if _, ok := names[eventID]; !ok {
			o.EventName = "Unknown event"
		}
		orders = append(orders, &o)
		orderIDs = append(orderIDs, o.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		serverError(w, "orders", err)
		return
	}
	items, err := itemsByOrder(ctx, h.db, orderIDs)
	if err != nil {
		serverError(w, "orders items", err)
		return
	}
	for _, o := range orders {
		o.Items = itemsOf(items, o.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"orders": orders, "total": total, "summary": summary})
}

type ticketStat struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Price    int64  `json:"price"`
	Capacity int64  `json:"capacity"`
	Sold     int64  `json:"sold"`
	Revenue  int64  `json:"revenue"`
}

type buyer struct {
	OrderID    string             `json:"orderId"`
	Status     string             `json:"status"`
	CreatedAt  httpx.Time         `json:"createdAt"`
	BuyerName  string             `json:"buyerName"`
	BuyerEmail string             `json:"buyerEmail"`
	Items      []orderItemSummary `json:"items"`
}

func (h *handlers) eventOrders(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	ctx := r.Context()
	rows, err := h.db.Query(ctx, `select tt.id, tt.name, tt.price, tt.quantity,
			coalesce(s.sold, 0), coalesce(s.revenue, 0)
		from ticket_type tt
		left join (select oi.ticket_type_id, sum(oi.quantity) sold, sum(oi.quantity::bigint * oi.price) revenue
			from order_item oi join "order" o on o.id = oi.order_id and o.status = 'paid'
			where oi.ticket_type_id in (select id from ticket_type where event_id = $1)
			group by oi.ticket_type_id) s on s.ticket_type_id = tt.id
		where tt.event_id = $1 order by tt.display_order`, id)
	if err != nil {
		serverError(w, "event orders stats", err)
		return
	}
	stats := []ticketStat{}
	var s ticketStat
	if _, err := pgx.ForEachRow(rows, []any{&s.ID, &s.Name, &s.Price, &s.Capacity, &s.Sold, &s.Revenue}, func() error {
		stats = append(stats, s)
		return nil
	}); err != nil {
		serverError(w, "event orders stats", err)
		return
	}

	rows, err = h.db.Query(ctx, `select o.id, o.status, o.created_at, u.name, u.email
		from "order" o join "user" u on u.id = o.user_id where o.event_id = $1 order by o.created_at desc`, id)
	if err != nil {
		serverError(w, "event orders", err)
		return
	}
	buyers := []*buyer{}
	var orderIDs []string
	var b buyer
	if _, err := pgx.ForEachRow(rows, []any{&b.OrderID, &b.Status, &b.CreatedAt, &b.BuyerName, &b.BuyerEmail}, func() error {
		cp := b
		buyers = append(buyers, &cp)
		orderIDs = append(orderIDs, b.OrderID)
		return nil
	}); err != nil {
		serverError(w, "event orders", err)
		return
	}
	items, err := itemsByOrder(ctx, h.db, orderIDs)
	if err != nil {
		serverError(w, "event orders items", err)
		return
	}
	for _, b := range buyers {
		b.Items = itemsOf(items, b.OrderID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"stats": stats, "buyers": buyers})
}
