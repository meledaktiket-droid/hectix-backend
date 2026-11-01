package web

import (
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/httpx"
)

var eventCategorySlugs = []string{"seni-budaya", "atraksi", "edukasi", "musik", "outdoor"}

type publishedEvent struct {
	eventListItem
	CommentCount     int      `json:"commentCount"`
	CommenterAvatars []string `json:"commenterAvatars"`
}

func (h *handlers) publishedEvents(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	term := strings.TrimSpace(q.Get("q"))
	var category *string
	if c := q.Get("category"); slices.Contains(eventCategorySlugs, c) {
		category = &c
	}
	rows, err := h.db.Query(ctx, `
		select e.id, e.name, e.venue_name, e.city, e.starts_at, e.banner_url, o.name,
			coalesce((select min(price) from ticket_type where event_id = e.id), 0)
		from event e join organization o on o.id = e.organization_id
		where e.status = 'published' and e.visibility = 'public' and e.deleted_at is null
			and coalesce(e.ends_at, e.starts_at) > now()
			and ($1 = '' or e.name ilike $2 or o.name ilike $2 or e.city ilike $2 or e.venue_name ilike $2)
			and ($3::text is null or e.categories @> jsonb_build_array($3::text))
		order by e.starts_at`, term, "%"+escapeLike(term)+"%", category)
	if err != nil {
		serverError(w, "published events", err)
		return
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (publishedEvent, error) {
		var e publishedEvent
		e.CommenterAvatars = []string{}
		return e, row.Scan(&e.ID, &e.Name, &e.VenueName, &e.City, &e.StartsAt, &e.BannerURL, &e.OrganizationName, &e.MinPrice)
	})
	if err != nil {
		serverError(w, "published events", err)
		return
	}
	if len(events) == 0 {
		httpx.JSON(w, http.StatusOK, []publishedEvent{})
		return
	}

	idx := make(map[string]int, len(events))
	ids := make([]string, len(events))
	for i, e := range events {
		idx[e.ID], ids[i] = i, e.ID
	}
	rows, err = h.db.Query(ctx, `select c.event_id, c.user_id, u.image from wall_comment c join "user" u on u.id = c.user_id
		where c.event_id = any($1) order by c.created_at desc`, ids)
	if err != nil {
		serverError(w, "published events comments", err)
		return
	}
	seen := map[[2]string]bool{}
	var eventID, userID string
	var image *string
	if _, err := pgx.ForEachRow(rows, []any{&eventID, &userID, &image}, func() error {
		e := &events[idx[eventID]]
		e.CommentCount++
		k := [2]string{eventID, userID}
		if !seen[k] {
			seen[k] = true
			if image != nil && *image != "" && len(e.CommenterAvatars) < 3 {
				e.CommenterAvatars = append(e.CommenterAvatars, *image)
			}
		}
		return nil
	}); err != nil {
		serverError(w, "published events comments", err)
		return
	}
	httpx.JSON(w, http.StatusOK, events)
}

type homeBanner struct {
	ID          string `json:"id"`
	Tag         string `json:"tag"`
	Description string `json:"description"`
	ImageURL    string `json:"imageUrl"`
}

func (h *handlers) activeBanners(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `select id, tag, description, image_url from banner
		where is_active and deleted_at is null order by display_order, created_at`)
	if err != nil {
		serverError(w, "banners", err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (homeBanner, error) {
		var b homeBanner
		return b, row.Scan(&b.ID, &b.Tag, &b.Description, &b.ImageURL)
	})
	if err != nil {
		serverError(w, "banners", err)
		return
	}
	httpx.JSON(w, http.StatusOK, nonNil(out))
}
