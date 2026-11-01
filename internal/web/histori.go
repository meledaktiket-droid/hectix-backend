package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/ticketpdf"
)

type tier struct {
	TicketTypeName string `json:"ticketTypeName"`
	Quantity       int    `json:"quantity"`
}

type paidOrder struct {
	OrderID         string     `json:"orderId"`
	ReferenceNumber *string    `json:"-"`
	EventID         string     `json:"eventId"`
	EventName       string     `json:"eventName"`
	EventBannerURL  *string    `json:"eventBannerUrl"`
	EventStartsAt   httpx.Time `json:"eventStartsAt"`
	Tiers           []tier     `json:"tiers"`
	TicketCount     int        `json:"ticketCount"`
}

func (h *handlers) paidOrders(ctx context.Context, userID, filter string, limit, offset int) ([]paidOrder, bool, error) {
	rows, err := h.db.Query(ctx, `
		select o.id, o.reference_number, o.event_id, e.name, e.banner_url, e.starts_at
		from "order" o join event e on e.id = o.event_id
		where o.user_id = $1 and o.status = 'paid' `+filter+`
		order by e.starts_at desc, o.id
		limit $2 offset $3`, userID, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	orders, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (paidOrder, error) {
		var o paidOrder
		return o, row.Scan(&o.OrderID, &o.ReferenceNumber, &o.EventID, &o.EventName, &o.EventBannerURL, &o.EventStartsAt)
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(orders) > limit
	if hasMore {
		orders = orders[:limit]
	}
	if len(orders) == 0 {
		return []paidOrder{}, false, nil
	}
	ids := make([]string, len(orders))
	for i, o := range orders {
		ids[i] = o.OrderID
	}
	rows, err = h.db.Query(ctx, `select order_id, ticket_type_name, quantity from order_item where order_id = any($1)`, ids)
	if err != nil {
		return nil, false, err
	}
	byOrder := map[string][]tier{}
	var id string
	var t tier
	if _, err := pgx.ForEachRow(rows, []any{&id, &t.TicketTypeName, &t.Quantity}, func() error {
		byOrder[id] = append(byOrder[id], t)
		return nil
	}); err != nil {
		return nil, false, err
	}
	for i := range orders {
		orders[i].Tiers = nonNil(byOrder[orders[i].OrderID])
		for _, t := range orders[i].Tiers {
			orders[i].TicketCount += t.Quantity
		}
	}
	return orders, hasMore, nil
}

func (h *handlers) histori(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, offset := page(q)
	selesai := q.Get("tab") == "selesai"
	filter, label := "and coalesce(e.ends_at, e.starts_at) > now()", "Akan Datang"
	if selesai {
		filter, label = "and coalesce(e.ends_at, e.starts_at) <= now()", "Selesai"
	}
	orders, hasMore, err := h.paidOrders(r.Context(), auth.UserFrom(r.Context()).ID, filter, limit, offset)
	if err != nil {
		serverError(w, "histori", err)
		return
	}
	type historiOrder struct {
		paidOrder
		ReferenceNumber *string `json:"referenceNumber"`
		StatusLabel     string  `json:"statusLabel"`
	}
	out := make([]historiOrder, len(orders))
	for i, o := range orders {
		out[i] = historiOrder{paidOrder: o, ReferenceNumber: o.ReferenceNumber, StatusLabel: label}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"orders": out, "nextOffset": nextOffset(hasMore, offset, limit)})
}

func (h *handlers) historiDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID := chi.URLParam(r, "orderId")
	var o struct {
		ID              string      `json:"id"`
		ReferenceNumber *string     `json:"referenceNumber"`
		Barcode         *string     `json:"barcode"`
		Status          string      `json:"status"`
		PaymentMethod   string      `json:"paymentMethod"`
		Bank            *string     `json:"bank"`
		Subtotal        int         `json:"subtotal"`
		Tax             int         `json:"tax"`
		ServiceFee      int         `json:"serviceFee"`
		Total           int         `json:"total"`
		VoucherDiscount int         `json:"voucherDiscount"`
		CreatedAt       httpx.Time  `json:"createdAt"`
		EventID         string      `json:"eventId"`
		EventName       string      `json:"eventName"`
		EventStartsAt   httpx.Time  `json:"eventStartsAt"`
		EventBannerURL  *string     `json:"eventBannerUrl"`
		EventVenueName  *string     `json:"eventVenueName"`
		EventCity       *string     `json:"eventCity"`
		BuyerName       string      `json:"buyerName"`
		Items           []orderLine `json:"items"`
	}
	err := h.db.QueryRow(ctx, `
		select o.id, o.reference_number, o.barcode, o.status, o.payment_method, o.bank,
			o.subtotal, o.tax, o.service_fee, o.total, o.voucher_discount, o.created_at,
			o.event_id, e.name, e.starts_at, e.banner_url, e.venue_name, e.city, u.name
		from "order" o join event e on e.id = o.event_id join "user" u on u.id = o.user_id
		where o.id = $1 and o.user_id = $2`, orderID, auth.UserFrom(ctx).ID).Scan(
		&o.ID, &o.ReferenceNumber, &o.Barcode, &o.Status, &o.PaymentMethod, &o.Bank,
		&o.Subtotal, &o.Tax, &o.ServiceFee, &o.Total, &o.VoucherDiscount, &o.CreatedAt,
		&o.EventID, &o.EventName, &o.EventStartsAt, &o.EventBannerURL, &o.EventVenueName, &o.EventCity, &o.BuyerName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "histori detail", err)
		return
	}
	if o.Items, err = h.orderLines(ctx, orderID); err != nil {
		serverError(w, "histori detail items", err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func plainText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	w.WriteHeader(status)
	w.Write([]byte(msg))
}

func (h *handlers) ticketPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		plainText(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	u := auth.UserFrom(ctx)
	if u == nil {
		plainText(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	orderID := chi.URLParam(r, "orderId")
	var (
		ref, barcode, venue, city, banner *string
		total                             int64
		createdAt, startsAt               time.Time
		eventName, buyerName, status      string
	)
	err := h.db.QueryRow(ctx, `
		select o.status, o.reference_number, o.barcode, o.total, o.created_at,
			e.name, e.starts_at, e.banner_url, e.venue_name, e.city, u.name
		from "order" o join event e on e.id = o.event_id join "user" u on u.id = o.user_id
		where o.id = $1 and o.user_id = $2`, orderID, u.ID).Scan(
		&status, &ref, &barcode, &total, &createdAt, &eventName, &startsAt, &banner, &venue, &city, &buyerName)
	if errors.Is(err, pgx.ErrNoRows) {
		plainText(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		slog.Error("web: ticket pdf", "err", err)
		plainText(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	if status != "paid" || str(barcode) == "" || str(ref) == "" {
		plainText(w, http.StatusConflict, "Ticket not ready")
		return
	}

	rows, err := h.db.Query(ctx, `select ticket_type_name, quantity from order_item where order_id = $1`, orderID)
	if err != nil {
		slog.Error("web: ticket pdf items", "err", err)
		plainText(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	ticketType, quantity := "-", 0
	first := true
	var name string
	var qty int
	if _, err := pgx.ForEachRow(rows, []any{&name, &qty}, func() error {
		if first {
			ticketType, first = name, false
		}
		quantity += qty
		return nil
	}); err != nil {
		slog.Error("web: ticket pdf items", "err", err)
		plainText(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	pdf, err := ticketpdf.Render(ticketpdf.Data{
		EventName:       eventName,
		EventStartsAt:   startsAt,
		EventVenueName:  str(venue),
		EventCity:       str(city),
		EventBannerURL:  str(banner),
		BuyerName:       buyerName,
		TicketTypeName:  ticketType,
		Quantity:        quantity,
		ReferenceNumber: *ref,
		CreatedAt:       createdAt,
		Total:           total,
		Barcode:         *barcode,
	}, h.banners)
	if err != nil {
		slog.Error("web: ticket pdf render", "order", orderID, "err", err)
		plainText(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="hectix-ticket-%s.pdf"`, *ref))
	w.Write(pdf)
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
