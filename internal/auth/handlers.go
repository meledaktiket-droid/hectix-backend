package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/httpx"
)

func (a *Auth) Routes(r chi.Router) {
	r.Get("/api/auth/get-session", a.getSession)
	r.Post("/api/auth/sign-out", a.signOut)
	r.Post("/api/auth/sign-in/social", a.signInSocial)
	r.Get("/api/auth/callback/google", a.callbackGoogle)
	r.Group(func(r chi.Router) {
		r.Use(requiredAuthAPI)
		r.Get("/api/auth/list-sessions", a.listSessions)
		r.Post("/api/auth/revoke-other-sessions", a.revokeOtherSessions)
	})
	r.Post("/api/auth/revoke-session", a.revokeSession)
	r.Post("/api/auth/update-user", a.updateUser)
}

func authError(w http.ResponseWriter, status int, code, message string) {
	httpx.JSON(w, status, struct {
		Message string `json:"message"`
		Code    string `json:"code,omitempty"`
	}{message, code})
}

func unauthorized(w http.ResponseWriter) {
	authError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Unauthorized")
}

func failedToGetSession(w http.ResponseWriter) {
	authError(w, http.StatusInternalServerError, "FAILED_TO_GET_SESSION", "Failed to get session")
}

func authUser(w http.ResponseWriter, r *http.Request) (*User, bool) {
	if loadErr(r.Context()) != nil {
		failedToGetSession(w)
		return nil, false
	}
	u := UserFrom(r.Context())
	if u == nil {
		unauthorized(w)
	}
	return u, u != nil
}

func requiredAuthAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authUser(w, r); ok {
			next.ServeHTTP(w, r)
		}
	})
}

func validationError(w http.ResponseWriter, message string) {
	authError(w, http.StatusBadRequest, "VALIDATION_ERROR", message)
}

func zodType(raw json.RawMessage, present bool) string {
	if !present {
		return "undefined"
	}
	switch b := bytes.TrimSpace(raw); {
	case len(b) == 0:
		return "undefined"
	case b[0] == '"':
		return "string"
	case b[0] == '{':
		return "object"
	case b[0] == '[':
		return "array"
	case b[0] == 't' || b[0] == 'f':
		return "boolean"
	case b[0] == 'n':
		return "null"
	}
	return "number"
}

func decodeObject(r *http.Request) (map[string]json.RawMessage, string) {
	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err != nil || !json.Valid(raw) {
		return nil, "undefined"
	}
	if t := zodType(raw, true); t != "object" {
		return nil, t
	}
	var m map[string]json.RawMessage
	json.Unmarshal(raw, &m)
	return m, ""
}

func serverError(w http.ResponseWriter, where string, err error) {
	log.Printf("auth: %s: %v", where, err)
	authError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "Internal Server Error")
}

func (a *Auth) getSession(w http.ResponseWriter, r *http.Request) {
	if loadErr(r.Context()) != nil {
		failedToGetSession(w)
		return
	}
	s, u := SessionFrom(r.Context()), UserFrom(r.Context())
	if s == nil {
		httpx.JSON(w, http.StatusOK, nil)
		return
	}
	app := httpx.AppFrom(r.Context()).Name
	httpx.JSON(w, http.StatusOK, map[string]any{"session": sessionView(app, s), "user": userView(app, u)})
}

func (a *Auth) signOut(w http.ResponseWriter, r *http.Request) {
	if token, ok := a.cookieToken(r); ok {
		if _, err := a.DB.Exec(r.Context(), `delete from session where token = $1`, token); err != nil {
			log.Printf("auth: sign-out: %v", err)
		}
	}
	a.clearSessionCookie(w, r)
	httpx.JSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (a *Auth) listSessions(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.Query(r.Context(), `select `+sessionCols+` from session s
		where s.user_id = $1 and s.expires_at > $2 order by s.created_at`,
		UserFrom(r.Context()).ID, time.Now().UTC())
	if err != nil {
		serverError(w, "list-sessions", err)
		return
	}
	defer rows.Close()
	app := httpx.AppFrom(r.Context()).Name
	out := []any{}
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			serverError(w, "list-sessions", err)
			return
		}
		out = append(out, sessionView(app, s))
	}
	if err := rows.Err(); err != nil {
		serverError(w, "list-sessions", err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (a *Auth) revokeSession(w http.ResponseWriter, r *http.Request) {
	body, bad := decodeObject(r)
	if bad != "" {
		validationError(w, "[body] Invalid input: expected object, received "+bad)
		return
	}
	var token string
	rawToken, ok := body["token"]
	if !ok || json.Unmarshal(rawToken, &token) != nil || zodType(rawToken, true) != "string" {
		validationError(w, "[body.token] Invalid input: expected string, received "+zodType(rawToken, ok))
		return
	}
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	if _, err := a.DB.Exec(r.Context(), `delete from session where token = $1 and user_id = $2`,
		token, user.ID); err != nil {
		serverError(w, "revoke-session", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"status": true})
}

func (a *Auth) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := a.DB.Exec(ctx, `delete from session where user_id = $1 and token <> $2`,
		UserFrom(ctx).ID, SessionFrom(ctx).Token); err != nil {
		serverError(w, "revoke-other-sessions", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"status": true})
}

func (a *Auth) updateUser(w http.ResponseWriter, r *http.Request) {
	body, bad := decodeObject(r)
	if bad != "" {
		validationError(w, "[body] Invalid input: expected record, received "+bad)
		return
	}
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	if raw, ok := body["email"]; ok && truthy(raw) {
		authError(w, http.StatusBadRequest, "EMAIL_CAN_NOT_BE_UPDATED", "Email can not be updated")
		return
	}
	rawName, hasName := body["name"]
	hasName = hasName && string(bytes.TrimSpace(rawName)) != "null"
	rawImage, hasImage := body["image"]
	if !hasName && !hasImage {
		authError(w, http.StatusBadRequest, "", "No fields to update")
		return
	}
	var name string
	if hasName && json.Unmarshal(rawName, &name) != nil {
		serverError(w, "update-user", errors.New("name is not a string"))
		return
	}
	var image *string
	if hasImage && json.Unmarshal(rawImage, &image) != nil {
		serverError(w, "update-user", errors.New("image is not a string or null"))
		return
	}
	_, err := a.DB.Exec(r.Context(), `update "user" set
		name = case when $2 then $3 else name end,
		image = case when $4 then $5 else image end,
		updated_at = $6
		where id = $1`,
		user.ID, hasName, name, hasImage, image, time.Now().UTC())
	if err != nil {
		serverError(w, "update-user", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"status": true})
}

func truthy(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", "0", "-0", `""`:
		return false
	}
	return true
}
