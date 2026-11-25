package scanner

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/httpx"
)

type handlers struct {
	db *pgxpool.Pool
}

type eventOption struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Status   string     `json:"status"`
	StartsAt httpx.Time `json:"startsAt"`
}

type sessionItem struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	StartsAt      httpx.Time `json:"startsAt"`
	EndsAt        httpx.Time `json:"endsAt"`
	AllowReentry  bool       `json:"allowReentry"`
	QuotaLimit    int        `json:"quotaLimit"`
	CurrentInside int        `json:"currentInside"`
	UniqueEntered int        `json:"uniqueEntered"`
}

func (h *handlers) listEvents(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	rows, err := h.db.Query(ctx, `
		select e.id, e.name, e.status, e.starts_at
		from event e
		where e.deleted_at is null
		  and (
		      exists (
		          select 1 from member m
		          where m.organization_id = e.organization_id
		            and m.user_id = $1
		            and m.role <> 'volunteer'
		      )
		      or exists (
		          select 1 from event_crew ec
		          where ec.event_id = e.id
		            and ec.status = 'accepted'
		            and (ec.user_id = $1 or ec.email = lower($2))
		      )
		      or ($3::bool and e.status = 'published')
		  )
		order by e.starts_at desc`, u.ID, u.Email, isAdminStaff(u.Role))
	if err != nil {
		log.Printf("scanner listEvents: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[eventOption])
	if err != nil {
		log.Printf("scanner listEvents scan: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	if events == nil {
		events = []eventOption{}
	}

	role := "volunteer"
	var nonVolRole string
	_ = h.db.QueryRow(ctx, `
		select role from member where user_id = $1 and role <> 'volunteer' limit 1`,
		u.ID).Scan(&nonVolRole)
	if nonVolRole != "" {
		role = nonVolRole
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"events": events,
		"role":   role,
	})
}

func (h *handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := requireUser(w, r)
	if !ok {
		return
	}
	eventID := strings.TrimSpace(r.URL.Query().Get("eventId"))
	if eventID == "" {
		httpx.Error(w, http.StatusBadRequest, "eventId wajib diisi")
		return
	}
	ctx := r.Context()
	if _, _, err := resolveEventRole(ctx, h.db, u, eventID); err != nil {
		if errors.Is(err, errForbidden) {
			httpx.Error(w, http.StatusForbidden, "Forbidden")
			return
		}
		log.Printf("scanner listSessions auth: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	rows, err := h.db.Query(ctx, `
		select id, name, starts_at, ends_at, allow_reentry, quota_limit, current_inside, unique_entered
		from event_session
		where event_id = $1
		order by starts_at asc`, eventID)
	if err != nil {
		log.Printf("scanner listSessions query: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	sessions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[sessionItem])
	if err != nil {
		log.Printf("scanner listSessions scan: %v", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}

	if sessions == nil {
		sessions = []sessionItem{}
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
	})
}
