package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type acceptResult struct {
	Status        string `json:"status"`
	Email         string `json:"email,omitempty"`
	RoleLabel     string `json:"roleLabel,omitempty"`
	InvitedEmail  string `json:"invitedEmail,omitempty"`
	SignedInEmail string `json:"signedInEmail,omitempty"`
}

func (h *handlers) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if auth.LoadError(ctx) != nil {
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	var body struct {
		Token any `json:"token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	token, _ := body.Token.(string)
	if token == "" {
		httpx.JSON(w, http.StatusOK, acceptResult{Status: "invalid"})
		return
	}

	var res acceptResult
	err := dbx.Tx(ctx, h.d.DB, func(tx pgx.Tx) error {
		var id, email, role, status string
		var expires time.Time
		err := tx.QueryRow(ctx, `select id, email, role, status, expires_at from admin_invitation
			where token = $1 for update`, token).Scan(&id, &email, &role, &status, &expires)
		if errors.Is(err, pgx.ErrNoRows) {
			res = acceptResult{Status: "invalid"}
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case status == "revoked":
			res = acceptResult{Status: "invalid"}
			return nil
		case status == "accepted":
			res = acceptResult{Status: "accepted"}
			return nil
		case expires.Before(time.Now()):
			res = acceptResult{Status: "expired"}
			return nil
		}
		u := auth.UserFrom(ctx)
		if u == nil {
			label := RoleLabels[role]
			if label == "" {
				label = role
			}
			res = acceptResult{Status: "needs-signin", Email: email, RoleLabel: label}
			return nil
		}
		if strings.ToLower(u.Email) != email {
			res = acceptResult{Status: "mismatch", InvitedEmail: email, SignedInEmail: u.Email}
			return nil
		}
		if role != "super_admin" {
			if _, err := tx.Exec(ctx, `select 1 from "user" where role = 'super_admin' order by id for update`); err != nil {
				return err
			}
			var lastSuper bool
			if err := tx.QueryRow(ctx, `select role = 'super_admin' and not exists(
				select 1 from "user" where role = 'super_admin' and id <> $1) from "user" where id = $1`, u.ID).Scan(&lastSuper); err != nil {
				return err
			}
			if lastSuper {
				role = "super_admin"
			}
		}
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `update "user" set role = $2, updated_at = $3 where id = $1`, u.ID, role, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update admin_invitation set status = 'accepted', accepted_at = $2 where id = $1`, id, now); err != nil {
			return err
		}
		res = acceptResult{Status: "accepted"}
		return nil
	})
	if err != nil {
		serverError(w, "accept invitation", err)
		return
	}
	httpx.JSON(w, http.StatusOK, res)
}
