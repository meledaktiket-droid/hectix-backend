package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
)

func (h *handlers) profile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := auth.UserFrom(ctx).ID
	var p struct {
		Name         string     `json:"name"`
		Email        string     `json:"email"`
		Image        *string    `json:"image"`
		CoverImage   *string    `json:"coverImage"`
		Location     *string    `json:"location"`
		Gender       *string    `json:"gender"`
		Instagram    *string    `json:"instagram"`
		Tiktok       *string    `json:"tiktok"`
		CreatedAt    httpx.Time `json:"-"`
		MemberSince  int        `json:"memberSince"`
		EventCount   int        `json:"eventCount"`
		CommentCount int        `json:"commentCount"`
	}
	err := h.db.QueryRow(ctx, `
		select name, email, image, cover_image, location, gender, instagram, tiktok, created_at,
			(select count(*) from wall_comment where user_id = u.id),
			(select count(distinct t.event_id) from ticket t join "order" o on o.id = t.order_id where o.user_id = u.id)
		from "user" u where id = $1`, userID).Scan(
		&p.Name, &p.Email, &p.Image, &p.CoverImage, &p.Location, &p.Gender, &p.Instagram, &p.Tiktok, &p.CreatedAt,
		&p.CommentCount, &p.EventCount)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "profile", err)
		return
	}
	p.MemberSince = p.CreatedAt.UTC().Year()
	httpx.JSON(w, http.StatusOK, p)
}

var profileFields = []struct{ key, col string }{
	{"name", "name"}, {"image", "image"}, {"coverImage", "cover_image"}, {"location", "location"},
	{"gender", "gender"}, {"instagram", "instagram"}, {"tiktok", "tiktok"},
}

func (h *handlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if err := httpx.Decode(r, &body); err != nil || body == nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid body")
		return
	}
	var sets []string
	var args []any
	for _, f := range profileFields {
		raw, ok := body[f.key]
		if !ok {
			continue
		}
		var v *string
		if err := json.Unmarshal(raw, &v); err != nil {
			if f.key == "name" {
				continue
			}
			httpx.Error(w, http.StatusBadRequest, "Invalid "+f.key)
			return
		}
		if f.key == "name" {
			if v == nil || strings.TrimSpace(*v) == "" {
				continue
			}
		} else if v != nil && *v == "" {
			v = nil
		}
		args = append(args, v)
		sets = append(sets, f.col+"=$"+strconv.Itoa(len(args)))
	}
	if len(sets) == 0 {
		httpx.Error(w, http.StatusBadRequest, "No fields to update")
		return
	}
	args = append(args, auth.UserFrom(r.Context()).ID)
	if _, err := h.db.Exec(r.Context(), `update "user" set `+strings.Join(sets, ", ")+`, updated_at=now() where id=$`+strconv.Itoa(len(args)), args...); err != nil {
		serverError(w, "update profile", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handlers) profileActivity(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r.URL.Query())
	activity, hasMore, err := h.paidOrders(r.Context(), auth.UserFrom(r.Context()).ID, "", limit, offset)
	if err != nil {
		serverError(w, "profile activity", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"activity": activity, "nextOffset": nextOffset(hasMore, offset, limit)})
}

func (h *handlers) profileTicket(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := auth.UserFrom(ctx).ID
	orderID := chi.URLParam(r, "orderId")
	type wallEntry struct {
		Content   string     `json:"content"`
		CreatedAt httpx.Time `json:"createdAt"`
	}
	var o struct {
		ID              string      `json:"id"`
		ReferenceNumber *string     `json:"referenceNumber"`
		Barcode         *string     `json:"barcode"`
		CreatedAt       httpx.Time  `json:"createdAt"`
		EventID         string      `json:"eventId"`
		EventName       string      `json:"eventName"`
		EventStartsAt   httpx.Time  `json:"eventStartsAt"`
		EventBannerURL  *string     `json:"eventBannerUrl"`
		EventVenueName  *string     `json:"eventVenueName"`
		EventCity       *string     `json:"eventCity"`
		BuyerName       string      `json:"buyerName"`
		Items           []orderLine `json:"items"`
		Comment         *wallEntry  `json:"comment"`
	}
	err := h.db.QueryRow(ctx, `
		select o.id, o.reference_number, o.barcode, o.created_at,
			o.event_id, e.name, e.starts_at, e.banner_url, e.venue_name, e.city, u.name
		from "order" o join event e on e.id = o.event_id join "user" u on u.id = o.user_id
		where o.id = $1 and o.user_id = $2`, orderID, userID).Scan(
		&o.ID, &o.ReferenceNumber, &o.Barcode, &o.CreatedAt,
		&o.EventID, &o.EventName, &o.EventStartsAt, &o.EventBannerURL, &o.EventVenueName, &o.EventCity, &o.BuyerName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "profile ticket", err)
		return
	}
	if o.Items, err = h.orderLines(ctx, orderID); err != nil {
		serverError(w, "profile ticket items", err)
		return
	}
	var c wallEntry
	err = h.db.QueryRow(ctx, `select content, created_at from wall_comment where user_id = $1 and event_id = $2
		order by created_at desc limit 1`, userID, o.EventID).Scan(&c.Content, &c.CreatedAt)
	switch {
	case err == nil:
		o.Comment = &c
	case !errors.Is(err, pgx.ErrNoRows):
		serverError(w, "profile ticket comment", err)
		return
	}
	httpx.JSON(w, http.StatusOK, o)
}
