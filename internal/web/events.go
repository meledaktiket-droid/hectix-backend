package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
)

type eventListItem struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	VenueName        *string    `json:"venueName"`
	City             *string    `json:"city"`
	StartsAt         httpx.Time `json:"startsAt"`
	BannerURL        *string    `json:"bannerUrl"`
	OrganizationName string     `json:"organizationName"`
	MinPrice         int        `json:"minPrice"`
}

func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `
		select e.id, e.name, e.venue_name, e.city, e.starts_at, e.banner_url, o.name,
			coalesce((select min(price) from ticket_type where event_id = e.id), 0)
		from event e join organization o on o.id = e.organization_id
		where e.status = 'published' and e.visibility = 'public' and e.deleted_at is null
		order by e.starts_at`)
	if err != nil {
		serverError(w, "events", err)
		return
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (eventListItem, error) {
		var e eventListItem
		return e, row.Scan(&e.ID, &e.Name, &e.VenueName, &e.City, &e.StartsAt, &e.BannerURL, &e.OrganizationName, &e.MinPrice)
	})
	if err != nil {
		serverError(w, "events", err)
		return
	}
	httpx.JSON(w, http.StatusOK, nonNil(events))
}

type eventTicket struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Price     int             `json:"price"`
	Quantity  int             `json:"quantity"`
	Sold      int             `json:"sold"`
	Benefits  json.RawMessage `json:"benefits"`
	Remaining int             `json:"remaining"`
}

type eventPerformer struct {
	Name     string  `json:"name"`
	ImageURL *string `json:"imageUrl"`
}

func (h *handlers) getEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		serverError(w, "event: session", err)
		return
	}
	id := chi.URLParam(r, "id")
	var ev struct {
		ID               string           `json:"id"`
		Name             string           `json:"name"`
		Description      *string          `json:"description"`
		BannerURL        *string          `json:"bannerUrl"`
		VenueName        *string          `json:"venueName"`
		Address          *string          `json:"address"`
		City             *string          `json:"city"`
		StartsAt         httpx.Time       `json:"startsAt"`
		EndsAt           *httpx.Time      `json:"endsAt"`
		OrganizationName string           `json:"organizationName"`
		OrganizationLogo *string          `json:"organizationLogo"`
		Tickets          []eventTicket    `json:"tickets"`
		Performers       []eventPerformer `json:"performers"`
		CommentCount     int              `json:"commentCount"`
		CommenterAvatars []string         `json:"commenterAvatars"`
		CanComment       bool             `json:"canComment"`
	}
	err := h.db.QueryRow(ctx, `
		select e.id, e.name, e.description, e.banner_url, e.venue_name, e.address, e.city, e.starts_at, e.ends_at, o.name, o.logo
		from event e join organization o on o.id = e.organization_id
		where e.id = $1 and e.status = 'published' and e.deleted_at is null`, id).Scan(
		&ev.ID, &ev.Name, &ev.Description, &ev.BannerURL, &ev.VenueName, &ev.Address, &ev.City, &ev.StartsAt, &ev.EndsAt,
		&ev.OrganizationName, &ev.OrganizationLogo)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "event", err)
		return
	}

	rows, err := h.db.Query(ctx, `select id, name, price, quantity, sold, coalesce(benefits, 'null'::jsonb)
		from ticket_type where event_id = $1 order by display_order`, id)
	if err != nil {
		serverError(w, "event tickets", err)
		return
	}
	ev.Tickets, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (eventTicket, error) {
		var t eventTicket
		var benefits []byte
		err := row.Scan(&t.ID, &t.Name, &t.Price, &t.Quantity, &t.Sold, &benefits)
		t.Benefits = benefits
		t.Remaining = max(0, t.Quantity-t.Sold)
		return t, err
	})
	if err != nil {
		serverError(w, "event tickets", err)
		return
	}

	rows, err = h.db.Query(ctx, `select name, image_url from performer where event_id = $1 and deleted_at is null order by display_order`, id)
	if err != nil {
		serverError(w, "event performers", err)
		return
	}
	ev.Performers, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (eventPerformer, error) {
		var p eventPerformer
		return p, row.Scan(&p.Name, &p.ImageURL)
	})
	if err != nil {
		serverError(w, "event performers", err)
		return
	}

	if err := h.db.QueryRow(ctx, `select count(*) from wall_comment where event_id = $1`, id).Scan(&ev.CommentCount); err != nil {
		serverError(w, "event comment count", err)
		return
	}

	rows, err = h.db.Query(ctx, `select c.user_id, u.image from wall_comment c join "user" u on u.id = c.user_id
		where c.event_id = $1 order by c.created_at desc limit 20`, id)
	if err != nil {
		serverError(w, "event commenters", err)
		return
	}
	ev.CommenterAvatars = []string{}
	seen := map[string]bool{}
	var userID string
	var image *string
	if _, err := pgx.ForEachRow(rows, []any{&userID, &image}, func() error {
		if seen[userID] || len(ev.CommenterAvatars) == 3 {
			return nil
		}
		seen[userID] = true
		if image != nil && *image != "" {
			ev.CommenterAvatars = append(ev.CommenterAvatars, *image)
		}
		return nil
	}); err != nil {
		serverError(w, "event commenters", err)
		return
	}

	if u := auth.UserFrom(ctx); u != nil {
		if err := h.db.QueryRow(ctx, `select exists(select 1 from "order" where user_id=$1 and event_id=$2 and status='paid')`,
			u.ID, id).Scan(&ev.CanComment); err != nil {
			serverError(w, "event can comment", err)
			return
		}
	}
	ev.Tickets, ev.Performers = nonNil(ev.Tickets), nonNil(ev.Performers)
	httpx.JSON(w, http.StatusOK, ev)
}

var escapeLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace

type eventSuggestion struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	City             *string `json:"city"`
	OrganizationName string  `json:"organizationName"`
	BannerURL        *string `json:"bannerUrl"`
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if jsLen(term) < 2 {
		httpx.JSON(w, http.StatusOK, []eventSuggestion{})
		return
	}
	rows, err := h.db.Query(r.Context(), `
		select e.id, e.name, e.city, o.name, e.banner_url
		from event e join organization o on o.id = e.organization_id
		where e.status = 'published' and e.visibility = 'public' and e.deleted_at is null
			and coalesce(e.ends_at, e.starts_at) > now()
			and (e.name ilike $1 or o.name ilike $1 or e.city ilike $1 or e.venue_name ilike $1)
		order by e.starts_at
		limit 6`, "%"+escapeLike(term)+"%")
	if err != nil {
		serverError(w, "search", err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (eventSuggestion, error) {
		var s eventSuggestion
		return s, row.Scan(&s.ID, &s.Name, &s.City, &s.OrganizationName, &s.BannerURL)
	})
	if err != nil {
		serverError(w, "search", err)
		return
	}
	httpx.JSON(w, http.StatusOK, nonNil(out))
}

func (h *handlers) eventVisible(w http.ResponseWriter, r *http.Request, id string) bool {
	var ok bool
	err := h.db.QueryRow(r.Context(), `select true from event where id=$1 and status='published' and deleted_at is null`, id).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return false
	}
	if err != nil {
		serverError(w, "event visible", err)
		return false
	}
	return true
}
