package organizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

const (
	msgTicketHasOrders   = "Tidak bisa menghapus tipe tiket yang sudah memiliki pesanan."
	msgQuantityBelowSold = "Kuota tiket tidak boleh kurang dari jumlah tiket yang sudah terjual."
)

func (h *handlers) listTickets(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	tickets, err := list(r.Context(), h.db, scanTicket, `select `+ticketCols+` from ticket_type where event_id = $1 order by display_order`, id)
	if err != nil {
		serverError(w, "tickets", err)
		return
	}
	_ = h.attachTicketDates(r.Context(), tickets)
	httpx.JSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func isJSONArray(raw []byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '['
}

func (h *handlers) createTicket(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	if !c.canEdit() {
		forbidden(w)
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	name, _ := b.str("name")
	if name == "" {
		httpx.Error(w, http.StatusBadRequest, "Name is required")
		return
	}
	price, ok := b.integer("price")
	if !ok || price < 0 {
		httpx.Error(w, http.StatusBadRequest, "Valid price is required")
		return
	}
	qty, ok := b.integer("quantity")
	if !ok || qty < 1 {
		httpx.Error(w, http.StatusBadRequest, "Quantity must be at least 1")
		return
	}
	benefits := "[]"
	if isJSONArray(b["benefits"]) {
		benefits = string(b["benefits"])
	}
	var order int64
	if _, isNum := b.num("displayOrder"); isNum {
		if order, ok = b.integer("displayOrder"); !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid displayOrder")
			return
		}
	}
	var validDates []string
	if b.has("validDates") {
		var okDates bool
		validDates, okDates = parseValidDates(b["validDates"])
		if !okDates {
			httpx.Error(w, http.StatusBadRequest, "Invalid validDates")
			return
		}
	}
	now := time.Now().UTC()
	t, err := scanTicket(h.db.QueryRow(r.Context(), `insert into ticket_type(id, event_id, name, price, quantity, benefits,
		display_order, created_at, updated_at) values($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8) returning `+ticketCols,
		dbx.UUID(), id, name, price, qty, benefits, order, now))
	if err != nil {
		serverError(w, "create ticket", err)
		return
	}
	if b.has("validDates") {
		if err := h.syncTicketDatesAndSessions(r.Context(), id, t.ID, t.Name, t.Quantity, validDates); err != nil {
			serverError(w, "sync ticket dates", err)
			return
		}
		t.ValidDates = validDates
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"ticket": t})
}

func (h *handlers) updateTicket(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id, ticketID := chi.URLParam(r, "id"), chi.URLParam(r, "ticketId")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	ctx := r.Context()
	var exists bool
	if err := h.db.QueryRow(ctx, `select exists(select 1 from ticket_type where id = $1 and event_id = $2)`, ticketID, id).Scan(&exists); err != nil {
		serverError(w, "ticket", err)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "Ticket not found")
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	set := map[string]any{}
	if b.has("name") {
		s, ok := b.str("name")
		if !ok || s == "" {
			httpx.Error(w, http.StatusBadRequest, "Name is required")
			return
		}
		set["name"] = s
	}
	if b.has("price") {
		v, ok := b.integer("price")
		if !ok || v < 0 {
			httpx.Error(w, http.StatusBadRequest, "Valid price is required")
			return
		}
		set["price"] = v
	}
	if b.has("quantity") {
		v, ok := b.integer("quantity")
		if !ok || v < 1 {
			httpx.Error(w, http.StatusBadRequest, "Quantity must be at least 1")
			return
		}
		set["quantity"] = v
	}
	if b.has("benefits") {
		switch {
		case b.isNull("benefits"):
			set["benefits"] = nil
		case isJSONArray(b["benefits"]):
			set["benefits"] = string(b["benefits"])
		default:
			httpx.Error(w, http.StatusBadRequest, "Invalid benefits")
			return
		}
	}
	if b.has("displayOrder") {
		v, ok := b.integer("displayOrder")
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid displayOrder")
			return
		}
		set["display_order"] = v
	}
	var validDates []string
	hasDates := b.has("validDates")
	if hasDates {
		var okDates bool
		validDates, okDates = parseValidDates(b["validDates"])
		if !okDates {
			httpx.Error(w, http.StatusBadRequest, "Invalid validDates")
			return
		}
	}
	set["updated_at"] = time.Now().UTC()
	var cols []string
	args := []any{ticketID, id}
	for col, v := range set {
		args = append(args, v)
		cast := ""
		if col == "benefits" {
			cast = "::jsonb"
		}
		cols = append(cols, fmt.Sprintf("%s = $%d%s", col, len(args), cast))
	}
	t, err := scanTicket(h.db.QueryRow(ctx, `update ticket_type set `+strings.Join(cols, ", ")+
		` where id = $1 and event_id = $2 returning `+ticketCols, args...))
	if code, constraint := pgCode(err); code == "23514" && constraint == "ticket_type_sold_within_quantity" {
		httpx.Error(w, http.StatusConflict, msgQuantityBelowSold)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Ticket not found")
		return
	}
	if err != nil {
		serverError(w, "update ticket", err)
		return
	}
	if hasDates {
		if err := h.syncTicketDatesAndSessions(ctx, id, t.ID, t.Name, t.Quantity, validDates); err != nil {
			serverError(w, "sync ticket dates", err)
			return
		}
		t.ValidDates = validDates
	} else {
		_ = h.attachTicketDates(ctx, []*ticketRow{t})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ticket": t})
}

func (h *handlers) deleteTicket(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id, ticketID := chi.URLParam(r, "id"), chi.URLParam(r, "ticketId")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	ctx := r.Context()
	var refs int64
	if err := h.db.QueryRow(ctx, `select count(*) from order_item where ticket_type_id = $1`, ticketID).Scan(&refs); err != nil {
		serverError(w, "delete ticket", err)
		return
	}
	if refs > 0 {
		httpx.Error(w, http.StatusConflict, msgTicketHasOrders)
		return
	}
	_, err := h.db.Exec(ctx, `delete from ticket_type where id = $1 and event_id = $2`, ticketID, id)
	if code, _ := pgCode(err); code == "23503" {
		httpx.Error(w, http.StatusConflict, msgTicketHasOrders)
		return
	}
	if err != nil {
		serverError(w, "delete ticket", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func parseValidDates(raw json.RawMessage) ([]string, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, true
	}
	var in []string
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, false
	}
	var out []string
	for _, s := range in {
		d := strings.TrimSpace(s)
		if d == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return nil, false
		}
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out, true
}

func (h *handlers) attachTicketDates(ctx context.Context, tickets []*ticketRow) error {
	if len(tickets) == 0 {
		return nil
	}
	ids := make([]string, len(tickets))
	byID := make(map[string]*ticketRow, len(tickets))
	for i, t := range tickets {
		ids[i] = t.ID
		byID[t.ID] = t
	}
	rows, err := h.db.Query(ctx, `
		select ticket_type_id, to_char(valid_date, 'YYYY-MM-DD')
		from ticket_type_dates
		where ticket_type_id = any($1)
		order by valid_date asc`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tid, d string
		if err := rows.Scan(&tid, &d); err == nil {
			if t := byID[tid]; t != nil {
				t.ValidDates = append(t.ValidDates, d)
			}
		}
	}
	return rows.Err()
}

var soldOutSuffixRe = regexp.MustCompile(`(?i)\s*\((?:sudah\s+habis|sold\s+out|habis)\)\s*$`)

func cleanSessionTicketName(name string) string {
	if cleaned := strings.TrimSpace(soldOutSuffixRe.ReplaceAllString(strings.TrimSpace(name), "")); cleaned != "" {
		return cleaned
	}
	return strings.TrimSpace(name)
}

func (h *handlers) syncTicketDatesAndSessions(ctx context.Context, eventID, ticketID, ticketName string, qty int64, validDates []string) error {
	sesName := cleanSessionTicketName(ticketName)
	return dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var existingSesID string
		_ = tx.QueryRow(ctx, `
			select session_id from ticket_type_dates
			where ticket_type_id = $1 and session_id is not null
			limit 1`, ticketID).Scan(&existingSesID)

		if _, err := tx.Exec(ctx, `delete from ticket_type_dates where ticket_type_id = $1`, ticketID); err != nil {
			return err
		}

		var sessionIDs []string
		if len(validDates) > 0 {
			rangeStart, err := time.Parse("2006-01-02", validDates[0])
			if err != nil {
				return err
			}
			lastDay, err := time.Parse("2006-01-02", validDates[len(validDates)-1])
			if err != nil {
				return err
			}
			rangeEnd := lastDay.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

			sesID := existingSesID
			if sesID == "" {
				_ = tx.QueryRow(ctx, `
					select id from event_session
					where event_id = $1 and name = $2
					limit 1`, eventID, sesName).Scan(&sesID)
			}
			if sesID == "" {
				sesID = dbx.UUID()
				if _, err := tx.Exec(ctx, `
					insert into event_session(id, event_id, name, starts_at, ends_at, status, quota_limit)
					values($1, $2, $3, $4, $5, 'open', $6)`,
					sesID, eventID, sesName, rangeStart, rangeEnd, qty); err != nil {
					return err
				}
			} else {
				if _, err := tx.Exec(ctx, `
					update event_session
					set name = $1, starts_at = $2, ends_at = $3, quota_limit = $4
					where id = $5 and event_id = $6`,
					sesName, rangeStart, rangeEnd, qty, sesID, eventID); err != nil {
					return err
				}
			}

			for _, d := range validDates {
				if _, err := tx.Exec(ctx, `
					insert into ticket_type_dates(id, ticket_type_id, event_id, valid_date, session_id)
					values($1, $2, $3, $4::date, $5)
					on conflict (ticket_type_id, valid_date) do update set session_id = excluded.session_id`,
					dbx.UUID(), ticketID, eventID, d, sesID); err != nil {
					return err
				}
			}
			sessionIDs = []string{sesID}
		}

		sesJSON, _ := json.Marshal(sessionIDs)
		accessType := "SPECIFIC_DATES"
		if len(sessionIDs) == 0 {
			accessType = "ALL_DAYS"
		}
		_, err := tx.Exec(ctx, `
			insert into ticket_type_access_map(ticket_type_id, event_id, access_type, session_ids, updated_at)
			values($1, $2, $3, $4::jsonb, now())
			on conflict (ticket_type_id) do update set
				access_type = excluded.access_type,
				session_ids = excluded.session_ids,
				updated_at = now()`,
			ticketID, eventID, accessType, string(sesJSON))
		return err
	})
}
