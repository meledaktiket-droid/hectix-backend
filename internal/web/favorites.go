package web

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/settings"
)

func (h *handlers) config(w http.ResponseWriter, r *http.Request) {
	s, err := settings.Get(r.Context(), h.db)
	if err != nil {
		serverError(w, "config", err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

type favoriteRow struct {
	EventID   string     `json:"eventId"`
	Name      string     `json:"name"`
	BannerURL *string    `json:"bannerUrl"`
	VenueName *string    `json:"venueName"`
	City      *string    `json:"city"`
	StartsAt  httpx.Time `json:"startsAt"`
}

func (h *handlers) listFavorites(w http.ResponseWriter, r *http.Request) {
	limit, offset := page(r.URL.Query())
	rows, err := h.db.Query(r.Context(), `
		select e.id, e.name, e.banner_url, e.venue_name, e.city, e.starts_at
		from favorite f join event e on e.id = f.event_id
		where f.user_id = $1 and e.deleted_at is null and e.status = 'published'
		order by f.created_at desc, f.id
		limit $2 offset $3`, auth.UserFrom(r.Context()).ID, limit+1, offset)
	if err != nil {
		serverError(w, "favorites", err)
		return
	}
	favs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (favoriteRow, error) {
		var f favoriteRow
		return f, row.Scan(&f.EventID, &f.Name, &f.BannerURL, &f.VenueName, &f.City, &f.StartsAt)
	})
	if err != nil {
		serverError(w, "favorites", err)
		return
	}
	hasMore := len(favs) > limit
	if hasMore {
		favs = favs[:limit]
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"favorites": nonNil(favs), "nextOffset": nextOffset(hasMore, offset, limit)})
}

func (h *handlers) addFavorite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EventID string `json:"eventId"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid body")
		return
	}
	if body.EventID == "" {
		httpx.Error(w, http.StatusBadRequest, "Missing eventId")
		return
	}
	var ok bool
	err := h.db.QueryRow(r.Context(), `select true from event where id=$1 and status='published' and deleted_at is null`, body.EventID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "add favorite", err)
		return
	}
	if _, err := h.db.Exec(r.Context(), `insert into favorite(id,user_id,event_id,created_at) values($1,$2,$3,now())
		on conflict do nothing`, dbx.UUID(), auth.UserFrom(r.Context()).ID, body.EventID); err != nil {
		serverError(w, "add favorite", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handlers) favoriteIDs(w http.ResponseWriter, r *http.Request) {
	if err := auth.LoadError(r.Context()); err != nil {
		serverError(w, "favorite ids: session", err)
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		httpx.JSON(w, http.StatusOK, []string{})
		return
	}
	rows, err := h.db.Query(r.Context(), `select event_id from favorite where user_id=$1`, u.ID)
	if err != nil {
		serverError(w, "favorite ids", err)
		return
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		serverError(w, "favorite ids", err)
		return
	}
	httpx.JSON(w, http.StatusOK, nonNil(ids))
}

func (h *handlers) removeFavorite(w http.ResponseWriter, r *http.Request) {
	if _, err := h.db.Exec(r.Context(), `delete from favorite where user_id=$1 and event_id=$2`,
		auth.UserFrom(r.Context()).ID, chi.URLParam(r, "eventId")); err != nil {
		serverError(w, "remove favorite", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
