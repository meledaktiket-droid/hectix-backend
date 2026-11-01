package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

const maxCommentLen = 150

type commentAuthor struct {
	Name  string  `json:"name"`
	Image *string `json:"image"`
}

type comment struct {
	ID        string        `json:"id"`
	Content   string        `json:"content"`
	CreatedAt httpx.Time    `json:"createdAt"`
	Author    commentAuthor `json:"author"`
}

func (h *handlers) listComments(w http.ResponseWriter, r *http.Request) {
	if !h.eventVisible(w, r, chi.URLParam(r, "id")) {
		return
	}
	rows, err := h.db.Query(r.Context(), `
		select c.id, c.content, c.created_at, u.name, u.image
		from wall_comment c join "user" u on u.id = c.user_id
		where c.event_id = $1
		order by c.created_at desc
		limit 100`, chi.URLParam(r, "id"))
	if err != nil {
		serverError(w, "comments", err)
		return
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (comment, error) {
		var c comment
		return c, row.Scan(&c.ID, &c.Content, &c.CreatedAt, &c.Author.Name, &c.Author.Image)
	})
	if err != nil {
		serverError(w, "comments", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"comments": nonNil(out)})
}

func (h *handlers) addComment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := auth.UserFrom(ctx)
	eventID := chi.URLParam(r, "id")
	if !h.eventVisible(w, r, eventID) {
		return
	}
	var body struct {
		Content *string `json:"content"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid content")
		return
	}
	content := ""
	if body.Content != nil {
		content = strings.TrimSpace(*body.Content)
	}
	if n := jsLen(content); n < 1 || n > maxCommentLen {
		httpx.Error(w, http.StatusBadRequest, "Invalid content")
		return
	}

	var paid bool
	if err := h.db.QueryRow(ctx, `select exists(select 1 from "order" where user_id=$1 and event_id=$2 and status='paid')`,
		u.ID, eventID).Scan(&paid); err != nil {
		serverError(w, "comment paid check", err)
		return
	}
	if !paid {
		httpx.Error(w, http.StatusForbidden, "Forbidden")
		return
	}

	c := comment{
		ID:        dbx.UUID(),
		Content:   content,
		CreatedAt: httpx.Time{Time: time.Now().UTC().Truncate(time.Millisecond)},
		Author:    commentAuthor{Name: u.Name, Image: u.Image},
	}
	if _, err := h.db.Exec(ctx, `insert into wall_comment(id,event_id,user_id,content,created_at) values($1,$2,$3,$4,$5)`,
		c.ID, eventID, u.ID, c.Content, c.CreatedAt.Time); err != nil {
		serverError(w, "add comment", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}
