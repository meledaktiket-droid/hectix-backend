package organizer

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
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
	now := time.Now().UTC()
	t, err := scanTicket(h.db.QueryRow(r.Context(), `insert into ticket_type(id, event_id, name, price, quantity, benefits,
		display_order, created_at, updated_at) values($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8) returning `+ticketCols,
		dbx.UUID(), id, name, price, qty, benefits, order, now))
	if err != nil {
		serverError(w, "create ticket", err)
		return
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
