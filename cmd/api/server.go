package main

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	adminroles "hectix/backend/internal/admin"
	"hectix/backend/internal/auth"
	"hectix/backend/internal/config"
	"hectix/backend/internal/deps"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
	"hectix/backend/internal/orders"
	organizerroutes "hectix/backend/internal/organizer"
	"hectix/backend/internal/payments"
	"hectix/backend/internal/uploads"
	webroutes "hectix/backend/internal/web"
)

func newServer(cfg config.Config, pool *pgxpool.Pool) http.Handler {
	a := &auth.Auth{
		DB: pool, Secret: cfg.AuthSecret,
		GoogleClientID: cfg.GoogleClientID, GoogleClientSecret: cfg.GoogleClientSecret,
		Mail: &mail.Mailer{APIKey: cfg.ResendAPIKey, From: cfg.MailFrom},
	}
	up := &uploads.Uploads{Dir: cfg.UploadDir, Secret: cfg.AuthSecret, DB: pool, IsStaff: adminroles.IsAdminRole}
	up.CleanStaleTemp()
	web, organizer, admin := chi.NewRouter(), chi.NewRouter(), chi.NewRouter()
	for _, r := range []chi.Router{web, organizer, admin} {
		r.Use(auth.OriginCheck, a.Load)
		if r == chi.Router(admin) {
			r.Use(staffOnly)
		}
		a.Routes(r)
		r.Put("/api/upload/put", up.Put)
		r.Group(func(r chi.Router) {
			r.Use(auth.Required)
			r.Post("/api/upload", up.Upload)
			r.Get("/api/files/*", up.File)
		})
	}

	a.OrgRoutes(organizer)

	d := deps.Deps{DB: pool, Cfg: cfg, Mail: a.Mail}
	webroutes.Mount(web, d)
	organizerroutes.Mount(organizer, d)
	adminroles.Mount(admin, d)

	web.Method(http.MethodPost, "/api/midtrans", &payments.Webhook{DB: pool, ServerKey: cfg.MidtransServerKey})
	checkout := &orders.Checkout{DB: pool, Midtrans: &payments.Midtrans{
		ServerKey: cfg.MidtransServerKey,
		BaseURL:   payments.MidtransBaseURL(cfg.MidtransProduction),
	}}
	web.Group(func(r chi.Router) {
		r.Use(auth.Required)
		r.Post("/api/orders", checkout.Create)
	})

	root := http.NewServeMux()
	root.HandleFunc("/healthz", healthz)
	root.Handle("/", httpx.ByHost(cfg.Apps, map[string]http.Handler{
		"web": web, "organizer": organizer, "admin": admin,
	}))
	root.Handle("/uploads/", http.StripPrefix("/uploads/", publicFiles(http.Dir(filepath.Join(cfg.UploadDir, "public")))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("X-Forwarded-Host"); h != "" {
			r.Host = h
		}
		root.ServeHTTP(w, r)
	})
}

func publicFiles(dir http.FileSystem) http.Handler {
	fs := http.FileServer(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "" || strings.HasSuffix(p, "/") || strings.HasPrefix(p, ".") || strings.Contains(p, "/.") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fs.ServeHTTP(w, r)
	})
}
