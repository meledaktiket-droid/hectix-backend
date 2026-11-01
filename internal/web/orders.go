package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/payments"
)

type orderLine struct {
	TicketTypeName string `json:"ticketTypeName"`
	Price          int    `json:"price"`
	Quantity       int    `json:"quantity"`
}

func (h *handlers) orderLines(ctx context.Context, orderID string) ([]orderLine, error) {
	rows, err := h.db.Query(ctx, `select ticket_type_name, price, quantity from order_item where order_id = $1`, orderID)
	if err != nil {
		return nil, err
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (orderLine, error) {
		var l orderLine
		return l, row.Scan(&l.TicketTypeName, &l.Price, &l.Quantity)
	})
	return nonNil(items), err
}

func (h *handlers) getOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID := chi.URLParam(r, "orderId")
	var o struct {
		ID              string      `json:"id"`
		Status          string      `json:"status"`
		PaymentMethod   string      `json:"paymentMethod"`
		Bank            *string     `json:"bank"`
		VANumber        *string     `json:"vaNumber"`
		ReferenceNumber *string     `json:"referenceNumber"`
		QrisURL         *string     `json:"qrisUrl"`
		Subtotal        int         `json:"subtotal"`
		Tax             int         `json:"tax"`
		ServiceFee      int         `json:"serviceFee"`
		Total           int         `json:"total"`
		VoucherDiscount int         `json:"voucherDiscount"`
		SendEticket     bool        `json:"sendEticket"`
		ExpiresAt       httpx.Time  `json:"expiresAt"`
		EventID         string      `json:"eventId"`
		EventName       string      `json:"eventName"`
		EventStartsAt   httpx.Time  `json:"eventStartsAt"`
		EventBannerURL  *string     `json:"eventBannerUrl"`
		Items           []orderLine `json:"items"`
	}
	err := h.db.QueryRow(ctx, `
		select o.id, o.status, o.payment_method, o.bank, o.va_number, o.reference_number, o.qris_url,
			o.subtotal, o.tax, o.service_fee, o.total, o.voucher_discount, o.send_eticket, o.expires_at,
			o.event_id, e.name, e.starts_at, e.banner_url
		from "order" o join event e on e.id = o.event_id
		where o.id = $1 and o.user_id = $2`, orderID, auth.UserFrom(ctx).ID).Scan(
		&o.ID, &o.Status, &o.PaymentMethod, &o.Bank, &o.VANumber, &o.ReferenceNumber, &o.QrisURL,
		&o.Subtotal, &o.Tax, &o.ServiceFee, &o.Total, &o.VoucherDiscount, &o.SendEticket, &o.ExpiresAt,
		&o.EventID, &o.EventName, &o.EventStartsAt, &o.EventBannerURL)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "order", err)
		return
	}
	if o.Items, err = h.orderLines(ctx, orderID); err != nil {
		serverError(w, "order items", err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}

func (h *handlers) cancelOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orderID := chi.URLParam(r, "orderId")
	var status string
	err := h.db.QueryRow(ctx, `select status from "order" where id = $1 and user_id = $2`, orderID, auth.UserFrom(ctx).ID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "cancel order", err)
		return
	}
	if status != "pending_payment" {
		httpx.Error(w, http.StatusBadRequest, "Cannot cancel")
		return
	}

	if err := h.cancelCharge(ctx, orderID); err != nil {
		slog.Warn("web: midtrans cancel", "order", orderID, "err", err)
		httpx.Error(w, http.StatusConflict, "Pesanan tidak bisa dibatalkan, mungkin sudah dibayar.")
		return
	}

	ctx = context.WithoutCancel(ctx)
	if err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `update "order" set status='cancelled', updated_at=now() where id=$1 and status='pending_payment'`, orderID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		return payments.Release(ctx, tx, orderID)
	}); err != nil {
		serverError(w, "cancel order", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
