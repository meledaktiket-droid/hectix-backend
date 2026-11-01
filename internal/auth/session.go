package auth

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/config"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
)

const (
	ExpiresIn = 7 * 24 * time.Hour
	UpdateAge = 24 * time.Hour
)

type Auth struct {
	DB                 *pgxpool.Pool
	Secret             string
	GoogleClientID     string
	GoogleClientSecret string

	GoogleAuthURL     string
	GoogleTokenURL    string
	GoogleUserinfoURL string

	Mail mail.Sender
}

type User struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Email         string     `json:"email"`
	EmailVerified bool       `json:"emailVerified"`
	Image         *string    `json:"image"`
	Role          string     `json:"-"`
	CreatedAt     httpx.Time `json:"createdAt"`
	UpdatedAt     httpx.Time `json:"updatedAt"`
}

type Session struct {
	ID                   string     `json:"id"`
	ExpiresAt            httpx.Time `json:"expiresAt"`
	Token                string     `json:"token"`
	CreatedAt            httpx.Time `json:"createdAt"`
	UpdatedAt            httpx.Time `json:"updatedAt"`
	IPAddress            *string    `json:"ipAddress"`
	UserAgent            *string    `json:"userAgent"`
	UserID               string     `json:"userId"`
	ActiveOrganizationID *string    `json:"-"`
}

func sessionView(app string, s *Session) any {
	if app == "organizer" {
		return struct {
			*Session
			ActiveOrganizationID *string `json:"activeOrganizationId"`
		}{s, s.ActiveOrganizationID}
	}
	return s
}

func userView(app string, u *User) any {
	if app == "admin" {
		return struct {
			*User
			Role string `json:"role"`
		}{u, u.Role}
	}
	return u
}

type ctxKey struct{}

type current struct {
	session *Session
	user    *User
	err     error
}

func loadErr(ctx context.Context) error {
	c, _ := ctx.Value(ctxKey{}).(*current)
	if c == nil {
		return nil
	}
	return c.err
}

func LoadError(ctx context.Context) error { return loadErr(ctx) }

func WithUser(ctx context.Context, u *User, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, &current{session: s, user: u})
}

func WithLoadError(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, ctxKey{}, &current{err: err})
}

func UserFrom(ctx context.Context) *User {
	c, _ := ctx.Value(ctxKey{}).(*current)
	if c == nil {
		return nil
	}
	return c.user
}

func SessionFrom(ctx context.Context) *Session {
	c, _ := ctx.Value(ctxKey{}).(*current)
	if c == nil {
		return nil
	}
	return c.session
}

func sessionCookieName(app config.App) string {
	return CookieName(app.Name, app.Secure, "session_token")
}

func (a *Auth) cookieToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookieName(httpx.AppFrom(r.Context())))
	if err != nil || c.Value == "" {
		return "", false
	}
	return Unsign(c.Value, a.Secret)
}

const sessionCols = `s.id, s.expires_at, s.token, s.created_at, s.updated_at, s.ip_address, s.user_agent, s.user_id, s.active_organization_id`

func scanSession(row pgx.Row, extra ...any) (*Session, error) {
	s := &Session{}
	dest := append([]any{&s.ID, &s.ExpiresAt, &s.Token, &s.CreatedAt, &s.UpdatedAt,
		&s.IPAddress, &s.UserAgent, &s.UserID, &s.ActiveOrganizationID}, extra...)
	return s, row.Scan(dest...)
}

func (a *Auth) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := a.cookieToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		ctx := r.Context()
		now := time.Now().UTC()
		u := &User{}
		s, err := scanSession(a.DB.QueryRow(ctx, `select `+sessionCols+`,
			       u.id, u.name, u.email, u.email_verified, u.image, u.role, u.created_at, u.updated_at
			from session s join "user" u on u.id = s.user_id
			where s.token = $1 and s.expires_at > $2`, token, now),
			&u.ID, &u.Name, &u.Email, &u.EmailVerified, &u.Image, &u.Role, &u.CreatedAt, &u.UpdatedAt)
		if err == pgx.ErrNoRows {
			if _, err := a.DB.Exec(ctx, `delete from session where token = $1 and expires_at <= $2`, token, now); err != nil {
				log.Printf("auth: delete expired session: %v", err)
			}
			a.clearSessionCookie(w, r)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			log.Printf("auth: load session: %v", err)
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, &current{err: err})))
			return
		}
		if s.ExpiresAt.Sub(now) <= ExpiresIn-UpdateAge {
			exp := now.Add(ExpiresIn)
			tag, err := a.DB.Exec(ctx, `update session set expires_at = $1, updated_at = $2 where token = $3`, exp, now, token)
			switch {
			case err != nil:
				log.Printf("auth: extend session: %v", err)
			case tag.RowsAffected() == 0:
				a.clearSessionCookie(w, r)
				next.ServeHTTP(w, r)
				return
			default:
				s.ExpiresAt, s.UpdatedAt = httpx.Time{Time: exp}, httpx.Time{Time: now}
				a.SetSessionCookie(w, r, s)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, ctxKey{}, &current{session: s, user: u})))
	})
}

func Required(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loadErr(r.Context()) != nil {
			httpx.Error(w, http.StatusInternalServerError, "Internal server error")
			return
		}
		if UserFrom(r.Context()) == nil {
			httpx.Error(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) CreateSession(ctx context.Context, userID, ip, ua string, activeOrg *string) (*Session, error) {
	return createSession(ctx, a.DB, userID, ip, ua, activeOrg)
}

func createSession(ctx context.Context, db dbx.DB, userID, ip, ua string, activeOrg *string) (*Session, error) {
	now := time.Now().UTC()
	s := &Session{
		ID: dbx.ID(), Token: dbx.ID(), UserID: userID, ActiveOrganizationID: activeOrg,
		ExpiresAt: httpx.Time{Time: now.Add(ExpiresIn)},
		CreatedAt: httpx.Time{Time: now}, UpdatedAt: httpx.Time{Time: now},
	}
	if ip != "" {
		s.IPAddress = &ip
	}
	if ua != "" {
		s.UserAgent = &ua
	}
	_, err := db.Exec(ctx, `insert into session
		(id, expires_at, token, created_at, updated_at, ip_address, user_agent, user_id, active_organization_id)
		values ($1, $2, $3, $4, $4, $5, $6, $7, $8)`,
		s.ID, s.ExpiresAt.Time, s.Token, now, s.IPAddress, s.UserAgent, s.UserID, s.ActiveOrganizationID)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (a *Auth) SetSessionCookie(w http.ResponseWriter, r *http.Request, s *Session) {
	app := httpx.AppFrom(r.Context())
	replaceCookie(w, &http.Cookie{
		Name: sessionCookieName(app), Value: Sign(s.Token, a.Secret),
		Path: "/", MaxAge: int(ExpiresIn / time.Second),
		HttpOnly: true, Secure: app.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	app := httpx.AppFrom(r.Context())
	replaceCookie(w, &http.Cookie{
		Name: sessionCookieName(app), Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: app.Secure, SameSite: http.SameSiteLaxMode,
	})
}

func replaceCookie(w http.ResponseWriter, c *http.Cookie) {
	h := w.Header()
	kept := h["Set-Cookie"][:0]
	for _, v := range h["Set-Cookie"] {
		if !strings.HasPrefix(v, c.Name+"=") {
			kept = append(kept, v)
		}
	}
	h["Set-Cookie"] = kept
	http.SetCookie(w, c)
}
