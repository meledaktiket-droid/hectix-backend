package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

var errForbidden = errors.New("forbidden")

type eventCtx struct {
	user  *auth.User
	orgID string
	role  string
}

func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("scanner %s: %v", where, err)
	httpx.Error(w, http.StatusInternalServerError, "Internal server error")
}

func badJSON(w http.ResponseWriter) {
	httpx.Error(w, http.StatusBadRequest, "Invalid JSON body")
}

func forbidden(w http.ResponseWriter) {
	httpx.Error(w, http.StatusForbidden, "Forbidden")
}

func notFound(w http.ResponseWriter) {
	httpx.Error(w, http.StatusNotFound, "Not found")
}

func requireUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
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
	return u, true
}

func isAdminStaff(role string) bool {
	return role == "super_admin" || role == "admin" || role == "staff"
}

// resolveEventRole authorizes access against the target event's organization or accepted crew row,
// independent of session.active_organization_id.
func resolveEventRole(ctx context.Context, db dbx.DB, u *auth.User, eventID string) (orgID, role string, err error) {
	var evStatus string
	err = db.QueryRow(ctx, `
		select organization_id, status from event where id = $1 and deleted_at is null`,
		eventID).Scan(&orgID, &evStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errForbidden
	}
	if err != nil {
		return "", "", err
	}

	if isAdminStaff(u.Role) && evStatus == "published" {
		return orgID, "admin", nil
	}

	var memberRole string
	err = db.QueryRow(ctx, `
		select role from member where organization_id = $1 and user_id = $2 limit 1`,
		orgID, u.ID).Scan(&memberRole)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", "", err
	}
	if memberRole != "" && memberRole != "volunteer" {
		return orgID, memberRole, nil
	}

	var isCrew bool
	err = db.QueryRow(ctx, `
		select exists(
			select 1 from event_crew ec
			where ec.event_id = $1
			  and ec.status = 'accepted'
			  and (ec.user_id = $2 or ec.email = lower($3))
		)`, eventID, u.ID, u.Email).Scan(&isCrew)
	if err != nil {
		return "", "", err
	}
	if isCrew {
		return orgID, "volunteer", nil
	}
	return "", "", errForbidden
}

func (h *handlers) eventAccess(w http.ResponseWriter, r *http.Request, eventID string) (*eventCtx, bool) {
	u, ok := requireUser(w, r)
	if !ok {
		return nil, false
	}
	orgID, role, err := resolveEventRole(r.Context(), h.db, u, eventID)
	if errors.Is(err, errForbidden) {
		forbidden(w)
		return nil, false
	}
	if err != nil {
		serverError(w, "event access", err)
		return nil, false
	}
	return &eventCtx{user: u, orgID: orgID, role: role}, true
}

func payloadHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// checkScanIdempotency replays or rejects duplicate scan_ledger requests; returns true if response was already written.
func (h *handlers) checkScanIdempotency(ctx context.Context, w http.ResponseWriter, reqID, pHash string) bool {
	var existingHash string
	var existingPayload []byte
	err := h.db.QueryRow(ctx, `
		select payload_hash, response_payload from scan_ledger where request_id = $1`, reqID).
		Scan(&existingHash, &existingPayload)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		serverError(w, "scan_ledger check", err)
		return true
	}
	if existingHash != pHash {
		httpx.JSON(w, http.StatusConflict, map[string]any{
			"result": "CONFLICT",
			"error":  "Request ID sudah pernah digunakan dengan parameter berbeda",
		})
		return true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(existingPayload)
	return true
}
