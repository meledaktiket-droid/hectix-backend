package organizer

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

func (h *handlers) listPerformers(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	ps, err := list(r.Context(), h.db, scanPerformer, `select `+performerCols+` from performer
		where event_id = $1 and deleted_at is null order by display_order`, id)
	if err != nil {
		serverError(w, "performers", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"performers": ps})
}

func (h *handlers) createPerformer(w http.ResponseWriter, r *http.Request) {
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
	name = strings.TrimSpace(name)
	if name == "" {
		httpx.Error(w, http.StatusBadRequest, "Name is required")
		return
	}
	var image *string
	if s, ok := b.str("imageUrl"); ok {
		if s = strings.TrimSpace(s); s != "" {
			image = &s
		}
	}
	now := time.Now().UTC()
	p, err := scanPerformer(h.db.QueryRow(r.Context(), `insert into performer(id, event_id, name, image_url, display_order,
		created_at, updated_at) values($1, $2, $3, $4, (select count(*) from performer where event_id = $2), $5, $5)
		returning `+performerCols, dbx.UUID(), id, name, image, now))
	if err != nil {
		serverError(w, "create performer", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"performer": p})
}

func (h *handlers) deletePerformer(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	now := time.Now().UTC()
	if _, err := h.db.Exec(r.Context(), `update performer set deleted_at = $3, updated_at = $3 where id = $1 and event_id = $2`,
		chi.URLParam(r, "performerId"), id, now); err != nil {
		serverError(w, "delete performer", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
