package admin

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/httpx"
)

type scannedTicket struct {
	ID             string      `json:"id"`
	TicketNumber   string      `json:"ticketNumber"`
	Barcode        string      `json:"barcode"`
	TicketTypeName string      `json:"ticketTypeName"`
	Status         string      `json:"status"`
	AttendeeName   *string     `json:"attendeeName"`
	CheckedInAt    *httpx.Time `json:"checkedInAt"`
}

type scanOrderDetail struct {
	OrderID         string          `json:"orderId"`
	ReferenceNumber string          `json:"referenceNumber"`
	OrderBarcode    string          `json:"orderBarcode"`
	OrderStatus     string          `json:"orderStatus"`
	Total           int64           `json:"total"`
	CreatedAt       httpx.Time      `json:"createdAt"`
	EventID         string          `json:"eventId"`
	EventName       string          `json:"eventName"`
	EventStartsAt   httpx.Time      `json:"eventStartsAt"`
	EventVenueName  *string         `json:"eventVenueName"`
	EventCity       *string         `json:"eventCity"`
	BuyerName       string          `json:"buyerName"`
	BuyerEmail      string          `json:"buyerEmail"`
	TotalTickets    int             `json:"totalTickets"`
	CheckedInCount  int             `json:"checkedInCount"`
	RemainingCount  int             `json:"remainingCount"`
	Tickets         []scannedTicket `json:"tickets"`
}

type scanSample struct {
	OrderID         string     `json:"orderId"`
	ReferenceNumber string     `json:"referenceNumber"`
	OrderBarcode    string     `json:"orderBarcode"`
	EventID         string     `json:"eventId"`
	EventName       string     `json:"eventName"`
	BuyerName       string     `json:"buyerName"`
	BuyerEmail      string     `json:"buyerEmail"`
	TotalTickets    int        `json:"totalTickets"`
	CheckedInCount  int        `json:"checkedInCount"`
	CreatedAt       httpx.Time `json:"createdAt"`
}

func (h *handlers) scannerSamples(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := jsTrim(r.URL.Query().Get("eventId"))

	var c conds
	c.add("o.status = ?", "paid")
	c.add("o.barcode is not null")
	if eventID != "" {
		c.add("o.event_id = ?", eventID)
	}

	samples, err := list[scanSample](ctx, db, `
		select o.id, coalesce(o.reference_number, o.id), coalesce(o.barcode, ''),
			o.event_id, e.name, u.name, u.email,
			count(t.id)::int as total_tickets,
			count(t.id) filter (where t.checked_in_at is not null)::int as checked_in_count,
			o.created_at
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		left join ticket t on t.order_id = o.id`+
		c.where()+`
		group by o.id, e.name, u.name, u.email
		order by o.created_at desc
		limit 15`, c.args...)
	if err != nil {
		serverError(w, "scanner samples", err)
		return
	}
	if samples == nil {
		samples = []scanSample{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"samples": samples})
}

func (h *handlers) loadScanOrder(r *http.Request, code string) (*scanOrderDetail, error) {
	ctx, db := r.Context(), h.d.DB
	var d scanOrderDetail
	var ref, barcode *string
	err := db.QueryRow(ctx, `
		select o.id, o.reference_number, o.barcode, o.status, o.total, o.created_at,
			o.event_id, e.name, e.starts_at, e.venue_name, e.city,
			u.name, u.email
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		where o.barcode = $1
		   or o.reference_number = $1
		   or o.id = $1
		   or exists (
		       select 1 from ticket t
		       where t.order_id = o.id and (t.barcode = $1 or t.ticket_number = $1 or t.id = $1)
		   )
		limit 1`, code).Scan(
		&d.OrderID, &ref, &barcode, &d.OrderStatus, &d.Total, &d.CreatedAt,
		&d.EventID, &d.EventName, &d.EventStartsAt, &d.EventVenueName, &d.EventCity,
		&d.BuyerName, &d.BuyerEmail,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if ref != nil {
		d.ReferenceNumber = *ref
	} else {
		d.ReferenceNumber = d.OrderID
	}
	if barcode != nil {
		d.OrderBarcode = *barcode
	}

	tickets, err := list[scannedTicket](ctx, db, `
		select t.id, t.ticket_number, t.barcode, coalesce(tt.name, '-'),
			t.status, t.attendee_name, t.checked_in_at
		from ticket t
		left join ticket_type tt on tt.id = t.ticket_type_id
		where t.order_id = $1
		order by t.created_at asc, t.id asc`, d.OrderID)
	if err != nil {
		return nil, err
	}
	if tickets == nil {
		tickets = []scannedTicket{}
	}
	d.Tickets = tickets
	d.TotalTickets = len(tickets)
	for _, t := range tickets {
		if t.CheckedInAt != nil {
			d.CheckedInCount++
		}
	}
	d.RemainingCount = d.TotalTickets - d.CheckedInCount
	return &d, nil
}

func (h *handlers) scannerLookup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code    string `json:"code"`
		EventID string `json:"eventId"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	code := jsTrim(body.Code)
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "Kode barcode atau Order ID wajib diisi")
		return
	}

	detail, err := h.loadScanOrder(r, code)
	if err != nil {
		serverError(w, "scanner lookup", err)
		return
	}
	if detail == nil {
		httpx.Error(w, http.StatusNotFound, "Tiket tidak ditemukan")
		return
	}
	if eventID := jsTrim(body.EventID); eventID != "" && detail.EventID != eventID {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":  "Tiket ini milik event lain: " + detail.EventName,
			"result": "wrong_event",
			"order":  detail,
		})
		return
	}
	if detail.OrderStatus != "paid" {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":  "Order belum lunas (status: " + detail.OrderStatus + ")",
			"result": "unpaid",
			"order":  detail,
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"result": "ok",
		"order":  detail,
	})
}

func (h *handlers) scannerCheckin(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	var body struct {
		Code     string `json:"code"`
		TicketID string `json:"ticketId"`
		EventID  string `json:"eventId"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	code := jsTrim(body.Code)
	ticketID := jsTrim(body.TicketID)
	if code == "" && ticketID != "" {
		code = ticketID
	}
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "Kode barcode atau Ticket ID wajib diisi")
		return
	}

	detail, err := h.loadScanOrder(r, code)
	if err != nil {
		serverError(w, "scanner checkin lookup", err)
		return
	}
	if detail == nil {
		httpx.Error(w, http.StatusNotFound, "Tiket tidak ditemukan")
		return
	}
	if eventID := jsTrim(body.EventID); eventID != "" && detail.EventID != eventID {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":  "Tiket ini milik event lain: " + detail.EventName,
			"result": "wrong_event",
			"order":  detail,
		})
		return
	}
	if detail.OrderStatus != "paid" {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":  "Order belum lunas (status: " + detail.OrderStatus + ")",
			"result": "unpaid",
			"order":  detail,
		})
		return
	}

	now := time.Now().UTC()
	var checkedInNow int64

	if ticketID != "" {
		tag, err := db.Exec(ctx, `
			update ticket
			set status = 'used', checked_in_at = $1
			where id = $2 and order_id = $3 and checked_in_at is null`,
			now, ticketID, detail.OrderID)
		if err != nil {
			serverError(w, "scanner checkin ticket", err)
			return
		}
		checkedInNow = tag.RowsAffected()
	} else {
		var matchedSingleTicketID string
		for _, t := range detail.Tickets {
			if t.Barcode == code || t.TicketNumber == code {
				matchedSingleTicketID = t.ID
				break
			}
		}
		if matchedSingleTicketID != "" && detail.OrderBarcode != code && detail.ReferenceNumber != code {
			tag, err := db.Exec(ctx, `
				update ticket
				set status = 'used', checked_in_at = $1
				where id = $2 and order_id = $3 and checked_in_at is null`,
				now, matchedSingleTicketID, detail.OrderID)
			if err != nil {
				serverError(w, "scanner checkin single", err)
				return
			}
			checkedInNow = tag.RowsAffected()
		} else {
			tag, err := db.Exec(ctx, `
				update ticket
				set status = 'used', checked_in_at = $1
				where order_id = $2 and checked_in_at is null`,
				now, detail.OrderID)
			if err != nil {
				serverError(w, "scanner checkin order", err)
				return
			}
			checkedInNow = tag.RowsAffected()
		}
	}

	updated, err := h.loadScanOrder(r, detail.OrderID)
	if err != nil {
		serverError(w, "scanner reload", err)
		return
	}

	if checkedInNow == 0 {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"error":        "Tiket sudah pernah di-check-in sebelumnya!",
			"result":       "already_checked_in",
			"checkedInNow": 0,
			"order":        updated,
		})
		return
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"result":       "checked_in",
		"checkedInNow": checkedInNow,
		"order":        updated,
	})
}

func (h *handlers) scannerReset(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	var body struct {
		OrderID string `json:"orderId"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	orderID := jsTrim(body.OrderID)
	if orderID == "" {
		httpx.Error(w, http.StatusBadRequest, "orderId wajib diisi")
		return
	}
	if _, err := db.Exec(ctx, `
		update ticket
		set status = 'valid', checked_in_at = null
		where order_id = $1`, orderID); err != nil {
		serverError(w, "scanner reset", err)
		return
	}
	updated, err := h.loadScanOrder(r, orderID)
	if err != nil {
		serverError(w, "scanner reset reload", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"result": "reset",
		"order":  updated,
	})
}
