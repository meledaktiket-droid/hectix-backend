package admin

import (
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

var orgStatuses = []string{"pending", "review", "active", "rejected", "suspended"}

type org struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Slug             *string    `json:"slug"`
	Logo             *string    `json:"logo"`
	CreatedAt        httpx.Time `json:"createdAt"`
	Metadata         *string    `json:"metadata"`
	Status           string     `json:"status"`
	VerificationData *string    `json:"verificationData"`
	StatusNote       *string    `json:"statusNote"`
	AllowResubmit    bool       `json:"allowResubmit"`
}

const orgCols = `id, name, slug, logo, created_at, metadata, status, verification_data, status_note, allow_resubmit`

func (h *handlers) listOrgs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := pageParam(r, "page")
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains(orgStatuses, status) {
		httpx.Error(w, http.StatusBadRequest, "Invalid status")
		return
	}
	var c conds
	if status != "" {
		c.add("status = ?", status)
	}
	rows, err := list[org](ctx, h.d.DB, `select `+orgCols+` from organization`+c.where()+`
		order by case status when 'pending' then 1 when 'review' then 2 when 'active' then 3
			when 'rejected' then 4 when 'suspended' then 5 else 6 end, created_at
		limit `+itoa(pageLimit)+` offset `+itoa((p-1)*pageLimit), c.args...)
	if err != nil {
		serverError(w, "orgs", err)
		return
	}
	var total int64
	if err := h.d.DB.QueryRow(ctx, `select count(*) from organization`+c.where(), c.args...).Scan(&total); err != nil {
		serverError(w, "orgs", err)
		return
	}
	httpx.JSON(w, http.StatusOK, page[org]{rows, total, p, totalPages(total, pageLimit)})
}

type orgMember struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	CreatedAt httpx.Time `json:"createdAt"`
	UserID    string     `json:"userId"`
	Name      string     `json:"name"`
	Email     string     `json:"email"`
	Image     *string    `json:"image"`
}

type statusHistory struct {
	ID         string     `json:"id"`
	FromStatus *string    `json:"fromStatus"`
	ToStatus   string     `json:"toStatus"`
	Note       *string    `json:"note"`
	ActorType  string     `json:"actorType"`
	ActorName  *string    `json:"actorName"`
	ActorEmail *string    `json:"actorEmail"`
	CreatedAt  httpx.Time `json:"createdAt"`
}

func (h *handlers) getOrg(w http.ResponseWriter, r *http.Request) {
	ctx, db, id := r.Context(), h.d.DB, chi.URLParam(r, "id")
	o, err := one[org](ctx, db, `select `+orgCols+` from organization where id = $1`, id)
	if err != nil {
		serverError(w, "org", err)
		return
	}
	if o == nil {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	members, err := list[orgMember](ctx, db, `select m.id, m.role, m.created_at, m.user_id, u.name, u.email, u.image
		from member m join "user" u on m.user_id = u.id where m.organization_id = $1 order by m.created_at, m.id`, id)
	if err != nil {
		serverError(w, "org", err)
		return
	}
	history, err := list[statusHistory](ctx, db, `select h.id, h.from_status, h.to_status, h.note, h.actor_type,
		u.name, u.email, h.created_at
		from organization_status_history h left join "user" u on h.actor_id = u.id
		where h.organization_id = $1 order by h.created_at desc`, id)
	if err != nil {
		log.Printf("admin: [orgs] failed to load status history: %v", err)
		history = []statusHistory{}
	}
	httpx.JSON(w, http.StatusOK, struct {
		*org
		Members []orgMember     `json:"members"`
		History []statusHistory `json:"history"`
	}{o, members, history})
}

func (h *handlers) patchOrg(w http.ResponseWriter, r *http.Request) {
	ctx, id := r.Context(), chi.URLParam(r, "id")
	var body struct {
		Status        string  `json:"status"`
		StatusNote    *string `json:"statusNote"`
		AllowResubmit *bool   `json:"allowResubmit"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	if !slices.Contains(orgStatuses, body.Status) {
		httpx.Error(w, http.StatusBadRequest, "Invalid status")
		return
	}
	allow := body.Status == "rejected" && body.AllowResubmit != nil && *body.AllowResubmit
	var from string
	err := h.d.DB.QueryRow(ctx, `update organization o set status = $2, status_note = $3, allow_resubmit = $4
		from (select id, status from organization where id = $1 for update) old
		where o.id = old.id returning old.status`, id, body.Status, body.StatusNote, allow).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "patch org", err)
		return
	}
	if from != body.Status {
		if _, err := h.d.DB.Exec(ctx, `insert into organization_status_history
			(id, organization_id, from_status, to_status, note, actor_id, actor_type, created_at)
			values ($1, $2, $3, $4, $5, $6, 'admin', $7)`,
			dbx.UUID(), id, from, body.Status, body.StatusNote, auth.UserFrom(ctx).ID, time.Now().UTC()); err != nil {
			log.Printf("admin: [status-history] failed to record transition: %v", err)
		}
	}
	ok(w)
}
