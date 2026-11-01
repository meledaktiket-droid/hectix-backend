package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
)

const inviteTTL = 7 * 24 * time.Hour

type adminInvitation struct {
	ID         string      `json:"id"`
	Email      string      `json:"email"`
	Role       string      `json:"role"`
	Token      string      `json:"token"`
	Status     string      `json:"status"`
	InvitedBy  string      `json:"invitedBy"`
	ExpiresAt  httpx.Time  `json:"expiresAt"`
	AcceptedAt *httpx.Time `json:"acceptedAt"`
	CreatedAt  httpx.Time  `json:"createdAt"`
}

const invitationCols = `id, email, role, token, status, invited_by, expires_at, accepted_at, created_at`

func inviteToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *handlers) sendInvite(ctx context.Context, to, role, token string) error {
	acceptURL := httpx.AppFrom(ctx).BaseURL + "/invite/accept?token=" + token
	return h.send(ctx, to, mail.AdminInvite(to, RoleLabels[role], acceptURL))
}

func (h *handlers) listInvitations(w http.ResponseWriter, r *http.Request) {
	rows, err := list[adminInvitation](r.Context(), h.d.DB, `select `+invitationCols+` from admin_invitation
		where status = 'pending' order by created_at desc`)
	if err != nil {
		serverError(w, "invitations", err)
		return
	}
	httpx.JSON(w, http.StatusOK, rows)
}

func (h *handlers) createInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Email any `json:"email"`
		Role  any `json:"role"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	email, _ := body.Email.(string)
	email = strings.ToLower(jsTrim(email))
	role, _ := body.Role.(string)
	if !emailRe.MatchString(email) || !IsAdminRole(role) {
		httpx.Error(w, http.StatusBadRequest, "A valid email and role are required.")
		return
	}

	token := inviteToken()
	var conflict string
	err := dbx.Tx(ctx, h.d.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('admin_invitation:' || $1))`, email); err != nil {
			return err
		}
		var member, pending bool
		if err := tx.QueryRow(ctx, `select
			exists(select 1 from "user" where email = $1 and role = any($2)),
			exists(select 1 from admin_invitation where email = $1 and status = 'pending')`, email, AdminRoles).Scan(&member, &pending); err != nil {
			return err
		}
		switch {
		case member:
			conflict = "member"
			return nil
		case pending:
			conflict = "pending"
			return nil
		}
		now := time.Now().UTC()
		_, err := tx.Exec(ctx, `insert into admin_invitation(id, email, role, token, status, invited_by, expires_at, created_at)
			values($1, $2, $3, $4, 'pending', $5, $6, $7)`, dbx.UUID(), email, role, token, auth.UserFrom(ctx).ID, now.Add(inviteTTL), now)
		return err
	})
	if err != nil {
		serverError(w, "create invitation", err)
		return
	}
	switch conflict {
	case "member":
		httpx.Error(w, http.StatusConflict, "This person is already a team member.")
		return
	case "pending":
		httpx.JSON(w, http.StatusConflict, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{"An invite is already pending for this email.", "PENDING"})
		return
	}
	if err := h.sendInvite(ctx, email, role, token); err != nil {
		serverError(w, "send admin invite", err)
		return
	}
	ok(w)
}

func (h *handlers) resendInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := inviteToken()
	var email, role string
	err := h.d.DB.QueryRow(ctx, `update admin_invitation set token = $2, expires_at = $3
		where id = $1 and status = 'pending' returning email, role`,
		chi.URLParam(r, "id"), token, time.Now().UTC().Add(inviteTTL)).Scan(&email, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "No pending invite to resend.")
		return
	}
	if err != nil {
		serverError(w, "resend invitation", err)
		return
	}
	if err := h.sendInvite(ctx, email, role, token); err != nil {
		serverError(w, "send admin invite", err)
		return
	}
	ok(w)
}

func (h *handlers) revokeInvitation(w http.ResponseWriter, r *http.Request) {
	tag, err := h.d.DB.Exec(r.Context(), `update admin_invitation set status = 'revoked'
		where id = $1 and status = 'pending'`, chi.URLParam(r, "id"))
	if err != nil {
		serverError(w, "revoke invitation", err)
		return
	}
	if tag.RowsAffected() == 0 {
		httpx.Error(w, http.StatusNotFound, "No pending invite to revoke.")
		return
	}
	ok(w)
}

type teamMember struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	Role      string     `json:"role"`
	Image     *string    `json:"image"`
	CreatedAt httpx.Time `json:"createdAt"`
}

func (h *handlers) listMembers(w http.ResponseWriter, r *http.Request) {
	rows, err := list[teamMember](r.Context(), h.d.DB, `select id, name, email, role, image, created_at
		from "user" where role = any($1) order by created_at, id`, AdminRoles)
	if err != nil {
		serverError(w, "members", err)
		return
	}
	httpx.JSON(w, http.StatusOK, rows)
}

var errLastSuperAdmin = errors.New("last super admin")

func (h *handlers) setStaffRole(ctx context.Context, id, newRole string) (found bool, err error) {
	err = dbx.Tx(ctx, h.d.DB, func(tx pgx.Tx) error {
		supers, err := list[struct{ ID string }](ctx, tx, `select id from "user" where role = 'super_admin' order by id for update`)
		if err != nil {
			return err
		}
		var role string
		err = tx.QueryRow(ctx, `select role from "user" where id = $1 for update`, id).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !IsAdminRole(role)) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		others := 0
		for _, s := range supers {
			if s.ID != id {
				others++
			}
		}
		if role == "super_admin" && newRole != "super_admin" && others == 0 {
			return errLastSuperAdmin
		}
		_, err = tx.Exec(ctx, `update "user" set role = $2, updated_at = $3 where id = $1`, id, newRole, time.Now().UTC())
		return err
	})
	return found, err
}

func (h *handlers) patchMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role any `json:"role"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	role, _ := body.Role.(string)
	if !IsAdminRole(role) {
		httpx.Error(w, http.StatusBadRequest, "Invalid role")
		return
	}
	h.memberRoleChange(w, r, role, "Cannot demote the last Super Admin.")
}

func (h *handlers) deleteMember(w http.ResponseWriter, r *http.Request) {
	h.memberRoleChange(w, r, "user", "Cannot remove the last Super Admin.")
}

func (h *handlers) memberRoleChange(w http.ResponseWriter, r *http.Request, role, lastMsg string) {
	found, err := h.setStaffRole(r.Context(), chi.URLParam(r, "id"), role)
	switch {
	case errors.Is(err, errLastSuperAdmin):
		httpx.Error(w, http.StatusConflict, lastMsg)
	case err != nil:
		serverError(w, "team member", err)
	case !found:
		httpx.Error(w, http.StatusNotFound, "Not a team member")
	default:
		ok(w)
	}
}
