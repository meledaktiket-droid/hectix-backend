package httpx

import (
	"context"
	"encoding/json"
	"net/http"

	"hectix/backend/internal/config"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func Error(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

func Decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

type ctxKey struct{}

func AppFrom(ctx context.Context) config.App {
	a, _ := ctx.Value(ctxKey{}).(config.App)
	return a
}

func WithApp(ctx context.Context, a config.App) context.Context {
	return context.WithValue(ctx, ctxKey{}, a)
}

func ByHost(apps map[string]config.App, routers map[string]http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app, ok := apps[r.Host]
		if !ok {
			http.NotFound(w, r)
			return
		}
		routers[app.Name].ServeHTTP(w, r.WithContext(WithApp(r.Context(), app)))
	})
}
