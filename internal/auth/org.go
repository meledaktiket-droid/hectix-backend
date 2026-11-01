package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
)

const (
	invitationExpiresIn = 7 * 24 * time.Hour
	creatorRole         = "owner"
	orgLimit            = 100
)

func (a *Auth) OrgRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(requiredAuthAPI)
		r.Get("/api/auth/organization/get-full-organization", a.getFullOrganization)
		r.Get("/api/auth/organization/list", a.listOrganizations)
		r.Get("/api/auth/organization/list-invitations", a.listInvitations)
	})
	r.Post("/api/auth/organization/set-active", a.setActiveOrganization)
	r.Post("/api/auth/organization/create", a.createOrganization)
	r.Post("/api/auth/organization/update", a.updateOrganization)
	r.Post("/api/auth/organization/invite-member", a.inviteMember)
	r.Post("/api/auth/organization/cancel-invitation", a.cancelInvitation)
	r.Post("/api/auth/organization/accept-invitation", a.acceptInvitation)
	r.Post("/api/auth/organization/remove-member", a.removeMember)
	r.Post("/api/auth/organization/update-member-role", a.updateMemberRole)
	r.Post("/api/auth/organization/leave", a.leaveOrganization)
}

var orgErrors = map[string]string{
	"ORGANIZATION_ALREADY_EXISTS":                              "Organization already exists",
	"ORGANIZATION_SLUG_ALREADY_TAKEN":                          "Organization slug already taken",
	"ORGANIZATION_NOT_FOUND":                                   "Organization not found",
	"USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION":                 "User is not a member of the organization",
	"YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_ORGANIZATION":          "You are not allowed to update this organization",
	"NO_ACTIVE_ORGANIZATION":                                   "No active organization",
	"USER_IS_ALREADY_A_MEMBER_OF_THIS_ORGANIZATION":            "User is already a member of this organization",
	"MEMBER_NOT_FOUND":                                         "Member not found",
	"YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER":      "You cannot leave the organization as the only owner",
	"YOU_CANNOT_LEAVE_THE_ORGANIZATION_WITHOUT_AN_OWNER":       "You cannot leave the organization without an owner",
	"YOU_ARE_NOT_ALLOWED_TO_DELETE_THIS_MEMBER":                "You are not allowed to delete this member",
	"YOU_ARE_NOT_ALLOWED_TO_INVITE_USERS_TO_THIS_ORGANIZATION": "You are not allowed to invite users to this organization",
	"USER_IS_ALREADY_INVITED_TO_THIS_ORGANIZATION":             "User is already invited to this organization",
	"INVITATION_NOT_FOUND":                                     "Invitation not found",
	"YOU_ARE_NOT_THE_RECIPIENT_OF_THE_INVITATION":              "You are not the recipient of the invitation",
	"YOU_ARE_NOT_ALLOWED_TO_CANCEL_THIS_INVITATION":            "You are not allowed to cancel this invitation",
	"YOU_ARE_NOT_ALLOWED_TO_INVITE_USER_WITH_THIS_ROLE":        "You are not allowed to invite a user with this role",
	"FAILED_TO_RETRIEVE_INVITATION":                            "Failed to retrieve invitation",
	"YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER":                "You are not allowed to update this member",
	"ORGANIZATION_MEMBERSHIP_LIMIT_REACHED":                    "Organization membership limit reached",
	"INVITATION_LIMIT_REACHED":                                 "Invitation limit reached",
	"INVALID_EMAIL":                                            "Invalid email",
}

func orgError(w http.ResponseWriter, status int, code string) {
	authError(w, status, code, orgErrors[code])
}

func emptyError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
}

var orgRolePerms = map[string]map[string][]string{
	"owner":  {"organization": {"update", "delete"}, "member": {"create", "update", "delete"}, "invitation": {"create", "cancel"}},
	"admin":  {"organization": {"update"}, "member": {"create", "update", "delete"}, "invitation": {"create", "cancel"}},
	"member": {},
}

func hasOrgPermission(role, resource, action string, allowCreatorAll bool) bool {
	roles := strings.Split(role, ",")
	if allowCreatorAll && slices.Contains(roles, creatorRole) {
		return true
	}
	for _, r := range roles {
		if slices.Contains(orgRolePerms[r][resource], action) {
			return true
		}
	}
	return false
}

func hasRole(role, want string) bool { return slices.Contains(strings.Split(role, ","), want) }

var orgRoleLabels = map[string]string{"owner": "Owner", "admin": "Admin", "member": "Member"}

type Organization struct {
	Name      string     `json:"name"`
	Slug      *string    `json:"slug"`
	Logo      *string    `json:"logo"`
	CreatedAt httpx.Time `json:"createdAt"`
	Metadata  *string    `json:"metadata"`
	ID        string     `json:"id"`
}

type parsedOrg struct {
	Name      string          `json:"name"`
	Slug      *string         `json:"slug"`
	Logo      *string         `json:"logo"`
	CreatedAt httpx.Time      `json:"createdAt"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	ID        string          `json:"id"`
}

func (o *Organization) parsed() parsedOrg {
	p := parsedOrg{Name: o.Name, Slug: o.Slug, Logo: o.Logo, CreatedAt: o.CreatedAt, ID: o.ID}
	if o.Metadata != nil && *o.Metadata != "" && json.Valid([]byte(*o.Metadata)) {
		p.Metadata = json.RawMessage(*o.Metadata)
	}
	return p
}

type Member struct {
	OrganizationID string      `json:"organizationId"`
	UserID         string      `json:"userId"`
	Role           string      `json:"role"`
	CreatedAt      httpx.Time  `json:"createdAt"`
	ID             string      `json:"id"`
	User           *memberUser `json:"user,omitempty"`
}

type memberUser struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Email string  `json:"email"`
	Image *string `json:"image"`
}

type Invitation struct {
	OrganizationID string     `json:"organizationId"`
	Email          string     `json:"email"`
	Role           *string    `json:"role"`
	Status         string     `json:"status"`
	ExpiresAt      httpx.Time `json:"expiresAt"`
	CreatedAt      httpx.Time `json:"createdAt"`
	InviterID      string     `json:"inviterId"`
	ID             string     `json:"id"`
}

const (
	orgCols    = `o.name, o.slug, o.logo, o.created_at, o.metadata, o.id`
	memberCols = `m.organization_id, m.user_id, m.role, m.created_at, m.id`
	userCols   = `u.id, u.name, u.email, u.image`
	invCols    = `i.organization_id, i.email, i.role, i.status, i.expires_at, i.created_at, i.inviter_id, i.id`
)

func scanOrg(row pgx.Row) (*Organization, error) {
	o := &Organization{}
	return o, row.Scan(&o.Name, &o.Slug, &o.Logo, &o.CreatedAt, &o.Metadata, &o.ID)
}

func scanMember(row pgx.Row, withUser bool) (*Member, error) {
	m := &Member{}
	dest := []any{&m.OrganizationID, &m.UserID, &m.Role, &m.CreatedAt, &m.ID}
	if withUser {
		m.User = &memberUser{}
		dest = append(dest, &m.User.ID, &m.User.Name, &m.User.Email, &m.User.Image)
	}
	return m, row.Scan(dest...)
}

func scanInvitation(row pgx.Row) (*Invitation, error) {
	i := &Invitation{}
	return i, row.Scan(&i.OrganizationID, &i.Email, &i.Role, &i.Status, &i.ExpiresAt, &i.CreatedAt, &i.InviterID, &i.ID)
}

func one[T any](v *T, err error) (*T, error) {
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

func collect[T any](ctx context.Context, db dbx.DB, scan func(pgx.Row) (*T, error), sql string, args ...any) ([]*T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (a *Auth) findOrg(ctx context.Context, field, value string) (*Organization, error) {
	return one(scanOrg(a.DB.QueryRow(ctx, `select `+orgCols+` from organization o where o.`+field+` = $1`, value)))
}

func (a *Auth) findMemberByOrgID(ctx context.Context, userID, orgID string) (*Member, error) {
	return one(scanMember(a.DB.QueryRow(ctx, `select `+memberCols+`, `+userCols+`
		from member m join "user" u on u.id = m.user_id
		where m.user_id = $1 and m.organization_id = $2 limit 1`, userID, orgID), true))
}

func (a *Auth) findMemberByID(ctx context.Context, id string) (*Member, error) {
	return one(scanMember(a.DB.QueryRow(ctx, `select `+memberCols+`, `+userCols+`
		from member m join "user" u on u.id = m.user_id where m.id = $1`, id), true))
}

func (a *Auth) findMemberByEmail(ctx context.Context, email, orgID string) (*Member, error) {
	return one(scanMember(a.DB.QueryRow(ctx, `select `+memberCols+`, `+userCols+`
		from "user" u join member m on m.user_id = u.id
		where u.email = $1 and m.organization_id = $2 limit 1`, strings.ToLower(email), orgID), true))
}

func (a *Auth) findInvitation(ctx context.Context, id string) (*Invitation, error) {
	return one(scanInvitation(a.DB.QueryRow(ctx, `select `+invCols+` from invitation i where i.id = $1`, id)))
}

func (a *Auth) countOwners(ctx context.Context, orgID string) (int, error) {
	var n int
	err := a.DB.QueryRow(ctx, `select count(*) from member
		where organization_id = $1 and $2 = any(string_to_array(role, ','))`, orgID, creatorRole).Scan(&n)
	return n, err
}

func (a *Auth) setActiveOrg(ctx context.Context, s *Session, orgID *string) error {
	now := time.Now().UTC()
	_, err := a.DB.Exec(ctx, `update session set active_organization_id = $1, updated_at = $2 where token = $3`, orgID, now, s.Token)
	if err == nil {
		s.ActiveOrganizationID, s.UpdatedAt = orgID, httpx.Time{Time: now}
	}
	return err
}

func strOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

type zv struct{ issues []string }

func (v *zv) add(path, msg string) { v.issues = append(v.issues, "["+path+"] "+msg) }

func (v *zv) failed(w http.ResponseWriter) bool {
	if len(v.issues) == 0 {
		return false
	}
	validationError(w, strings.Join(v.issues, "; "))
	return true
}

type presence int

const (
	required presence = iota
	optional
	nullish
)

func (v *zv) str(m map[string]json.RawMessage, prefix, key string, p presence, min1 bool) (s string, ok bool) {
	raw, present := m[key]
	path := prefix + "." + key
	t := zodType(raw, present)
	switch {
	case !present && p != required, t == "null" && p == nullish:
		return "", false
	case t != "string":
		v.add(path, "Invalid input: expected string, received "+t)
		return "", false
	}
	json.Unmarshal(raw, &s)
	if min1 && s == "" {
		v.add(path, "Too small: expected string to have >=1 characters")
		return "", false
	}
	return s, true
}

func (v *zv) boolean(m map[string]json.RawMessage, prefix, key string) bool {
	raw, present := m[key]
	if !present {
		return false
	}
	if t := zodType(raw, true); t != "boolean" {
		v.add(prefix+"."+key, "Invalid input: expected boolean, received "+t)
		return false
	}
	var b bool
	json.Unmarshal(raw, &b)
	return b
}

func (v *zv) record(m map[string]json.RawMessage, prefix, key string) (json.RawMessage, bool) {
	raw, present := m[key]
	if !present {
		return nil, false
	}
	if t := zodType(raw, true); t != "object" {
		v.add(prefix+"."+key, "Invalid input: expected record, received "+t)
		return nil, false
	}
	var buf bytes.Buffer
	json.Compact(&buf, raw)
	return buf.Bytes(), true
}

func (v *zv) roles(m map[string]json.RawMessage, prefix, key string, p presence) (joined string, list []string, ok bool) {
	raw, present := m[key]
	if !present && p == optional {
		return "", nil, false
	}
	var s string
	if zodType(raw, present) == "string" && json.Unmarshal(raw, &s) == nil {
		return s, []string{s}, true
	}
	if zodType(raw, present) == "array" && json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, ","), list, true
	}
	v.add(prefix+"."+key, "Invalid input")
	return "", nil, false
}

func bodyObject(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	body, bad := decodeObject(r)
	if bad != "" {
		validationError(w, "[body] Invalid input: expected object, received "+bad)
		return nil, false
	}
	return body, true
}

func sessionUser(w http.ResponseWriter, r *http.Request) (*User, *Session, bool) {
	u, ok := authUser(w, r)
	if !ok {
		return nil, nil, false
	}
	return u, SessionFrom(r.Context()), true
}

func (a *Auth) getFullOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, sess := UserFrom(ctx), SessionFrom(ctx)
	q := r.URL.Query()
	field, value := "id", q.Get("organizationId")
	if slug := q.Get("organizationSlug"); slug != "" {
		field, value = "slug", slug
	} else if value == "" {
		value = strOr(sess.ActiveOrganizationID)
	}
	if value == "" {
		httpx.JSON(w, http.StatusOK, nil)
		return
	}
	membersLimit := orgLimit
	if n, ok := parseIntPrefix(q.Get("membersLimit")); ok && n > 0 {
		membersLimit = n
	}
	org, err := a.findOrg(ctx, field, value)
	if err != nil {
		serverError(w, "get-full-organization", err)
		return
	}
	if org == nil {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, org.ID)
	if err != nil {
		serverError(w, "get-full-organization", err)
		return
	}
	if me == nil {
		if err := a.setActiveOrg(ctx, sess, nil); err != nil {
			serverError(w, "get-full-organization", err)
			return
		}
		orgError(w, http.StatusForbidden, "USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION")
		return
	}
	invs, err := collect(ctx, a.DB, scanInvitation, `select `+invCols+` from invitation i
		where i.organization_id = $1 order by i.created_at, i.id limit $2`, org.ID, orgLimit)
	if err != nil {
		serverError(w, "get-full-organization", err)
		return
	}
	members, err := collect(ctx, a.DB, func(row pgx.Row) (*Member, error) { return scanMember(row, true) }, `select `+memberCols+`, `+userCols+`
		from member m join "user" u on u.id = m.user_id
		where m.organization_id = $1 order by m.created_at, m.id limit $2`, org.ID, membersLimit)
	if err != nil {
		serverError(w, "get-full-organization", err)
		return
	}
	httpx.JSON(w, http.StatusOK, struct {
		*Organization
		Invitations []*Invitation `json:"invitations"`
		Members     []*Member     `json:"members"`
	}{org, invs, members})
}

func parseIntPrefix(s string) (int, bool) {
	s = strings.TrimSpace(s)
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg, s = s[0] == '-', s[1:]
	}
	n, digits := 0, 0
	for _, c := range s {
		if c < '0' || c > '9' || n > 1<<30 {
			break
		}
		n, digits = n*10+int(c-'0'), digits+1
	}
	if digits == 0 {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func (a *Auth) listOrganizations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgs, err := collect(ctx, a.DB, scanOrg, `select `+orgCols+` from member m
		join organization o on o.id = m.organization_id
		where m.user_id = $1 order by m.created_at, m.id limit $2`, UserFrom(ctx).ID, orgLimit)
	if err != nil {
		serverError(w, "organization/list", err)
		return
	}
	httpx.JSON(w, http.StatusOK, orgs)
}

func (a *Auth) listInvitations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgID := r.URL.Query().Get("organizationId")
	if orgID == "" {
		orgID = strOr(SessionFrom(ctx).ActiveOrganizationID)
	}
	if orgID == "" {
		authError(w, http.StatusBadRequest, "", "Organization ID is required")
		return
	}
	me, err := a.findMemberByOrgID(ctx, UserFrom(ctx).ID, orgID)
	if err != nil {
		serverError(w, "list-invitations", err)
		return
	}
	if me == nil {
		authError(w, http.StatusForbidden, "", "You are not a member of this organization")
		return
	}
	invs, err := collect(ctx, a.DB, scanInvitation, `select `+invCols+` from invitation i
		where i.organization_id = $1 order by i.created_at, i.id limit $2`, orgID, orgLimit)
	if err != nil {
		serverError(w, "list-invitations", err)
		return
	}
	httpx.JSON(w, http.StatusOK, invs)
}

func (a *Auth) setActiveOrganization(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	orgID, _ := v.str(body, "body", "organizationId", nullish, false)
	slug, _ := v.str(body, "body", "organizationSlug", optional, false)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if raw, ok := body["organizationId"]; ok && zodType(raw, true) == "null" {
		if sess.ActiveOrganizationID != nil {
			if err := a.setActiveOrg(ctx, sess, nil); err != nil {
				serverError(w, "set-active", err)
				return
			}
			a.SetSessionCookie(w, r, sess)
		}
		httpx.JSON(w, http.StatusOK, nil)
		return
	}
	if orgID == "" && slug == "" {
		if sess.ActiveOrganizationID == nil || *sess.ActiveOrganizationID == "" {
			httpx.JSON(w, http.StatusOK, nil)
			return
		}
		orgID = *sess.ActiveOrganizationID
	}
	if slug != "" && orgID == "" {
		org, err := a.findOrg(ctx, "slug", slug)
		if err != nil {
			serverError(w, "set-active", err)
			return
		}
		if org == nil {
			orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
			return
		}
		orgID = org.ID
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "set-active", err)
		return
	}
	if me == nil {
		if err := a.setActiveOrg(ctx, sess, nil); err != nil {
			serverError(w, "set-active", err)
			return
		}
		orgError(w, http.StatusForbidden, "USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION")
		return
	}
	org, err := a.findOrg(ctx, "id", orgID)
	if err != nil {
		serverError(w, "set-active", err)
		return
	}
	if org == nil {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	if err := a.setActiveOrg(ctx, sess, &org.ID); err != nil {
		serverError(w, "set-active", err)
		return
	}
	a.SetSessionCookie(w, r, sess)
	httpx.JSON(w, http.StatusOK, org)
}

func (a *Auth) createOrganization(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	name, _ := v.str(body, "body", "name", required, true)
	slug, _ := v.str(body, "body", "slug", required, true)
	logo, hasLogo := v.str(body, "body", "logo", optional, false)
	metadata, hasMeta := v.record(body, "body", "metadata")
	keepActive := v.boolean(body, "body", "keepCurrentActiveOrganization")
	if v.failed(w) {
		return
	}
	if loadErr(r.Context()) != nil {
		failedToGetSession(w)
		return
	}
	user, sess := UserFrom(r.Context()), SessionFrom(r.Context())
	if user == nil {
		emptyError(w, http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	existing, err := a.findOrg(ctx, "slug", slug)
	if err != nil {
		serverError(w, "organization/create", err)
		return
	}
	if existing != nil {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_ALREADY_EXISTS")
		return
	}
	now := time.Now().UTC()
	org := &Organization{ID: dbx.ID(), Name: name, Slug: &slug, CreatedAt: httpx.Time{Time: now}}
	if hasLogo {
		org.Logo = &logo
	}
	if hasMeta {
		m := string(metadata)
		org.Metadata = &m
	}
	member := &Member{ID: dbx.ID(), OrganizationID: org.ID, UserID: user.ID, Role: creatorRole, CreatedAt: httpx.Time{Time: now}}
	err = dbx.Tx(ctx, a.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `insert into organization (id, name, slug, logo, created_at, metadata)
			values ($1, $2, $3, $4, $5, $6)`, org.ID, org.Name, org.Slug, org.Logo, now, org.Metadata); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into member (id, organization_id, user_id, role, created_at)
			values ($1, $2, $3, $4, $5)`, member.ID, member.OrganizationID, member.UserID, member.Role, now)
		return err
	})
	if err != nil {
		serverError(w, "organization/create", err)
		return
	}
	if !keepActive {
		if err := a.setActiveOrg(ctx, sess, &org.ID); err != nil {
			serverError(w, "organization/create", err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, struct {
		parsedOrg
		Members []*Member `json:"members"`
	}{org.parsed(), []*Member{member}})
}

func (a *Auth) updateOrganization(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	var data map[string]json.RawMessage
	if raw, present := body["data"]; zodType(raw, present) != "object" {
		v.add("body.data", "Invalid input: expected object, received "+zodType(raw, present))
	} else {
		json.Unmarshal(raw, &data)
	}
	name, hasName := v.str(data, "body.data", "name", optional, true)
	slug, hasSlug := v.str(data, "body.data", "slug", optional, true)
	logo, hasLogo := v.str(data, "body.data", "logo", optional, false)
	metadata, hasMeta := v.record(data, "body.data", "metadata")
	orgID, _ := v.str(body, "body", "organizationId", optional, false)
	if v.failed(w) {
		return
	}
	if loadErr(r.Context()) != nil {
		failedToGetSession(w)
		return
	}
	user, sess := UserFrom(r.Context()), SessionFrom(r.Context())
	if user == nil {
		authError(w, http.StatusUnauthorized, "", "User not found")
		return
	}
	ctx := r.Context()
	if orgID == "" {
		orgID = strOr(sess.ActiveOrganizationID)
	}
	if orgID == "" {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "organization/update", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION")
		return
	}
	if !hasOrgPermission(me.Role, "organization", "update", false) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_ORGANIZATION")
		return
	}
	if hasSlug {
		other, err := a.findOrg(ctx, "slug", slug)
		if err != nil {
			serverError(w, "organization/update", err)
			return
		}
		if other != nil && other.ID != orgID {
			orgError(w, http.StatusBadRequest, "ORGANIZATION_SLUG_ALREADY_TAKEN")
			return
		}
	}
	org, err := one(scanOrg(a.DB.QueryRow(ctx, `update organization o set
		name = case when $2 then $3 else o.name end,
		slug = case when $4 then $5 else o.slug end,
		logo = case when $6 then $7 else o.logo end,
		metadata = case when $8 then $9 else o.metadata end
		where o.id = $1 returning `+orgCols,
		orgID, hasName, name, hasSlug, slug, hasLogo, logo, hasMeta, string(metadata))))
	if err != nil {
		serverError(w, "organization/update", err)
		return
	}
	if org == nil {
		httpx.JSON(w, http.StatusOK, nil)
		return
	}
	httpx.JSON(w, http.StatusOK, org.parsed())
}

func (a *Auth) removeMember(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	target, _ := v.str(body, "body", "memberIdOrEmail", required, false)
	orgID, _ := v.str(body, "body", "organizationId", optional, false)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if orgID == "" {
		orgID = strOr(sess.ActiveOrganizationID)
	}
	if orgID == "" {
		orgError(w, http.StatusBadRequest, "NO_ACTIVE_ORGANIZATION")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "remove-member", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	var removed *Member
	if strings.Contains(target, "@") {
		removed, err = a.findMemberByEmail(ctx, target, orgID)
	} else if removed, err = a.findMemberByID(ctx, target); removed != nil {
		removed.User = nil
	}
	if err != nil {
		serverError(w, "remove-member", err)
		return
	}
	if removed == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if hasRole(removed.Role, creatorRole) {
		owners, err := a.countOwners(ctx, orgID)
		if err != nil {
			serverError(w, "remove-member", err)
			return
		}
		if !slices.Contains(trimmedRoles(me.Role), creatorRole) || owners <= 1 {
			orgError(w, http.StatusBadRequest, "YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER")
			return
		}
	}
	if !hasOrgPermission(me.Role, "member", "delete", false) {
		orgError(w, http.StatusUnauthorized, "YOU_ARE_NOT_ALLOWED_TO_DELETE_THIS_MEMBER")
		return
	}
	if removed.OrganizationID != orgID {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if _, err := a.DB.Exec(ctx, `delete from member where id = $1`, removed.ID); err != nil {
		serverError(w, "remove-member", err)
		return
	}
	if user.ID == removed.UserID && strOr(sess.ActiveOrganizationID) == removed.OrganizationID {
		if err := a.setActiveOrg(ctx, sess, nil); err != nil {
			serverError(w, "remove-member", err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]*Member{"member": removed})
}

func trimmedRoles(role string) []string {
	out := strings.Split(role, ",")
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}

func (a *Auth) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	newRole, roleList, _ := v.roles(body, "body", "role", required)
	memberID, _ := v.str(body, "body", "memberId", required, false)
	orgID, _ := v.str(body, "body", "organizationId", optional, false)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	if raw := body["role"]; zodType(raw, true) == "string" && newRole == "" {
		emptyError(w, http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if orgID == "" {
		orgID = strOr(sess.ActiveOrganizationID)
	}
	if orgID == "" {
		orgError(w, http.StatusBadRequest, "NO_ACTIVE_ORGANIZATION")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "update-member-role", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	target := me
	if me.ID != memberID {
		if target, err = a.findMemberByID(ctx, memberID); err != nil {
			serverError(w, "update-member-role", err)
			return
		}
	}
	if target == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if target.OrganizationID != orgID {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER")
		return
	}
	updaterIsCreator := hasRole(me.Role, creatorRole)
	settingCreator := slices.Contains(roleList, creatorRole) || hasRole(newRole, creatorRole)
	if (hasRole(target.Role, creatorRole) || settingCreator) && !updaterIsCreator {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER")
		return
	}
	if updaterIsCreator && me.ID == target.ID {
		owners, err := a.countOwners(ctx, orgID)
		if err != nil {
			serverError(w, "update-member-role", err)
			return
		}
		if owners <= 1 && !settingCreator {
			orgError(w, http.StatusBadRequest, "YOU_CANNOT_LEAVE_THE_ORGANIZATION_WITHOUT_AN_OWNER")
			return
		}
	}
	if !hasOrgPermission(me.Role, "member", "update", true) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER")
		return
	}
	updated, err := one(scanMember(a.DB.QueryRow(ctx, `update member m set role = $2 where m.id = $1
		returning `+memberCols, memberID, newRole), false))
	if err != nil {
		serverError(w, "update-member-role", err)
		return
	}
	if updated == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	httpx.JSON(w, http.StatusOK, updated)
}

func (a *Auth) leaveOrganization(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	orgID, _ := v.str(body, "body", "organizationId", required, false)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "leave", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if hasRole(me.Role, creatorRole) {
		owners, err := a.countOwners(ctx, orgID)
		if err != nil {
			serverError(w, "leave", err)
			return
		}
		if owners <= 1 {
			orgError(w, http.StatusBadRequest, "YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER")
			return
		}
	}
	if _, err := a.DB.Exec(ctx, `delete from member where id = $1`, me.ID); err != nil {
		serverError(w, "leave", err)
		return
	}
	if strOr(sess.ActiveOrganizationID) == orgID {
		if err := a.setActiveOrg(ctx, sess, nil); err != nil {
			serverError(w, "leave", err)
			return
		}
	}
	httpx.JSON(w, http.StatusOK, me)
}

var emailRe = regexp.MustCompile(`^([A-Za-z0-9_'+\-\.]*)[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$`)

func validEmail(s string) bool {
	return !strings.HasPrefix(s, ".") && !strings.Contains(s, "..") && emailRe.MatchString(s)
}

func (a *Auth) inviteMember(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	email, _ := v.str(body, "body", "email", required, false)
	roles, _, _ := v.roles(body, "body", "role", required)
	orgID, _ := v.str(body, "body", "organizationId", optional, false)
	resend := v.boolean(body, "body", "resend")
	v.roles(body, "body", "teamId", optional)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if orgID == "" {
		orgID = strOr(sess.ActiveOrganizationID)
	}
	if orgID == "" {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	email = strings.ToLower(email)
	if !validEmail(email) {
		orgError(w, http.StatusBadRequest, "INVALID_EMAIL")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, orgID)
	if err != nil {
		serverError(w, "invite-member", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if !hasOrgPermission(me.Role, "invitation", "create", false) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_INVITE_USERS_TO_THIS_ORGANIZATION")
		return
	}
	var unknown []string
	for _, role := range trimmedRoles(roles) {
		if _, known := orgRolePerms[role]; role != "" && !known {
			unknown = append(unknown, role)
		}
	}
	if len(unknown) > 0 {
		authError(w, http.StatusBadRequest, "", "Role not found: "+strings.Join(unknown, ", "))
		return
	}
	if !slices.Contains(trimmedRoles(me.Role), creatorRole) && hasRole(roles, creatorRole) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_INVITE_USER_WITH_THIS_ROLE")
		return
	}
	if existing, err := a.findMemberByEmail(ctx, email, orgID); err != nil {
		serverError(w, "invite-member", err)
		return
	} else if existing != nil {
		orgError(w, http.StatusBadRequest, "USER_IS_ALREADY_A_MEMBER_OF_THIS_ORGANIZATION")
		return
	}
	now := time.Now().UTC()
	pending, err := collect(ctx, a.DB, scanInvitation, `select `+invCols+` from invitation i
		where i.email = $1 and i.organization_id = $2 and i.status = 'pending' and i.expires_at > $3
		order by i.created_at, i.id`, email, orgID, now)
	if err != nil {
		serverError(w, "invite-member", err)
		return
	}
	if len(pending) > 0 && !resend {
		orgError(w, http.StatusBadRequest, "USER_IS_ALREADY_INVITED_TO_THIS_ORGANIZATION")
		return
	}
	org, err := a.findOrg(ctx, "id", orgID)
	if err != nil {
		serverError(w, "invite-member", err)
		return
	}
	if org == nil {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	expires := now.Add(invitationExpiresIn)
	if len(pending) > 0 {
		inv := pending[0]
		if _, err := a.DB.Exec(ctx, `update invitation set expires_at = $2 where id = $1`, inv.ID, expires); err != nil {
			serverError(w, "invite-member", err)
			return
		}
		inv.ExpiresAt = httpx.Time{Time: expires}
		if err := a.sendInvitation(r, inv, user, org); err != nil {
			serverError(w, "invite-member: send email", err)
			return
		}
		httpx.JSON(w, http.StatusOK, inv)
		return
	}
	var open int
	if err := a.DB.QueryRow(ctx, `select count(*) from invitation
		where organization_id = $1 and status = 'pending' and expires_at > $2`, orgID, now).Scan(&open); err != nil {
		serverError(w, "invite-member", err)
		return
	}
	if open >= orgLimit {
		orgError(w, http.StatusForbidden, "INVITATION_LIMIT_REACHED")
		return
	}
	inv := &Invitation{
		ID: dbx.ID(), OrganizationID: orgID, Email: email, Role: &roles, Status: "pending",
		ExpiresAt: httpx.Time{Time: expires}, CreatedAt: httpx.Time{Time: now}, InviterID: user.ID,
	}
	if _, err := a.DB.Exec(ctx, `insert into invitation (id, organization_id, email, role, status, expires_at, inviter_id, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)`,
		inv.ID, inv.OrganizationID, inv.Email, inv.Role, inv.Status, expires, inv.InviterID, now); err != nil {
		serverError(w, "invite-member", err)
		return
	}
	if err := a.sendInvitation(r, inv, user, org); err != nil {
		serverError(w, "invite-member: send email", err)
		return
	}
	httpx.JSON(w, http.StatusOK, inv)
}

func (a *Auth) sendInvitation(r *http.Request, inv *Invitation, inviter *User, org *Organization) error {
	if a.Mail == nil {
		return nil
	}
	inviterName := inviter.Name
	if inviterName == "" {
		inviterName = inviter.Email
	}
	role := strOr(inv.Role)
	label, ok := orgRoleLabels[role]
	if !ok {
		label = role
	}
	to := strings.ToLower(inv.Email)
	acceptURL := httpx.AppFrom(r.Context()).BaseURL + "/invite/accept?id=" + inv.ID
	m := mail.OrgInvite(to, inviterName, org.Name, label, acceptURL)
	return a.Mail.Send(r.Context(), to, m.Subject, m.HTML, m.Text)
}

func (a *Auth) cancelInvitation(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	id, _ := v.str(body, "body", "invitationId", required, false)
	if v.failed(w) {
		return
	}
	user, _, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	inv, err := a.findInvitation(ctx, id)
	if err != nil {
		serverError(w, "cancel-invitation", err)
		return
	}
	if inv == nil {
		orgError(w, http.StatusBadRequest, "INVITATION_NOT_FOUND")
		return
	}
	me, err := a.findMemberByOrgID(ctx, user.ID, inv.OrganizationID)
	if err != nil {
		serverError(w, "cancel-invitation", err)
		return
	}
	if me == nil {
		orgError(w, http.StatusBadRequest, "MEMBER_NOT_FOUND")
		return
	}
	if !hasOrgPermission(me.Role, "invitation", "cancel", false) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_ALLOWED_TO_CANCEL_THIS_INVITATION")
		return
	}
	canceled, err := one(scanInvitation(a.DB.QueryRow(ctx, `update invitation i set status = 'canceled'
		where i.id = $1 returning `+invCols, id)))
	if err != nil {
		serverError(w, "cancel-invitation", err)
		return
	}
	httpx.JSON(w, http.StatusOK, canceled)
}

func (a *Auth) acceptInvitation(w http.ResponseWriter, r *http.Request) {
	body, ok := bodyObject(w, r)
	if !ok {
		return
	}
	var v zv
	id, _ := v.str(body, "body", "invitationId", required, false)
	if v.failed(w) {
		return
	}
	user, sess, ok := sessionUser(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	inv, err := a.findInvitation(ctx, id)
	if err != nil {
		serverError(w, "accept-invitation", err)
		return
	}
	if inv == nil || inv.ExpiresAt.Before(time.Now()) || inv.Status != "pending" {
		orgError(w, http.StatusBadRequest, "INVITATION_NOT_FOUND")
		return
	}
	if !strings.EqualFold(inv.Email, user.Email) {
		orgError(w, http.StatusForbidden, "YOU_ARE_NOT_THE_RECIPIENT_OF_THE_INVITATION")
		return
	}
	var members int
	if err := a.DB.QueryRow(ctx, `select count(*) from member where organization_id = $1`, inv.OrganizationID).Scan(&members); err != nil {
		serverError(w, "accept-invitation", err)
		return
	}
	if org, err := a.findOrg(ctx, "id", inv.OrganizationID); err != nil {
		serverError(w, "accept-invitation", err)
		return
	} else if org == nil {
		orgError(w, http.StatusBadRequest, "ORGANIZATION_NOT_FOUND")
		return
	}
	if members >= orgLimit {
		orgError(w, http.StatusForbidden, "ORGANIZATION_MEMBERSHIP_LIMIT_REACHED")
		return
	}
	accepted, err := one(scanInvitation(a.DB.QueryRow(ctx, `update invitation i set status = 'accepted'
		where i.id = $1 returning `+invCols, id)))
	if err != nil {
		serverError(w, "accept-invitation", err)
		return
	}
	if accepted == nil {
		orgError(w, http.StatusBadRequest, "FAILED_TO_RETRIEVE_INVITATION")
		return
	}
	role := "member"
	if inv.Role != nil {
		role = *inv.Role
	}
	now := time.Now().UTC()
	member := &Member{ID: dbx.ID(), OrganizationID: inv.OrganizationID, UserID: user.ID, Role: role, CreatedAt: httpx.Time{Time: now}}
	if _, err := a.DB.Exec(ctx, `insert into member (id, organization_id, user_id, role, created_at)
		values ($1, $2, $3, $4, $5)`, member.ID, member.OrganizationID, member.UserID, member.Role, now); err != nil {
		serverError(w, "accept-invitation", err)
		return
	}
	if err := a.setActiveOrg(ctx, sess, &inv.OrganizationID); err != nil {
		serverError(w, "accept-invitation", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"invitation": accepted, "member": member})
}
