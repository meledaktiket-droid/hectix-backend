package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"hectix/backend/internal/config"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

const (
	googleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleUserinfoURL = "https://openidconnect.googleapis.com/v1/userinfo"
	stateTTL          = 10 * time.Minute
	stateCookieMaxAge = 300
)

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func (a *Auth) googleConfig(app config.App) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.GoogleClientID,
		ClientSecret: a.GoogleClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:   or(a.GoogleAuthURL, googleAuthURL),
			TokenURL:  or(a.GoogleTokenURL, googleTokenURL),
			AuthStyle: oauth2.AuthStyleInParams,
		},
		RedirectURL: app.BaseURL + "/api/auth/callback/google",
		Scopes:      []string{"email", "profile", "openid"},
	}
}

var relativePath = regexp.MustCompile(`^/[\w\-.+/@]*(?:\?[\w\-.+/=&%@]*)?$`)

func safeRedirect(target, baseURL string) bool {
	if strings.HasPrefix(target, "/") {
		rest := strings.ToLower(target[1:])
		for _, p := range []string{"/", `\`, "%2f", "%5c"} {
			if strings.HasPrefix(rest, p) {
				return false
			}
		}
		return relativePath.MatchString(target)
	}
	return sameOrigin(target, baseURL)
}

type stateData struct {
	CallbackURL  string `json:"callbackURL"`
	CodeVerifier string `json:"codeVerifier"`
	ErrorURL     string `json:"errorURL,omitempty"`
	NewUserURL   string `json:"newUserURL,omitempty"`
	ExpiresAt    int64  `json:"expiresAt"`
}

func (a *Auth) signInSocial(w http.ResponseWriter, r *http.Request) {
	app := httpx.AppFrom(r.Context())
	body, bad := decodeObject(r)
	if bad != "" {
		validationError(w, "[body] Invalid input: expected object, received "+bad)
		return
	}
	str := func(k string) string {
		var s string
		json.Unmarshal(body[k], &s)
		return s
	}
	for _, f := range []struct{ key, code, msg string }{
		{"callbackURL", "INVALID_CALLBACK_URL", "Invalid callbackURL"},
		{"errorCallbackURL", "INVALID_ERROR_CALLBACK_URL", "Invalid errorCallbackURL"},
		{"newUserCallbackURL", "INVALID_NEW_USER_CALLBACK_URL", "Invalid newUserCallbackURL"},
	} {
		if v := str(f.key); v != "" && !safeRedirect(v, app.BaseURL) {
			authError(w, http.StatusForbidden, f.code, f.msg)
			return
		}
	}
	provider := str("provider")
	if provider == "" {
		validationError(w, "[body.provider] Invalid input")
		return
	}
	if provider != "google" {
		authError(w, http.StatusNotFound, "PROVIDER_NOT_FOUND", "Provider not found")
		return
	}
	var disableRedirect bool
	json.Unmarshal(body["disableRedirect"], &disableRedirect)

	now := time.Now().UTC()
	state, verifier := dbx.ID(), oauth2.GenerateVerifier()
	sd, _ := json.Marshal(stateData{
		CallbackURL: or(str("callbackURL"), app.BaseURL), CodeVerifier: verifier,
		ErrorURL: str("errorCallbackURL"), NewUserURL: str("newUserCallbackURL"),
		ExpiresAt: now.Add(stateTTL).UnixMilli(),
	})
	if _, err := a.DB.Exec(r.Context(), `insert into verification (id, identifier, value, expires_at, created_at, updated_at)
		values ($1, $2, $3, $4, $5, $5)`, dbx.ID(), state, string(sd), now.Add(stateTTL), now); err != nil {
		serverError(w, "sign-in/social", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName(app.Name, app.Secure, "state"), Value: Sign(state, a.Secret),
		Path: "/", MaxAge: stateCookieMaxAge, HttpOnly: true, Secure: app.Secure, SameSite: http.SameSiteLaxMode,
	})
	authURL := a.googleConfig(app).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"))
	if !disableRedirect {
		w.Header().Set("Location", authURL)
	}
	httpx.JSON(w, http.StatusOK, struct {
		URL      string `json:"url"`
		Redirect bool   `json:"redirect"`
	}{authURL, !disableRedirect})
}

func redirectWith(w http.ResponseWriter, r *http.Request, base string, params url.Values) {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	http.Redirect(w, r, base+sep+params.Encode(), http.StatusFound)
}

type googleUser struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
}

var errNotLinked = errors.New("account_not_linked")

func (a *Auth) callbackGoogle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	app := httpx.AppFrom(ctx)
	q := r.URL.Query()
	defaultErr := app.BaseURL
	state := q.Get("state")
	if state == "" {
		redirectWith(w, r, defaultErr, url.Values{"state": {"state_not_found"}})
		return
	}
	stateCookie := CookieName(app.Name, app.Secure, "state")
	c, err := r.Cookie(stateCookie)
	if err != nil {
		redirectWith(w, r, defaultErr, url.Values{"error": {"state_mismatch"}})
		return
	}
	if v, ok := Unsign(c.Value, a.Secret); !ok || v != state {
		redirectWith(w, r, defaultErr, url.Values{"error": {"state_mismatch"}})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: app.Secure, SameSite: http.SameSiteLaxMode})

	now := time.Now().UTC()
	var raw string
	err = a.DB.QueryRow(ctx, `delete from verification where identifier = $1 and expires_at > $2 returning value`, state, now).Scan(&raw)
	var sd stateData
	if err == nil {
		err = json.Unmarshal([]byte(raw), &sd)
	}
	if err != nil || sd.CallbackURL == "" || sd.CodeVerifier == "" || sd.ExpiresAt < now.UnixMilli() {
		if err != nil && err != pgx.ErrNoRows {
			log.Printf("auth: oauth state: %v", err)
		}
		redirectWith(w, r, defaultErr, url.Values{"error": {"please_restart_the_process"}})
		return
	}
	errURL := or(sd.ErrorURL, defaultErr)
	fail := func(code string) { redirectWith(w, r, errURL, url.Values{"error": {code}}) }

	if e := q.Get("error"); e != "" {
		p := url.Values{"error": {e}}
		if d := q.Get("error_description"); d != "" {
			p.Set("error_description", d)
		}
		redirectWith(w, r, errURL, p)
		return
	}
	code := q.Get("code")
	if code == "" {
		fail("no_code")
		return
	}
	xctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conf := a.googleConfig(app)
	tok, err := conf.Exchange(xctx, code, oauth2.VerifierOption(sd.CodeVerifier))
	if err != nil {
		log.Printf("auth: google exchange: %v", err)
		fail("invalid_code")
		return
	}
	gu, err := a.googleUserinfo(xctx, conf, tok)
	if err != nil {
		log.Printf("auth: google userinfo: %v", err)
		fail("unable_to_get_user_info")
		return
	}
	if gu.Email == "" {
		fail("email_not_found")
		return
	}

	var s *Session
	var isNew bool
	err = dbx.Tx(ctx, a.DB, func(tx pgx.Tx) error {
		userID, created, err := upsertGoogleUser(ctx, tx, gu, tok)
		if err != nil {
			return err
		}
		isNew = created
		var activeOrg *string
		if app.Name == "organizer" {
			err := tx.QueryRow(ctx, `select organization_id from member where user_id = $1 order by created_at asc limit 1`, userID).Scan(&activeOrg)
			if err != nil && err != pgx.ErrNoRows {
				return err
			}
		}
		s, err = createSession(ctx, tx, userID, clientIP(r), r.UserAgent(), activeOrg)
		return err
	})
	if errors.Is(err, errNotLinked) {
		fail("account_not_linked")
		return
	}
	if err != nil {
		log.Printf("auth: google sign-in: %v", err)
		fail("unable_to_create_user")
		return
	}
	a.SetSessionCookie(w, r, s)
	to := sd.CallbackURL
	if isNew && sd.NewUserURL != "" {
		to = sd.NewUserURL
	}
	http.Redirect(w, r, to, http.StatusFound)
}

func (a *Auth) googleUserinfo(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token) (*googleUser, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", or(a.GoogleUserinfoURL, googleUserinfoURL), nil)
	res, err := conf.Client(ctx, tok).Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("userinfo: " + res.Status)
	}
	var gu googleUser
	if err := json.NewDecoder(res.Body).Decode(&gu); err != nil {
		return nil, err
	}
	if gu.Sub == "" {
		return nil, errors.New("userinfo: no sub")
	}
	return &gu, nil
}

func upsertGoogleUser(ctx context.Context, tx pgx.Tx, gu *googleUser, tok *oauth2.Token) (userID string, created bool, err error) {
	now := time.Now().UTC()
	email := strings.ToLower(gu.Email)
	nilIfEmpty := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	idToken, _ := tok.Extra("id_token").(string)
	scopeRaw, _ := tok.Extra("scope").(string)
	var scope *string
	if f := strings.Fields(scopeRaw); len(f) > 0 {
		scope = nilIfEmpty(strings.Join(f, ","))
	}
	var expires *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry.UTC()
		expires = &e
	}
	markVerified := func(id string) error {
		if !gu.EmailVerified {
			return nil
		}
		_, err := tx.Exec(ctx, `update "user" set email_verified = true, updated_at = $3
			where id = $1 and lower(email) = $2 and not email_verified`, id, email, now)
		return err
	}

	var accountID string
	err = tx.QueryRow(ctx, `select a.id, a.user_id from account a join "user" u on u.id = a.user_id
		where a.provider_id = 'google' and a.account_id = $1 limit 1`, gu.Sub).Scan(&accountID, &userID)
	switch {
	case err == nil:
		_, err = tx.Exec(ctx, `update account set
			access_token = coalesce($2, access_token), refresh_token = coalesce($3, refresh_token),
			id_token = coalesce($4, id_token), access_token_expires_at = coalesce($5, access_token_expires_at),
			scope = coalesce($6, scope), updated_at = $7
			where id = $1`, accountID, nilIfEmpty(tok.AccessToken), nilIfEmpty(tok.RefreshToken), nilIfEmpty(idToken), expires, scope, now)
		if err == nil {
			err = markVerified(userID)
		}
		return userID, false, err
	case err != pgx.ErrNoRows:
		return "", false, err
	}

	err = tx.QueryRow(ctx, `select id from "user" where lower(email) = $1`, email).Scan(&userID)
	switch {
	case err == nil:
		if !gu.EmailVerified {
			return "", false, errNotLinked
		}
	case err == pgx.ErrNoRows:
		userID, created = dbx.ID(), true
		_, err = tx.Exec(ctx, `insert into "user" (id, name, email, email_verified, image, created_at, updated_at)
			values ($1, $2, $3, $4, $5, $6, $6)`, userID, gu.Name, email, gu.EmailVerified, nilIfEmpty(gu.Picture), now)
		if err != nil {
			return "", false, err
		}
	default:
		return "", false, err
	}
	_, err = tx.Exec(ctx, `insert into account (id, account_id, provider_id, user_id, access_token, refresh_token,
		id_token, access_token_expires_at, scope, created_at, updated_at)
		values ($1, $2, 'google', $3, $4, $5, $6, $7, $8, $9, $9)`,
		dbx.ID(), gu.Sub, userID, nilIfEmpty(tok.AccessToken), nilIfEmpty(tok.RefreshToken), nilIfEmpty(idToken), expires, scope, now)
	if err == nil && !created {
		err = markVerified(userID)
	}
	return userID, created, err
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
