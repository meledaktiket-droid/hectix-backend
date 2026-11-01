package organizer

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
)

var orgRoleLabels = map[string]string{"owner": "Owner", "admin": "Admin", "member": "Member"}

type inviteResult struct {
	Status string `json:"status"`
}

type inviteNeedsSignin struct {
	Status      string `json:"status"`
	Email       string `json:"email"`
	OrgName     string `json:"orgName"`
	RoleLabel   string `json:"roleLabel"`
	InviterName string `json:"inviterName"`
}

type inviteMismatch struct {
	Status        string `json:"status"`
	InvitedEmail  string `json:"invitedEmail"`
	SignedInEmail string `json:"signedInEmail"`
}

func (h *handlers) inspectInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := auth.LoadError(ctx); err != nil {
		serverError(w, "session", err)
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	invalid := inviteResult{Status: "invalid"}
	id, _ := b.str("id")
	if id == "" {
		httpx.JSON(w, http.StatusOK, invalid)
		return
	}
	var (
		orgID, email, status, inviterID string
		role                            *string
		expiresAt                       time.Time
	)
	err = h.db.QueryRow(ctx, `select organization_id, email, role, status, expires_at, inviter_id from invitation where id = $1`, id).
		Scan(&orgID, &email, &role, &status, &expiresAt, &inviterID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, http.StatusOK, invalid)
		return
	}
	if err != nil {
		serverError(w, "invite", err)
		return
	}
	switch {
	case status == "canceled" || status == "rejected":
		httpx.JSON(w, http.StatusOK, invalid)
		return
	case status == "accepted":
		httpx.JSON(w, http.StatusOK, inviteResult{Status: "accepted"})
		return
	case expiresAt.Before(time.Now()):
		httpx.JSON(w, http.StatusOK, inviteResult{Status: "expired"})
		return
	}
	var orgName string
	err = h.db.QueryRow(ctx, `select name from organization where id = $1`, orgID).Scan(&orgName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, http.StatusOK, invalid)
		return
	}
	if err != nil {
		serverError(w, "invite org", err)
		return
	}
	roleKey := "member"
	if role != nil {
		roleKey = *role
	}
	roleLabel, ok := orgRoleLabels[roleKey]
	if !ok {
		roleLabel = roleKey
	}

	u := auth.UserFrom(ctx)
	if u == nil {
		var name, inviterEmail string
		err := h.db.QueryRow(ctx, `select name, email from "user" where id = $1`, inviterID).Scan(&name, &inviterEmail)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			serverError(w, "invite inviter", err)
			return
		}
		inviter := name
		if inviter == "" {
			inviter = inviterEmail
		}
		if inviter == "" {
			inviter = "A teammate"
		}
		httpx.JSON(w, http.StatusOK, inviteNeedsSignin{"needs-signin", email, orgName, roleLabel, inviter})
		return
	}
	if strings.ToLower(u.Email) != strings.ToLower(email) {
		httpx.JSON(w, http.StatusOK, inviteMismatch{"mismatch", email, u.Email})
		return
	}
	memberRole, err := h.memberRole(ctx, orgID, u.ID)
	if err != nil {
		serverError(w, "invite member", err)
		return
	}
	if memberRole != "" {
		httpx.JSON(w, http.StatusOK, inviteResult{Status: "accepted"})
		return
	}
	httpx.JSON(w, http.StatusOK, inviteResult{Status: "needs-accept"})
}
