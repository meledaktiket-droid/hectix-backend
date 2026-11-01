package organizer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

func (h *handlers) liveEvent(w http.ResponseWriter, ctx context.Context, orgID, id string) *eventRow {
	e, err := scanEvent(h.db.QueryRow(ctx, `select `+eventCols+` from event
		where id = $1 and organization_id = $2 and deleted_at is null`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w)
		return nil
	}
	if err != nil {
		serverError(w, "event", err)
		return nil
	}
	return e
}

func blank(s *string) bool { return s == nil || *s == "" }

func (h *handlers) publishEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok || !h.requireActiveOrg(w, r, c) {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	ev := h.liveEvent(w, ctx, c.orgID, id)
	if ev == nil {
		return
	}
	var tickets int64
	if err := h.db.QueryRow(ctx, `select count(*) from ticket_type where event_id = $1`, id).Scan(&tickets); err != nil {
		serverError(w, "publish", err)
		return
	}
	var cats []any
	_ = json.Unmarshal(ev.Categories, &cats)

	errs := []string{}
	if ev.Name == "" {
		errs = append(errs, "Event name is required")
	}
	if ev.StartsAt.IsZero() {
		errs = append(errs, "Start date is required")
	}
	if ev.EndsAt == nil {
		errs = append(errs, "End date is required")
	}
	if blank(ev.VenueName) {
		errs = append(errs, "Venue name is required")
	}
	if blank(ev.Address) {
		errs = append(errs, "Address is required")
	}
	if blank(ev.City) {
		errs = append(errs, "City is required")
	}
	if len(cats) == 0 {
		errs = append(errs, "At least one category is required")
	}
	if tickets == 0 {
		errs = append(errs, "At least one ticket type is required")
	}
	if len(errs) > 0 {
		httpx.JSON(w, http.StatusUnprocessableEntity, struct {
			Error   string   `json:"error"`
			Details []string `json:"details"`
		}{"Cannot publish", errs})
		return
	}
	h.setStatus(w, ctx, id, "published")
}

func (h *handlers) setStatus(w http.ResponseWriter, ctx context.Context, id, status string) {
	e, err := scanEvent(h.db.QueryRow(ctx, `update event set status = $2, updated_at = $3 where id = $1 returning `+eventCols,
		id, status, time.Now().UTC()))
	if err != nil {
		serverError(w, "event status", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"event": e})
}

func (h *handlers) unpublishEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	paid, err := paidOrderCount(ctx, h.db, id)
	if err != nil {
		serverError(w, "unpublish", err)
		return
	}
	if paid > 0 {
		httpx.Error(w, http.StatusConflict, "Cannot unpublish an event that has sold tickets")
		return
	}
	h.setStatus(w, ctx, id, "draft")
}

func (h *handlers) duplicateEvent(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	src := h.liveEvent(w, ctx, c.orgID, id)
	if src == nil {
		return
	}
	now := time.Now().UTC()
	newID := dbx.UUID()
	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into event(id, organization_id, name, description, venue_name, address, city,
			banner_url, categories, starts_at, ends_at, status, visibility, attendee_form, deleted_at, created_at, updated_at)
			values($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, null, 'draft', $11, $12, null, $10, $10)`,
			newID, c.orgID, "Copy of "+src.Name, src.Description, src.VenueName, src.Address, src.City,
			src.BannerURL, string(src.Categories), now, src.Visibility, src.AttendeeForm); err != nil {
			return err
		}
		tickets, err := list(ctx, tx, scanTicket, `select `+ticketCols+` from ticket_type where event_id = $1 order by display_order`, id)
		if err != nil {
			return err
		}
		for _, t := range tickets {
			var benefits any
			if t.Benefits != nil {
				benefits = string(t.Benefits)
			}
			if _, err := tx.Exec(ctx, `insert into ticket_type(id, event_id, name, price, quantity, benefits, display_order,
				created_at, updated_at) values($1, $2, $3, $4, $5, $6::jsonb, $7, $8, $8)`,
				dbx.UUID(), newID, t.Name, t.Price, t.Quantity, benefits, t.DisplayOrder, now); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `insert into performer(id, event_id, name, image_url, display_order, created_at, updated_at)
			select gen_random_uuid()::text, $2, name, image_url, display_order, $3, $3
			from performer where event_id = $1 and deleted_at is null`, id, newID, now); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `insert into voucher(id, event_id, code, type, amount, usage_limit, usage_count, expires_at, created_at)
			select gen_random_uuid()::text, $2, code, type, amount, usage_limit, 0, expires_at, $3
			from voucher where event_id = $1 and deleted_at is null`, id, newID, now)
		return err
	})
	if err != nil {
		serverError(w, "duplicate", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]string{"id": newID})
}
