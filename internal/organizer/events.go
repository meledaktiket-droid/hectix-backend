package organizer

import (
	"context"
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

type eventWithSales struct {
	*eventRow
	SoldCount int64 `json:"soldCount"`
	Capacity  int64 `json:"capacity"`
}

func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	events, err := list(ctx, h.db, scanEvent, `select `+eventCols+` from event
		where organization_id = $1 and deleted_at is null order by created_at desc`, c.orgID)
	if err != nil {
		serverError(w, "events", err)
		return
	}
	out := make([]eventWithSales, 0, len(events))
	if len(events) > 0 {
		capacity, sold := map[string]int64{}, map[string]int64{}
		ids := make([]string, len(events))
		for i, e := range events {
			ids[i] = e.ID
		}
		if err := sumBy(ctx, h, capacity, `select event_id, sum(quantity) from ticket_type
			where event_id = any($1) group by event_id`, ids); err != nil {
			serverError(w, "events capacity", err)
			return
		}
		if err := sumBy(ctx, h, sold, `select o.event_id, sum(oi.quantity) from order_item oi
			join "order" o on o.id = oi.order_id and o.status = 'paid'
			where o.event_id = any($1) group by o.event_id`, ids); err != nil {
			serverError(w, "events sold", err)
			return
		}
		for _, e := range events {
			out = append(out, eventWithSales{e, sold[e.ID], capacity[e.ID]})
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": out})
}

func sumBy(ctx context.Context, h *handlers, m map[string]int64, sql string, args ...any) error {
	rows, err := h.db.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	var k string
	var v *int64
	_, err = pgx.ForEachRow(rows, []any{&k, &v}, func() error {
		if v != nil {
			m[k] = *v
		} else {
			m[k] = 0
		}
		return nil
	})
	return err
}

func eventFields(b body, create bool) (map[string]any, string) {
	set := map[string]any{}
	for _, f := range []struct{ key, col string }{
		{"description", "description"}, {"venueName", "venue_name"}, {"address", "address"},
		{"city", "city"}, {"bannerUrl", "banner_url"},
	} {
		if !b.has(f.key) {
			continue
		}
		v, ok := b.nullStr(f.key)
		if !ok {
			return nil, "Invalid " + f.key
		}
		set[f.col] = v
	}
	if b.has("visibility") && !(create && b.isNull("visibility")) {
		v, ok := b.str("visibility")
		if !ok {
			return nil, "Invalid visibility"
		}
		set["visibility"] = v
	}
	if b.has("attendeeForm") && !(create && b.isNull("attendeeForm")) {
		var v bool
		switch strings.TrimSpace(string(b["attendeeForm"])) {
		case "true":
			v = true
		case "false":
		default:
			return nil, "Invalid attendeeForm"
		}
		set["attendee_form"] = v
	}
	if b.has("categories") {
		cats, _ := marshal(sanitizeCategories(b["categories"]))
		set["categories"] = string(cats)
	}
	if b.has("endsAt") {
		if !b.truthy("endsAt") {
			set["ends_at"] = nil
		} else if t, ok := b.date("endsAt"); ok {
			set["ends_at"] = t
		} else {
			return nil, "Invalid endsAt"
		}
	}
	return set, ""
}

func (h *handlers) createEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	set, msg := eventFields(b, true)
	if msg != "" {
		httpx.Error(w, http.StatusBadRequest, msg)
		return
	}
	now := time.Now().UTC()
	name := "Untitled Event"
	if s, ok := b.str("name"); ok && s != "" {
		name = s
	}
	startsAt := now
	if b.truthy("startsAt") {
		t, ok := b.date("startsAt")
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid startsAt")
			return
		}
		startsAt = t
	}
	vals := map[string]any{
		"description": nil, "venue_name": nil, "address": nil, "city": nil, "banner_url": nil, "ends_at": nil,
		"visibility": "public", "attendee_form": false, "categories": "[]",
	}
	for k, v := range set {
		vals[k] = v
	}
	e, err := scanEvent(h.db.QueryRow(r.Context(), `insert into event(id, organization_id, name, description,
		venue_name, address, city, banner_url, starts_at, ends_at, status, visibility, attendee_form, categories,
		created_at, updated_at)
		values($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'draft', $11, $12, $13::jsonb, $14, $14)
		returning `+eventCols,
		dbx.UUID(), c.orgID, name, vals["description"], vals["venue_name"], vals["address"], vals["city"],
		vals["banner_url"], startsAt, vals["ends_at"], vals["visibility"], vals["attendee_form"], vals["categories"], now))
	if err != nil {
		serverError(w, "create event", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"event": e})
}

func (h *handlers) getEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	e, err := scanEvent(h.db.QueryRow(ctx, `select `+eventCols+` from event
		where id = $1 and organization_id = $2 and deleted_at is null`, id, c.orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return
	}
	if err != nil {
		serverError(w, "event", err)
		return
	}
	tickets, err := list(ctx, h.db, scanTicket, `select `+ticketCols+` from ticket_type where event_id = $1 order by display_order`, id)
	if err != nil {
		serverError(w, "event tickets", err)
		return
	}
	performers, err := list(ctx, h.db, scanPerformer, `select `+performerCols+` from performer
		where event_id = $1 and deleted_at is null order by display_order`, id)
	if err != nil {
		serverError(w, "event performers", err)
		return
	}
	vouchers, err := list(ctx, h.db, scanVoucher, `select `+voucherCols+` from voucher where event_id = $1 and deleted_at is null`, id)
	if err != nil {
		serverError(w, "event vouchers", err)
		return
	}
	paid, err := paidOrderCount(ctx, h.db, id)
	if err != nil {
		serverError(w, "event paid count", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"event": e, "tickets": tickets, "performers": performers, "vouchers": vouchers, "paidOrderCount": paid,
	})
}

func (h *handlers) updateEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	set, msg := eventFields(b, false)
	if msg != "" {
		httpx.Error(w, http.StatusBadRequest, msg)
		return
	}
	if b.has("name") {
		s, ok := b.str("name")
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid name")
			return
		}
		set["name"] = s
	}
	if b.has("startsAt") {
		t, ok := b.date("startsAt")
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid startsAt")
			return
		}
		set["starts_at"] = t
	}
	set["updated_at"] = time.Now().UTC()

	var cols []string
	args := []any{id}
	for col, v := range set {
		args = append(args, v)
		cast := ""
		if col == "categories" {
			cast = "::jsonb"
		}
		cols = append(cols, fmt.Sprintf("%s = $%d%s", col, len(args), cast))
	}
	e, err := scanEvent(h.db.QueryRow(r.Context(), `update event set `+strings.Join(cols, ", ")+
		` where id = $1 returning `+eventCols, args...))
	if err != nil {
		serverError(w, "update event", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"event": e})
}

func (h *handlers) deleteEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	ctx := r.Context()
	paid, err := paidOrderCount(ctx, h.db, id)
	if err != nil {
		serverError(w, "delete event", err)
		return
	}
	if paid > 0 {
		httpx.Error(w, http.StatusConflict, "Cannot delete an event that has sold tickets")
		return
	}
	now := time.Now().UTC()
	if _, err := h.db.Exec(ctx, `update event set deleted_at = $2, updated_at = $2 where id = $1`, id, now); err != nil {
		serverError(w, "delete event", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
