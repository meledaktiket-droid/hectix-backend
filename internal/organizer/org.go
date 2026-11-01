package organizer

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/config"
	"hectix/backend/internal/httpx"
)

type handlers struct {
	db         *pgxpool.Pool
	uploadDir  string
	apps       map[string]config.App
	logoClient *http.Client
	now        func() time.Time
}

func (h *handlers) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

type orgCtx struct {
	user  *auth.User
	orgID string
	role  string
}

func (c *orgCtx) canEdit() bool { return c.role == "owner" || c.role == "admin" }

func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("organizer %s: %v", where, err)
	httpx.Error(w, http.StatusInternalServerError, "Internal server error")
}

func forbidden(w http.ResponseWriter) { httpx.Error(w, http.StatusForbidden, "Forbidden") }
func notFound(w http.ResponseWriter)  { httpx.Error(w, http.StatusNotFound, "Not found") }
func badJSON(w http.ResponseWriter)   { httpx.Error(w, http.StatusBadRequest, "Invalid JSON body") }

func (h *handlers) org(w http.ResponseWriter, r *http.Request) (*orgCtx, bool) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		serverError(w, "session", err)
		return nil, false
	}
	u := auth.UserFrom(ctx)
	if u == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return nil, false
	}
	s := auth.SessionFrom(ctx)
	if s == nil || s.ActiveOrganizationID == nil || *s.ActiveOrganizationID == "" {
		httpx.Error(w, http.StatusBadRequest, "No active organization")
		return nil, false
	}
	orgID := *s.ActiveOrganizationID
	role, err := h.memberRole(ctx, orgID, u.ID)
	if err != nil {
		serverError(w, "member", err)
		return nil, false
	}
	if role == "" {
		forbidden(w)
		return nil, false
	}
	return &orgCtx{user: u, orgID: orgID, role: role}, true
}

func (h *handlers) memberRole(ctx context.Context, orgID, userID string) (string, error) {
	var role string
	err := h.db.QueryRow(ctx, `select role from member where organization_id = $1 and user_id = $2 limit 1`,
		orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (h *handlers) editor(w http.ResponseWriter, r *http.Request) (*orgCtx, bool) {
	c, ok := h.org(w, r)
	if !ok {
		return nil, false
	}
	if !c.canEdit() {
		forbidden(w)
		return nil, false
	}
	return c, true
}

const msgOrgNotVerified = "Organisasi belum terverifikasi"

func (h *handlers) requireActiveOrg(w http.ResponseWriter, r *http.Request, c *orgCtx) bool {
	var status string
	err := h.db.QueryRow(r.Context(), `select status from organization where id = $1`, c.orgID).Scan(&status)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, "org status", err)
		return false
	}
	if status != "active" {
		httpx.Error(w, http.StatusForbidden, msgOrgNotVerified)
		return false
	}
	return true
}

func (h *handlers) eventInOrg(ctx context.Context, orgID, eventID string) (bool, error) {
	var ok bool
	err := h.db.QueryRow(ctx, `select exists(select 1 from event where id = $1 and organization_id = $2 and deleted_at is null)`,
		eventID, orgID).Scan(&ok)
	return ok, err
}

func (h *handlers) requireEvent(w http.ResponseWriter, r *http.Request, c *orgCtx, eventID string) bool {
	ok, err := h.eventInOrg(r.Context(), c.orgID, eventID)
	if err != nil {
		serverError(w, "event lookup", err)
		return false
	}
	if !ok {
		notFound(w)
		return false
	}
	return true
}

func pgCode(err error) (code, constraint string) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code, pe.ConstraintName
	}
	return "", ""
}

func paidOrderCount(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, eventID string) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `select count(*) from "order" where event_id = $1 and status = 'paid'`, eventID).Scan(&n)
	return n, err
}
