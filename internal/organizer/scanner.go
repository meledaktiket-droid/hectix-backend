package organizer

import (
	"errors"
	"net/http"
	"strings"

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

type scanEventOption struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	StartsAt httpx.Time `json:"startsAt"`
}

func (h *handlers) scannerEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := h.scannerOrg(w, r)
	if !ok {
		return
	}
	rows, err := h.db.Query(r.Context(), `
		select id, name, status, starts_at
		from event
		where organization_id = $1 and deleted_at is null
		order by starts_at desc`, c.orgID)
	if err != nil {
		serverError(w, "scanner events", err)
		return
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[scanEventOption])
	if err != nil {
		serverError(w, "scanner events scan", err)
		return
	}
	if events == nil {
		events = []scanEventOption{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"events": events,
		"role":   c.role,
	})
}

func (h *handlers) scannerSamples(w http.ResponseWriter, r *http.Request) {
	c, ok := h.scannerOrg(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	eventID := strings.TrimSpace(r.URL.Query().Get("eventId"))

	args := []any{c.orgID}
	where := ` where e.organization_id = $1 and e.deleted_at is null and o.status = 'paid' and o.barcode is not null`
	if eventID != "" {
		args = append(args, eventID)
		where += ` and o.event_id = $2`
	}

	rows, err := h.db.Query(ctx, `
		select o.id, coalesce(o.reference_number, o.id), coalesce(o.barcode, ''),
			o.event_id, e.name, u.name, u.email,
			count(t.id)::int as total_tickets,
			count(t.id) filter (where t.checked_in_at is not null)::int as checked_in_count,
			o.created_at
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		left join ticket t on t.order_id = o.id`+
		where+`
		group by o.id, e.name, u.name, u.email
		order by o.created_at desc
		limit 15`, args...)
	if err != nil {
		serverError(w, "scanner samples", err)
		return
	}
	samples, err := pgx.CollectRows(rows, pgx.RowToStructByPos[scanSample])
	if err != nil {
		serverError(w, "scanner samples scan", err)
		return
	}
	if samples == nil {
		samples = []scanSample{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"samples": samples,
		"role":    c.role,
	})
}

func (h *handlers) loadOrgScanOrder(r *http.Request, orgID, code string) (*scanOrderDetail, error) {
	ctx := r.Context()
	var d scanOrderDetail
	var ref, barcode *string
	err := h.db.QueryRow(ctx, `
		select o.id, o.reference_number, o.barcode, o.status, o.total, o.created_at,
			o.event_id, e.name, e.starts_at, e.venue_name, e.city,
			u.name, u.email
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		where e.organization_id = $1
		  and e.deleted_at is null
		  and (
		      o.barcode = $2
		      or o.reference_number = $2
		      or o.id = $2
		      or exists (
		          select 1 from ticket t
		          where t.order_id = o.id and (t.barcode = $2 or t.ticket_number = $2 or t.id = $2)
		      )
		  )
		limit 1`, orgID, code).Scan(
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

	rows, err := h.db.Query(ctx, `
		select t.id, t.ticket_number, t.barcode, coalesce(tt.name, '-'),
			t.status, t.attendee_name, t.checked_in_at
		from ticket t
		left join ticket_type tt on tt.id = t.ticket_type_id
		where t.order_id = $1
		order by t.created_at asc, t.id asc`, d.OrderID)
	if err != nil {
		return nil, err
	}
	tickets, err := pgx.CollectRows(rows, pgx.RowToStructByPos[scannedTicket])
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
	c, ok := h.scannerOrg(w, r)
	if !ok {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	codeStr, _ := b.str("code")
	eventIDStr, _ := b.str("eventId")
	code := strings.TrimSpace(codeStr)
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "Kode barcode atau Order ID wajib diisi")
		return
	}

	detail, err := h.loadOrgScanOrder(r, c.orgID, code)
	if err != nil {
		serverError(w, "scanner lookup", err)
		return
	}
	if detail == nil {
		httpx.Error(w, http.StatusNotFound, "Tiket tidak ditemukan di organisasi ini")
		return
	}
	if eventID := strings.TrimSpace(eventIDStr); eventID != "" && detail.EventID != eventID {
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
	c, ok := h.scannerOrg(w, r)
	if !ok {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	codeStr, _ := b.str("code")
	ticketIDStr, _ := b.str("ticketId")
	eventIDStr, _ := b.str("eventId")
	code := strings.TrimSpace(codeStr)
	ticketID := strings.TrimSpace(ticketIDStr)
	if code == "" && ticketID != "" {
		code = ticketID
	}
	if code == "" {
		httpx.Error(w, http.StatusBadRequest, "Kode barcode atau Ticket ID wajib diisi")
		return
	}

	detail, err := h.loadOrgScanOrder(r, c.orgID, code)
	if err != nil {
		serverError(w, "scanner checkin lookup", err)
		return
	}
	if detail == nil {
		httpx.Error(w, http.StatusNotFound, "Tiket tidak ditemukan di organisasi ini")
		return
	}
	if eventID := strings.TrimSpace(eventIDStr); eventID != "" && detail.EventID != eventID {
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

	ctx := r.Context()
	now := h.clock().UTC()
	var checkedInNow int64

	if ticketID != "" {
		tag, err := h.db.Exec(ctx, `
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
			tag, err := h.db.Exec(ctx, `
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
			tag, err := h.db.Exec(ctx, `
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

	updated, err := h.loadOrgScanOrder(r, c.orgID, detail.OrderID)
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
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	orderIDStr, _ := b.str("orderId")
	orderID := strings.TrimSpace(orderIDStr)
	if orderID == "" {
		httpx.Error(w, http.StatusBadRequest, "orderId wajib diisi")
		return
	}
	detail, err := h.loadOrgScanOrder(r, c.orgID, orderID)
	if err != nil {
		serverError(w, "scanner reset lookup", err)
		return
	}
	if detail == nil {
		notFound(w)
		return
	}
	if _, err := h.db.Exec(r.Context(), `
		update ticket
		set status = 'valid', checked_in_at = null
		where order_id = $1`, detail.OrderID); err != nil {
		serverError(w, "scanner reset", err)
		return
	}
	updated, err := h.loadOrgScanOrder(r, c.orgID, detail.OrderID)
	if err != nil {
		serverError(w, "scanner reset reload", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"result": "reset",
		"order":  updated,
	})
}
