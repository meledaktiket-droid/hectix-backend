package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

type eventVolunteerItem struct {
	ID             string      `json:"id"`
	EventID        string      `json:"eventId"`
	Email          string      `json:"email"`
	UserID         *string     `json:"userId"`
	UserName       *string     `json:"userName"`
	Role           string      `json:"role"`
	Status         string      `json:"status"`
	InvitedByName  string      `json:"invitedByName"`
	InvitedByEmail string      `json:"invitedByEmail"`
	CreatedAt      httpx.Time  `json:"createdAt"`
	AcceptedAt     *httpx.Time `json:"acceptedAt"`
}

type volunteerHistoryItem struct {
	ID             string     `json:"id"`
	FromStatus     *string    `json:"fromStatus"`
	ToStatus       string     `json:"toStatus"`
	VolunteerEmail string     `json:"volunteerEmail"`
	VolunteerName  *string    `json:"volunteerName"`
	Note           *string    `json:"note"`
	ActorType      string     `json:"actorType"`
	ActorName      *string    `json:"actorName"`
	ActorEmail     *string    `json:"actorEmail"`
	CreatedAt      httpx.Time `json:"createdAt"`
}

func randomCrewToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (h *handlers) listEventVolunteers(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")

	var exists bool
	if err := db.QueryRow(ctx, `select exists(select 1 from event where id = $1 and deleted_at is null)`, eventID).Scan(&exists); err != nil {
		serverError(w, "list volunteers event", err)
		return
	}
	if !exists {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}

	items, err := list[eventVolunteerItem](ctx, db, `
		select ec.id, ec.event_id, ec.email, ec.user_id, u.name,
			ec.role, ec.status, coalesce(inv.name, ''), coalesce(inv.email, ''),
			ec.created_at, ec.accepted_at
		from event_crew ec
		left join "user" u on u.id = ec.user_id
		left join "user" inv on inv.id = ec.invited_by
		where ec.event_id = $1 and ec.status <> 'revoked'
		order by ec.created_at desc, ec.id desc`, eventID)
	if err != nil {
		serverError(w, "list volunteers", err)
		return
	}
	if items == nil {
		items = []eventVolunteerItem{}
	}

	history, err := list[volunteerHistoryItem](ctx, db, `
		select
			eh.id as id,
			eh.from_status as from_status,
			coalesce(eh.to_status, 'pending') as to_status,
			coalesce(eh.metadata->>'email', ec.email, '') as volunteer_email,
			vu.name as volunteer_name,
			eh.note as note,
			eh.actor_type as actor_type,
			au.name as actor_name,
			au.email as actor_email,
			eh.created_at
		from event_activity_history eh
		left join event_crew ec on ec.id = eh.target_id
		left join "user" vu on (vu.id = ec.user_id or lower(vu.email) = lower(coalesce(eh.metadata->>'email', ec.email)))
		left join "user" au on au.id = eh.actor_id
		where eh.event_id = $1 and eh.category = 'volunteer'
		order by eh.created_at desc
		limit 50`, eventID)
	if err != nil {
		log.Printf("admin: list volunteer history: %v", err)
		history = []volunteerHistoryItem{}
	}
	if history == nil {
		history = []volunteerHistoryItem{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"volunteers": items,
		"history":    history,
	})
}

func (h *handlers) inviteEventVolunteer(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")
	actor := auth.UserFrom(ctx)
	if actor == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	email := strings.ToLower(jsTrim(body.Email))
	if email == "" || !emailRe.MatchString(email) {
		httpx.Error(w, http.StatusBadRequest, "Alamat email tidak valid")
		return
	}

	var orgID, eventName, orgName string
	err := db.QueryRow(ctx, `
		select e.organization_id, e.name, o.name
		from event e
		join organization o on o.id = e.organization_id
		where e.id = $1 and e.deleted_at is null`, eventID).Scan(&orgID, &eventName, &orgName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Event tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, "invite volunteer event", err)
		return
	}

	now := time.Now().UTC()
	expires := now.Add(7 * 24 * time.Hour)
	token := randomCrewToken()
	var crewID, inviteID string

	err = dbx.Tx(ctx, db, func(tx pgx.Tx) error {
		var existingUserID *string
		var uid string
		if err := tx.QueryRow(ctx, `select id from "user" where lower(email) = $1 limit 1`, email).Scan(&uid); err == nil {
			existingUserID = &uid
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		status := "pending"
		var existingRole string
		if err := tx.QueryRow(ctx, `
			select id, role from invitation
			where organization_id = $1 and lower(email) = $2 and status = 'pending'
			limit 1`, orgID, email).Scan(&inviteID, &existingRole); errors.Is(err, pgx.ErrNoRows) {
			inviteID = dbx.ID()
			if _, err := tx.Exec(ctx, `
				insert into invitation(id, organization_id, email, role, status, expires_at, created_at, inviter_id)
				values($1, $2, $3, 'volunteer', 'pending', $4, $5, $6)`,
				inviteID, orgID, email, expires, now, actor.ID); err != nil {
				return err
			}
		} else if err == nil {
			if existingRole != "volunteer" {
				return fmt.Errorf("CONFLICT:Email %s sudah memiliki undangan pending dengan peran %s di organisasi ini", email, existingRole)
			}
			if _, err := tx.Exec(ctx, `update invitation set expires_at = $2, inviter_id = $3 where id = $1`,
				inviteID, expires, actor.ID); err != nil {
				return err
			}
		} else {
			return err
		}

		var prevStatus *string
		var ps string
		if err := tx.QueryRow(ctx, `select status from event_crew where event_id = $1 and lower(email) = $2`, eventID, email).Scan(&ps); err == nil {
			prevStatus = &ps
		}

		err := tx.QueryRow(ctx, `
			insert into event_crew(id, event_id, email, user_id, role, token, status, invited_by, expires_at, accepted_at, revoked_at, created_at)
			values($1, $2, $3, $4, 'verification_crew', $5, 'pending', $6, $7, null, null, $8)
			on conflict (event_id, email) do update set
				user_id = coalesce(excluded.user_id, event_crew.user_id),
				token = excluded.token,
				status = 'pending',
				invited_by = excluded.invited_by,
				expires_at = excluded.expires_at,
				accepted_at = null,
				revoked_at = null
			returning id`,
			dbx.UUID(), eventID, email, existingUserID, token, actor.ID, expires, now).Scan(&crewID)
		if err != nil {
			return err
		}

		note := fmt.Sprintf("Invite volunteer scanner: %s", email)
		if prevStatus != nil && (*prevStatus == "revoked" || *prevStatus == "rejected") {
			note = fmt.Sprintf("Re-invite volunteer scanner: %s", email)
		}
		metaBytes, _ := json.Marshal(map[string]any{
			"email": email,
		})
		_, err = tx.Exec(ctx, `
			insert into event_activity_history(
				id, event_id, category, action, target_id, from_status, to_status, note, metadata, actor_id, actor_type, created_at
			) values($1, $2, 'volunteer', 'invited', $3, $4, $5, $6, $7::jsonb, $8, 'admin', $9)`,
			dbx.UUID(), eventID, crewID, prevStatus, status, note, string(metaBytes), actor.ID, now)
		return err
	})
	if err != nil {
		if strings.HasPrefix(err.Error(), "CONFLICT:") {
			httpx.Error(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "CONFLICT:"))
			return
		}
		serverError(w, "invite volunteer", err)
		return
	}

	if inviteID != "" {
		inviteBaseURL := "http://localhost:3001"
		if app, ok := h.d.Cfg.AppByName("scanner"); ok && app.BaseURL != "" {
			inviteBaseURL = app.BaseURL
		} else if app, ok := h.d.Cfg.AppByName("organizer"); ok && app.BaseURL != "" {
			inviteBaseURL = app.BaseURL
		}
		acceptURL := inviteBaseURL + "/invite/accept?id=" + inviteID
		inviterName := actor.Name
		if inviterName == "" {
			inviterName = actor.Email
		}
		if err := h.send(ctx, email, mail.OrgInvite(email, inviterName, orgName+" ("+eventName+")", "Volunteer", acceptURL)); err != nil {
			log.Printf("admin: invite volunteer mail (non-fatal): %v", err)
		}
	}

	item, err := one[eventVolunteerItem](ctx, db, `
		select ec.id, ec.event_id, ec.email, ec.user_id, u.name,
			ec.role, ec.status, coalesce(inv.name, ''), coalesce(inv.email, ''),
			ec.created_at, ec.accepted_at
		from event_crew ec
		left join "user" u on u.id = ec.user_id
		left join "user" inv on inv.id = ec.invited_by
		where ec.id = $1`, crewID)
	if err != nil || item == nil {
		serverError(w, "load volunteer", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"volunteer": item})
}

func (h *handlers) removeEventVolunteer(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	eventID := chi.URLParam(r, "id")
	volunteerID := chi.URLParam(r, "volunteerId")
	actor := auth.UserFrom(ctx)
	if actor == nil {
		httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
		return
	}

	var orgID string
	err := db.QueryRow(ctx, `select organization_id from event where id = $1`, eventID).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "remove volunteer event", err)
		return
	}

	now := time.Now().UTC()
	err = dbx.Tx(ctx, db, func(tx pgx.Tx) error {
		var prevStatus, email string
		var userID *string
		err := tx.QueryRow(ctx, `
			select status, email, user_id
			from event_crew
			where id = $1 and event_id = $2 and status <> 'revoked'
			for update`, volunteerID, eventID).Scan(&prevStatus, &email, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return pgx.ErrNoRows
		}
		if err != nil {
			return err
		}

		if _, err := tx.Exec(ctx, `
			update event_crew
			set status = 'revoked', revoked_at = $3
			where id = $1 and event_id = $2`,
			volunteerID, eventID, now); err != nil {
			return err
		}

		metaBytes, _ := json.Marshal(map[string]any{
			"email": email,
		})
		note := fmt.Sprintf("Revoke akses scanner: %s", email)
		if _, err := tx.Exec(ctx, `
			insert into event_activity_history(
				id, event_id, category, action, target_id, from_status, to_status, note, metadata, actor_id, actor_type, created_at
			) values($1, $2, 'volunteer', 'revoked', $3, $4, 'revoked', $5, $6::jsonb, $7, 'admin', $8)`,
			dbx.UUID(), eventID, volunteerID, prevStatus, note, string(metaBytes), actor.ID, now); err != nil {
			return err
		}

		var otherPendingCrew int
		if err := tx.QueryRow(ctx, `
			select count(*)
			from event_crew ec
			join event e on e.id = ec.event_id
			where e.organization_id = $1
			  and lower(ec.email) = lower($2)
			  and ec.id <> $3
			  and ec.status = 'pending'`,
			orgID, email, volunteerID).Scan(&otherPendingCrew); err != nil {
			return err
		}
		if otherPendingCrew == 0 {
			if _, err := tx.Exec(ctx, `
				update invitation set status = 'canceled'
				where organization_id = $1 and lower(email) = lower($2) and role = 'volunteer' and status = 'pending'`,
				orgID, email); err != nil {
				return err
			}
		}

		resolvedUserID := userID
		if resolvedUserID == nil {
			var uid string
			if err := tx.QueryRow(ctx, `select id from "user" where lower(email) = lower($1) limit 1`, email).Scan(&uid); err == nil {
				resolvedUserID = &uid
			}
		}

		if resolvedUserID != nil {
			var activeAccepted int
			if err := tx.QueryRow(ctx, `
				select count(*)
				from event_crew ec
				join event e on e.id = ec.event_id
				where e.organization_id = $1
				  and (ec.user_id = $2 or lower(ec.email) = lower($3))
				  and ec.status = 'accepted'`,
				orgID, *resolvedUserID, email).Scan(&activeAccepted); err != nil {
				return err
			}
			if activeAccepted == 0 {
				if _, err := tx.Exec(ctx, `
					delete from member
					where organization_id = $1 and user_id = $2 and role = 'volunteer'`,
					orgID, *resolvedUserID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Volunteer tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, "remove volunteer", err)
		return
	}
	ok(w)
}
