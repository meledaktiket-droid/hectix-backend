package organizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

var orgStatuses = []string{"pending", "review", "active", "rejected", "suspended"}

func (h *handlers) orgStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		serverError(w, "session", err)
		return
	}
	u, s := auth.UserFrom(ctx), auth.SessionFrom(ctx)
	if u == nil || s == nil || s.ActiveOrganizationID == nil || *s.ActiveOrganizationID == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": nil})
		return
	}
	orgID := *s.ActiveOrganizationID
	role, err := h.memberRole(ctx, orgID, u.ID)
	if err != nil {
		serverError(w, "org status", err)
		return
	}
	if role == "" {
		httpx.JSON(w, http.StatusOK, map[string]any{"status": nil})
		return
	}
	status, note, allow := "pending", (*string)(nil), false
	err = h.db.QueryRow(ctx, `select status, status_note, allow_resubmit from organization where id = $1`, orgID).
		Scan(&status, &note, &allow)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, "org status", err)
		return
	}
	if !slices.Contains(orgStatuses, status) {
		status = "pending"
	}
	httpx.JSON(w, http.StatusOK, struct {
		Status        string  `json:"status"`
		StatusNote    *string `json:"statusNote"`
		AllowResubmit bool    `json:"allowResubmit"`
	}{status, note, allow})
}

func recordOrgStatusChange(ctx context.Context, db dbx.DB, orgID string, from *string, to, actorID, actorType string) {
	if from != nil && *from == to {
		return
	}
	if err := insertOrgStatusHistory(ctx, db, orgID, from, to, actorID, actorType); err != nil {
		log.Printf("[status-history] failed to record transition: %v", err)
	}
}

func insertOrgStatusHistory(ctx context.Context, db dbx.DB, orgID string, from *string, to, actorID, actorType string) error {
	_, err := db.Exec(ctx, `insert into organization_status_history(id, organization_id, from_status, to_status, note,
		actor_id, actor_type, created_at) values($1, $2, $3, $4, null, $5, $6, $7)`,
		dbx.UUID(), orgID, from, to, actorID, actorType, time.Now().UTC())
	return err
}

const msgCannotResubmit = "Verification cannot be submitted in the current organization status"

func (h *handlers) verifyOrg(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&raw); err != nil {
		badJSON(w)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var data any
	if err := dec.Decode(&data); err != nil {
		badJSON(w)
		return
	}
	vd, err := marshal(data)
	if err != nil {
		badJSON(w)
		return
	}
	ctx := r.Context()
	var current *string
	err = dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var status string
		var allow bool
		err := tx.QueryRow(ctx, `select status, allow_resubmit from organization where id = $1 for update`, c.orgID).Scan(&status, &allow)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		current = &status
		if status != "pending" && status != "review" && !allow {
			return errCannotResubmit
		}
		_, err = tx.Exec(ctx, `update organization set verification_data = $2, status = 'review', allow_resubmit = false
			where id = $1`, c.orgID, string(vd))
		return err
	})
	if errors.Is(err, errCannotResubmit) {
		httpx.Error(w, http.StatusConflict, msgCannotResubmit)
		return
	}
	if err != nil {
		serverError(w, "verify", err)
		return
	}
	if current != nil {
		recordOrgStatusChange(ctx, h.db, c.orgID, current, "review", c.user.ID, "organizer")
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var errCannotResubmit = errors.New(msgCannotResubmit)

func (h *handlers) logOrgCreation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		serverError(w, "session", err)
		return
	}
	u := auth.UserFrom(ctx)
	if u == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	orgID, _ := b.str("organizationId")
	if orgID == "" {
		httpx.Error(w, http.StatusBadRequest, "Missing organizationId")
		return
	}
	role, err := h.memberRole(ctx, orgID, u.ID)
	if err != nil {
		serverError(w, "log creation", err)
		return
	}
	if role != "owner" {
		forbidden(w)
		return
	}
	err = dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, "org-status-history:"+orgID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from organization_status_history where organization_id = $1)`, orgID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		sp, err := tx.Begin(ctx)
		if err != nil {
			return err
		}
		if err := insertOrgStatusHistory(ctx, sp, orgID, nil, "pending", u.ID, "organizer"); err != nil {
			log.Printf("[status-history] failed to record transition: %v", err)
			return sp.Rollback(ctx)
		}
		return sp.Commit(ctx)
	})
	if err != nil {
		serverError(w, "log creation", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
