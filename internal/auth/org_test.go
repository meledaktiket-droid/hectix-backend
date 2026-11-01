package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/golden"
	"hectix/backend/internal/mail"
	"hectix/backend/internal/testdb"
)

type sentMail struct{ to, subject, html, text string }

type fakeMail struct {
	mu   sync.Mutex
	sent []sentMail
	err  error
}

func (f *fakeMail) Send(_ context.Context, to, subject, html, text string, _ ...mail.Attachment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, sentMail{to, subject, html, text})
	return nil
}

func (f *fakeMail) last() sentMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return sentMail{}
	}
	return f.sent[len(f.sent)-1]
}

func newOrgEnv(t *testing.T) (*env, *fakeMail) {
	pool := testdb.New(t)
	fm := &fakeMail{}
	a := &Auth{DB: pool, Secret: secret, Mail: fm}
	r := chi.NewRouter()
	r.Use(OriginCheck, a.Load)
	a.Routes(r)
	a.OrgRoutes(r)
	return &env{t: t, pool: pool, a: a, h: r}, fm
}

const sampleMeta = `{"type":"personal","name":"Sample Name","phone":"080000000000"}`

func (e *env) org(slug string) string {
	e.t.Helper()
	id := dbx.ID()
	_, err := e.pool.Exec(context.Background(), `insert into organization (id, name, slug, created_at, metadata)
		values ($1, 'Sample Name', $2, $3, $4)`, id, slug, time.Now().UTC(), sampleMeta)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) member(orgID, userID, role string) string {
	e.t.Helper()
	id := dbx.ID()
	_, err := e.pool.Exec(context.Background(), `insert into member (id, organization_id, user_id, role, created_at)
		values ($1, $2, $3, $4, $5)`, id, orgID, userID, role, time.Now().UTC())
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) invitation(orgID, inviter, email, role, status string, expiresIn time.Duration) string {
	e.t.Helper()
	id := dbx.ID()
	now := time.Now().UTC()
	_, err := e.pool.Exec(context.Background(), `insert into invitation (id, organization_id, email, role, status, expires_at, inviter_id, created_at)
		values ($1, $2, $3, $4, $5, $6, $7, $8)`, id, orgID, email, role, status, now.Add(expiresIn), inviter, now)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) orgUser() string {
	e.t.Helper()
	id := e.user("user")
	if _, err := e.pool.Exec(context.Background(), `update "user" set email = lower(email) where id = $1`, id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func email(uid string) string { return strings.ToLower(uid) + "@example.com" }

func (e *env) joined(orgID, role string) (userID, token string) {
	e.t.Helper()
	userID = e.orgUser()
	if role != "" {
		e.member(orgID, userID, role)
	}
	return userID, e.session(userID, time.Minute, week, &orgID)
}

func (e *env) activeOrg(token string) *string {
	e.t.Helper()
	var org *string
	if err := e.pool.QueryRow(context.Background(), `select active_organization_id from session where token = $1`, token).Scan(&org); err != nil {
		e.t.Fatal(err)
	}
	return org
}

func (e *env) post(path, token, body string) (int, string) {
	e.t.Helper()
	rec := e.do("organizer", "POST", "/api/auth/organization/"+path, token, body)
	return rec.Code, rec.Body.String()
}

func (e *env) want(code int, body string) func(int, string) {
	return func(gotCode int, gotBody string) {
		e.t.Helper()
		if gotCode != code || gotBody != body {
			e.t.Errorf("got %d %q, want %d %q", gotCode, gotBody, code, body)
		}
	}
}

func orgErr(code string) string {
	return `{"message":"` + orgErrors[code] + `","code":"` + code + `"}` + "\n"
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %q", err, body)
	}
	return m
}

func TestOrgRead(t *testing.T) {
	e, _ := newOrgEnv(t)

	t.Run("get-full-organization", func(t *testing.T) {
		e := e.on(t)
		org := e.org("full-" + dbx.ID())
		owner, tok := e.joined(org, "owner")
		admin := e.orgUser()
		e.member(org, admin, "admin")
		e.invitation(org, owner, "x@example.com", "admin", "canceled", week)
		e.invitation(org, owner, "y@example.com", "member", "pending", week)

		rec := e.do("organizer", "GET", "/api/auth/organization/get-full-organization", tok, "")
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		golden.Assert(t, "../../testdata/golden/organizer/organization-get-full-organization.json", rec.Body.Bytes())
		m := decode(t, rec.Body.String())
		if m["id"] != org || len(m["members"].([]any)) != 2 || len(m["invitations"].([]any)) != 2 {
			t.Errorf("got %s", rec.Body)
		}
		if _, ok := m["teams"]; ok {
			t.Error("teams must be absent")
		}
		if m["metadata"] != sampleMeta {
			t.Errorf("metadata should be the stored string, got %v", m["metadata"])
		}
		for _, k := range []string{"status", "verificationData", "statusNote", "allowResubmit"} {
			if _, ok := m[k]; ok {
				t.Errorf("%s must not be exposed", k)
			}
		}

		var slug string
		e.pool.QueryRow(context.Background(), `select slug from organization where id = $1`, org).Scan(&slug)
		other := e.org("other-" + dbx.ID())
		e.member(other, owner, "member")
		for _, q := range []string{"?organizationId=" + other, "?organizationSlug=" + slug} {
			rec := e.do("organizer", "GET", "/api/auth/organization/get-full-organization"+q, tok, "")
			want := other
			if strings.Contains(q, "Slug") {
				want = org
			}
			if rec.Code != 200 || decode(t, rec.Body.String())["id"] != want {
				t.Errorf("%s: %d %s", q, rec.Code, rec.Body)
			}
		}
		e.expect(e.do("organizer", "GET", "/api/auth/organization/get-full-organization?organizationId=nope", tok, ""), 400, orgErr("ORGANIZATION_NOT_FOUND"))
	})

	t.Run("get-full-organization: non-member is 403 and active org is cleared", func(t *testing.T) {
		e := e.on(t)
		org := e.org("nm-" + dbx.ID())
		_, tok := e.joined(org, "")
		e.expect(e.do("organizer", "GET", "/api/auth/organization/get-full-organization", tok, ""), 403,
			orgErr("USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION"))
		if e.activeOrg(tok) != nil {
			t.Error("active org not cleared")
		}
		e.expect(e.do("organizer", "GET", "/api/auth/organization/get-full-organization", tok, ""), 200, "null\n")
		e.expect(e.do("organizer", "GET", "/api/auth/organization/get-full-organization", "", ""), 401, unauthBody)
	})

	t.Run("list returns only my orgs", func(t *testing.T) {
		e := e.on(t)
		a, b, c := e.org("a-"+dbx.ID()), e.org("b-"+dbx.ID()), e.org("c-"+dbx.ID())
		me, tok := e.joined(a, "owner")
		e.member(b, me, "member")
		someone := e.orgUser()
		e.member(c, someone, "owner")
		rec := e.do("organizer", "GET", "/api/auth/organization/list", tok, "")
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		golden.Assert(t, "../../testdata/golden/organizer/organization-list.json", rec.Body.Bytes())
		var got []map[string]any
		json.Unmarshal(rec.Body.Bytes(), &got)
		if len(got) != 2 || got[0]["id"] != a || got[1]["id"] != b {
			t.Errorf("got %s", rec.Body)
		}
		e.expect(e.do("organizer", "GET", "/api/auth/organization/list", "", ""), 401, unauthBody)
	})

	t.Run("list-invitations", func(t *testing.T) {
		e := e.on(t)
		org := e.org("li-" + dbx.ID())
		owner, tok := e.joined(org, "owner")
		e.invitation(org, owner, "p@example.com", "admin", "pending", week)
		e.invitation(org, owner, "q@example.com", "member", "accepted", week)
		rec := e.do("organizer", "GET", "/api/auth/organization/list-invitations?organizationId="+org, tok, "")
		if rec.Code != 200 {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		golden.Assert(t, "../../testdata/golden/organizer/organization-list-invitations.json", rec.Body.Bytes())
		var got []map[string]any
		json.Unmarshal(rec.Body.Bytes(), &got)
		if len(got) != 2 {
			t.Errorf("got %s", rec.Body)
		}
		outsider := e.orgUser()
		otok := e.session(outsider, time.Minute, week, nil)
		e.expect(e.do("organizer", "GET", "/api/auth/organization/list-invitations?organizationId="+org, otok, ""), 403,
			`{"message":"You are not a member of this organization"}`+"\n")
		e.expect(e.do("organizer", "GET", "/api/auth/organization/list-invitations", otok, ""), 400,
			`{"message":"Organization ID is required"}`+"\n")
	})
}

func TestOrgWrite(t *testing.T) {
	e, _ := newOrgEnv(t)
	ctx := context.Background()

	t.Run("create inserts org, owner member and sets active org", func(t *testing.T) {
		e := e.on(t)
		uid := e.orgUser()
		tok := e.session(uid, time.Minute, week, nil)
		slug := "acme-" + strings.ToLower(dbx.ID())
		code, body := e.post("create", tok, `{"name":"Acme","slug":"`+slug+`","metadata":{"type":"entity","phone":"0812"}}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		m := decode(t, body)
		id := m["id"].(string)
		meta, _ := m["metadata"].(map[string]any)
		if m["name"] != "Acme" || m["slug"] != slug || meta["phone"] != "0812" || m["logo"] != nil {
			t.Errorf("got %s", body)
		}
		members := m["members"].([]any)
		if len(members) != 1 || members[0].(map[string]any)["role"] != "owner" || members[0].(map[string]any)["userId"] != uid {
			t.Errorf("members %v", members)
		}
		var status, storedMeta string
		var allow bool
		e.pool.QueryRow(ctx, `select status, metadata, allow_resubmit from organization where id = $1`, id).Scan(&status, &storedMeta, &allow)
		if status != "pending" || storedMeta != `{"type":"entity","phone":"0812"}` || allow {
			t.Errorf("stored status=%q metadata=%q allow=%v", status, storedMeta, allow)
		}
		if e.count(`select count(*) from member where organization_id = $1 and user_id = $2 and role = 'owner'`, id, uid) != 1 {
			t.Error("owner member not inserted")
		}
		if a := e.activeOrg(tok); a == nil || *a != id {
			t.Errorf("active org = %v", a)
		}

		e.want(400, orgErr("ORGANIZATION_ALREADY_EXISTS"))(e.post("create", tok, `{"name":"Acme","slug":"`+slug+`"}`))
		code, body = e.post("create", tok, `{"name":"Keep","slug":"keep-`+dbx.ID()+`","keepCurrentActiveOrganization":true}`)
		if code != 200 || strings.Contains(body, `"metadata"`) {
			t.Errorf("%d %s", code, body)
		}
		if a := e.activeOrg(tok); a == nil || *a != id {
			t.Errorf("keepCurrentActiveOrganization ignored: %v", a)
		}
		e.want(400, `{"message":"[body.name] Too small: expected string to have >=1 characters; [body.slug] Invalid input: expected string, received undefined","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("create", tok, `{"name":""}`))
		e.want(400, `{"message":"[body.metadata] Invalid input: expected record, received array","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("create", tok, `{"name":"x","slug":"y","metadata":[]}`))
		e.want(401, "")(e.post("create", "", `{"name":"x","slug":"y"}`))
	})

	t.Run("set-active", func(t *testing.T) {
		e := e.on(t)
		a, b := e.org("sa-"+dbx.ID()), e.org("sb-"+dbx.ID())
		me, tok := e.joined(a, "owner")
		e.member(b, me, "member")
		rec := e.do("organizer", "POST", "/api/auth/organization/set-active", tok, `{"organizationId":"`+b+`"}`)
		if rec.Code != 200 || decode(t, rec.Body.String())["id"] != b {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}
		if decode(t, rec.Body.String())["metadata"] != sampleMeta {
			t.Errorf("metadata should be the stored string: %s", rec.Body)
		}
		if c := rec.Header().Get("Set-Cookie"); !strings.HasPrefix(c, "__Secure-organizer.session_token="+Sign(tok, secret)) {
			t.Errorf("cookie %q", c)
		}
		if got := e.activeOrg(tok); got == nil || *got != b {
			t.Errorf("active org = %v", got)
		}

		stranger := e.org("st-" + dbx.ID())
		e.want(403, orgErr("USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION"))(e.post("set-active", tok, `{"organizationId":"`+stranger+`"}`))
		if e.activeOrg(tok) != nil {
			t.Error("active org not cleared after non-member set-active")
		}
		e.want(400, orgErr("ORGANIZATION_NOT_FOUND"))(e.post("set-active", tok, `{"organizationSlug":"no-such-slug"}`))
		e.want(200, "null\n")(e.post("set-active", tok, `{}`))
		e.post("set-active", tok, `{"organizationId":"`+a+`"}`)
		e.want(200, "null\n")(e.post("set-active", tok, `{"organizationId":null}`))
		if e.activeOrg(tok) != nil {
			t.Error("null did not unset active org")
		}
		e.want(400, `{"message":"[body.organizationId] Invalid input: expected string, received number","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("set-active", tok, `{"organizationId":1}`))
		e.want(401, unauthBody)(e.post("set-active", "", `{}`))
	})

	t.Run("update", func(t *testing.T) {
		e := e.on(t)
		org := e.org("up-" + dbx.ID())
		_, ownerTok := e.joined(org, "owner")
		_, adminTok := e.joined(org, "admin")
		_, memberTok := e.joined(org, "member")
		outsider := e.orgUser()
		outTok := e.session(outsider, time.Minute, week, &org)

		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_ORGANIZATION"))(e.post("update", memberTok, `{"data":{"name":"x"}}`))
		e.want(400, orgErr("USER_IS_NOT_A_MEMBER_OF_THE_ORGANIZATION"))(e.post("update", outTok, `{"data":{"name":"x"}}`))
		code, body := e.post("update", adminTok, `{"organizationId":"`+org+`","data":{"name":"Renamed","logo":"https://x/l.png","metadata":{"type":"entity","name":"Renamed"}}}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		m := decode(t, body)
		if m["name"] != "Renamed" || m["logo"] != "https://x/l.png" || m["metadata"].(map[string]any)["type"] != "entity" {
			t.Errorf("got %s", body)
		}
		var meta string
		e.pool.QueryRow(ctx, `select metadata from organization where id = $1`, org).Scan(&meta)
		if meta != `{"type":"entity","name":"Renamed"}` {
			t.Errorf("stored metadata %q", meta)
		}
		taken := "taken-" + dbx.ID()
		e.org(taken)
		e.want(400, orgErr("ORGANIZATION_SLUG_ALREADY_TAKEN"))(e.post("update", ownerTok, `{"data":{"slug":"`+taken+`"}}`))
		e.want(400, `{"message":"[body.data] Invalid input: expected object, received undefined","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("update", ownerTok, `{}`))
		e.want(401, `{"message":"User not found"}`+"\n")(e.post("update", "", `{"data":{}}`))
	})
}

func TestOrgMembership(t *testing.T) {
	e, _ := newOrgEnv(t)

	t.Run("remove-member", func(t *testing.T) {
		e := e.on(t)
		org := e.org("rm-" + dbx.ID())
		owner, ownerTok := e.joined(org, "owner")
		_, adminTok := e.joined(org, "admin")
		_, memberTok := e.joined(org, "member")
		var ownerMember, adminMember string
		e.pool.QueryRow(context.Background(), `select id from member where user_id = $1`, owner).Scan(&ownerMember)
		victim := e.orgUser()
		victimMember := e.member(org, victim, "member")

		e.want(401, orgErr("YOU_ARE_NOT_ALLOWED_TO_DELETE_THIS_MEMBER"))(e.post("remove-member", memberTok, `{"memberIdOrEmail":"`+victimMember+`"}`))
		e.want(400, orgErr("YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER"))(e.post("remove-member", adminTok, `{"memberIdOrEmail":"`+ownerMember+`"}`))
		e.want(400, orgErr("YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER"))(e.post("remove-member", ownerTok, `{"memberIdOrEmail":"`+ownerMember+`"}`))
		e.want(400, orgErr("MEMBER_NOT_FOUND"))(e.post("remove-member", adminTok, `{"memberIdOrEmail":"nope"}`))

		code, body := e.post("remove-member", adminTok, `{"memberIdOrEmail":"`+victimMember+`"}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		if mem := decode(t, body)["member"].(map[string]any); mem["id"] != victimMember || mem["user"] != nil {
			t.Errorf("got %s", body)
		}
		if e.count(`select count(*) from member where id = $1`, victimMember) != 0 {
			t.Error("member not deleted")
		}
		admin2 := e.orgUser()
		adminMember = e.member(org, admin2, "admin")
		code, body = e.post("remove-member", ownerTok, `{"memberIdOrEmail":"`+strings.ToUpper(admin2)+`@example.com"}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		if mem := decode(t, body)["member"].(map[string]any); mem["id"] != adminMember || mem["user"] == nil {
			t.Errorf("got %s", body)
		}
		other := e.org("rmo-" + dbx.ID())
		foreign := e.member(other, e.orgUser(), "member")
		e.want(400, orgErr("MEMBER_NOT_FOUND"))(e.post("remove-member", ownerTok, `{"memberIdOrEmail":"`+foreign+`"}`))
	})

	t.Run("update-member-role", func(t *testing.T) {
		e := e.on(t)
		org := e.org("ur-" + dbx.ID())
		owner, ownerTok := e.joined(org, "owner")
		admin, adminTok := e.joined(org, "admin")
		_, memberTok := e.joined(org, "member")
		id := func(uid string) string {
			var s string
			e.pool.QueryRow(context.Background(), `select id from member where user_id = $1 and organization_id = $2`, uid, org).Scan(&s)
			return s
		}
		ownerM, adminM := id(owner), id(admin)
		target := e.member(org, e.orgUser(), "member")

		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER"))(e.post("update-member-role", memberTok, `{"memberId":"`+target+`","role":"admin"}`))
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER"))(e.post("update-member-role", adminTok, `{"memberId":"`+ownerM+`","role":"member"}`))
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER"))(e.post("update-member-role", adminTok, `{"memberId":"`+target+`","role":"owner"}`))
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER"))(e.post("update-member-role", adminTok, `{"memberId":"`+target+`","role":"owner,admin"}`))
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_UPDATE_THIS_MEMBER"))(e.post("update-member-role", adminTok, `{"memberId":"`+target+`","role":["admin","owner"]}`))
		e.want(400, orgErr("YOU_CANNOT_LEAVE_THE_ORGANIZATION_WITHOUT_AN_OWNER"))(e.post("update-member-role", ownerTok, `{"memberId":"`+ownerM+`","role":"admin"}`))
		e.want(400, "")(e.post("update-member-role", ownerTok, `{"memberId":"`+target+`","role":""}`))
		e.want(400, `{"message":"[body.role] Invalid input; [body.memberId] Invalid input: expected string, received undefined","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("update-member-role", ownerTok, `{"role":5}`))

		code, body := e.post("update-member-role", adminTok, `{"memberId":"`+target+`","role":"admin"}`)
		if code != 200 || decode(t, body)["role"] != "admin" || decode(t, body)["id"] != target {
			t.Errorf("%d %s", code, body)
		}
		code, body = e.post("update-member-role", ownerTok, `{"memberId":"`+adminM+`","role":["owner","admin"]}`)
		if code != 200 || decode(t, body)["role"] != "owner,admin" {
			t.Errorf("%d %s", code, body)
		}
		code, body = e.post("update-member-role", ownerTok, `{"memberId":"`+ownerM+`","role":"member"}`)
		if code != 200 || decode(t, body)["role"] != "member" {
			t.Errorf("%d %s", code, body)
		}
	})

	t.Run("leave", func(t *testing.T) {
		e := e.on(t)
		org := e.org("lv-" + dbx.ID())
		_, ownerTok := e.joined(org, "owner")
		me, memberTok := e.joined(org, "member")
		e.want(400, orgErr("YOU_CANNOT_LEAVE_THE_ORGANIZATION_AS_THE_ONLY_OWNER"))(e.post("leave", ownerTok, `{"organizationId":"`+org+`"}`))
		code, body := e.post("leave", memberTok, `{"organizationId":"`+org+`"}`)
		if code != 200 || decode(t, body)["userId"] != me || decode(t, body)["user"] == nil {
			t.Fatalf("%d %s", code, body)
		}
		if e.count(`select count(*) from member where user_id = $1`, me) != 0 || e.activeOrg(memberTok) != nil {
			t.Error("leave did not delete membership / clear active org")
		}
		e.want(400, orgErr("MEMBER_NOT_FOUND"))(e.post("leave", memberTok, `{"organizationId":"`+org+`"}`))
		e.want(400, `{"message":"[body.organizationId] Invalid input: expected string, received undefined","code":"VALIDATION_ERROR"}`+"\n")(e.post("leave", memberTok, `{}`))
		e.want(401, unauthBody)(e.post("leave", "", `{"organizationId":"x"}`))
	})
}

func TestOrgInvitations(t *testing.T) {
	e, fm := newOrgEnv(t)
	ctx := context.Background()

	t.Run("invite-member", func(t *testing.T) {
		e := e.on(t)
		org := e.org("inv-" + dbx.ID())
		owner, ownerTok := e.joined(org, "owner")
		e.pool.Exec(ctx, `update "user" set name = '' where id = $1`, owner)
		_, adminTok := e.joined(org, "admin")
		_, memberTok := e.joined(org, "member")
		outsider := e.orgUser()
		outTok := e.session(outsider, time.Minute, week, &org)

		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_INVITE_USERS_TO_THIS_ORGANIZATION"))(e.post("invite-member", memberTok, `{"email":"a@b.co","role":"member"}`))
		e.want(400, orgErr("MEMBER_NOT_FOUND"))(e.post("invite-member", outTok, `{"email":"a@b.co","role":"member"}`))
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_INVITE_USER_WITH_THIS_ROLE"))(e.post("invite-member", adminTok, `{"email":"a@b.co","role":"owner"}`))
		e.want(400, `{"message":"Role not found: boss, chief"}`+"\n")(e.post("invite-member", ownerTok, `{"email":"a@b.co","role":["member","boss","chief"]}`))
		e.want(400, orgErr("INVALID_EMAIL"))(e.post("invite-member", ownerTok, `{"email":"not-an-email","role":"member"}`))
		e.want(400, orgErr("USER_IS_ALREADY_A_MEMBER_OF_THIS_ORGANIZATION"))(e.post("invite-member", ownerTok, `{"email":"`+email(owner)+`","role":"member"}`))
		if len(fm.sent) != 0 {
			t.Fatalf("mail sent on failure: %v", fm.sent)
		}

		code, body := e.post("invite-member", ownerTok, `{"email":"New.Person@Example.com","role":"admin","organizationId":"`+org+`","resend":false}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		inv := decode(t, body)
		id := inv["id"].(string)
		if inv["email"] != "new.person@example.com" || inv["status"] != "pending" || inv["role"] != "admin" || inv["inviterId"] != owner {
			t.Errorf("got %s", body)
		}
		var exp time.Time
		e.pool.QueryRow(ctx, `select expires_at from invitation where id = $1 and status = 'pending'`, id).Scan(&exp)
		if d := time.Until(exp); d < week-time.Minute || d > week {
			t.Errorf("expires in %v", d)
		}
		m := fm.last()
		wantURL := "https://org.test/invite/accept?id=" + id
		if m.to != "new.person@example.com" || !strings.Contains(m.text, "Accept: "+wantURL) ||
			m.subject != email(owner)+" invited you to join Sample Name on Hectix" || !strings.Contains(m.text, "as Admin.") {
			t.Errorf("mail %+v", m)
		}

		e.want(400, orgErr("USER_IS_ALREADY_INVITED_TO_THIS_ORGANIZATION"))(e.post("invite-member", adminTok, `{"email":"new.person@example.com","role":"member"}`))

		e.pool.Exec(ctx, `update invitation set expires_at = now() + interval '1 hour' where id = $1`, id)
		n := len(fm.sent)
		code, body = e.post("invite-member", adminTok, `{"email":"new.person@example.com","role":"member","resend":true}`)
		if code != 200 || decode(t, body)["id"] != id || decode(t, body)["role"] != "admin" {
			t.Fatalf("%d %s", code, body)
		}
		e.pool.QueryRow(ctx, `select expires_at from invitation where id = $1`, id).Scan(&exp)
		if d := time.Until(exp); d < week-time.Minute {
			t.Errorf("resend did not extend expiry: %v", d)
		}
		if len(fm.sent) != n+1 || !strings.Contains(fm.last().text, "as Admin.") {
			t.Errorf("resend mail %+v", fm.last())
		}
		if e.count(`select count(*) from invitation where organization_id = $1`, org) != 1 {
			t.Error("resend inserted a new invitation")
		}

		fm.err = errors.New("resend down")
		defer func() { fm.err = nil }()
		code, _ = e.post("invite-member", ownerTok, `{"email":"fail@example.com","role":"member"}`)
		if code != 500 || e.count(`select count(*) from invitation where email = 'fail@example.com' and organization_id = $1`, org) != 1 {
			t.Errorf("send failure: code %d", code)
		}
		e.want(400, `{"message":"[body.email] Invalid input: expected string, received undefined; [body.role] Invalid input","code":"VALIDATION_ERROR"}`+"\n")(
			e.post("invite-member", ownerTok, `{}`))
		e.want(401, unauthBody)(e.post("invite-member", "", `{"email":"a@b.co","role":"member"}`))
	})

	t.Run("cancel-invitation", func(t *testing.T) {
		e := e.on(t)
		org := e.org("ci-" + dbx.ID())
		owner, _ := e.joined(org, "owner")
		_, adminTok := e.joined(org, "admin")
		_, memberTok := e.joined(org, "member")
		inv := e.invitation(org, owner, "c@example.com", "member", "pending", week)
		e.want(403, orgErr("YOU_ARE_NOT_ALLOWED_TO_CANCEL_THIS_INVITATION"))(e.post("cancel-invitation", memberTok, `{"invitationId":"`+inv+`"}`))
		e.want(400, orgErr("INVITATION_NOT_FOUND"))(e.post("cancel-invitation", adminTok, `{"invitationId":"nope"}`))
		code, body := e.post("cancel-invitation", adminTok, `{"invitationId":"`+inv+`"}`)
		if code != 200 || decode(t, body)["status"] != "canceled" {
			t.Errorf("%d %s", code, body)
		}
		if e.count(`select count(*) from invitation where id = $1 and status = 'canceled'`, inv) != 1 {
			t.Error("not canceled")
		}
	})

	t.Run("accept-invitation", func(t *testing.T) {
		e := e.on(t)
		org := e.org("ai-" + dbx.ID())
		owner, _ := e.joined(org, "owner")
		invitee := e.orgUser()
		tok := e.session(invitee, time.Minute, week, nil)
		other := e.orgUser()
		otherTok := e.session(other, time.Minute, week, nil)
		upper := strings.ToUpper(invitee) + "@EXAMPLE.com"

		inv := e.invitation(org, owner, upper, "admin", "pending", week)
		e.want(403, orgErr("YOU_ARE_NOT_THE_RECIPIENT_OF_THE_INVITATION"))(e.post("accept-invitation", otherTok, `{"invitationId":"`+inv+`"}`))
		expired := e.invitation(org, owner, upper, "admin", "pending", -time.Minute)
		e.want(400, orgErr("INVITATION_NOT_FOUND"))(e.post("accept-invitation", tok, `{"invitationId":"`+expired+`"}`))
		canceled := e.invitation(org, owner, upper, "admin", "canceled", week)
		e.want(400, orgErr("INVITATION_NOT_FOUND"))(e.post("accept-invitation", tok, `{"invitationId":"`+canceled+`"}`))
		e.want(400, orgErr("INVITATION_NOT_FOUND"))(e.post("accept-invitation", tok, `{"invitationId":"nope"}`))

		code, body := e.post("accept-invitation", tok, `{"invitationId":"`+inv+`"}`)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
		m := decode(t, body)
		if m["invitation"].(map[string]any)["status"] != "accepted" || m["member"].(map[string]any)["role"] != "admin" ||
			m["member"].(map[string]any)["userId"] != invitee {
			t.Errorf("got %s", body)
		}
		if e.count(`select count(*) from member where organization_id = $1 and user_id = $2 and role = 'admin'`, org, invitee) != 1 {
			t.Error("member not inserted")
		}
		if a := e.activeOrg(tok); a == nil || *a != org {
			t.Errorf("active org = %v", a)
		}
		e.want(400, orgErr("INVITATION_NOT_FOUND"))(e.post("accept-invitation", tok, `{"invitationId":"`+inv+`"}`))
	})
}
