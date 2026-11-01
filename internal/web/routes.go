package web

import (
	"context"
	"maps"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/deps"
	"hectix/backend/internal/payments"
	"hectix/backend/internal/ticketpdf"
)

type handlers struct {
	db           *pgxpool.Pool
	cancelCharge func(ctx context.Context, orderID string) error
	banners      ticketpdf.BannerLoader
}

func Mount(r chi.Router, d deps.Deps) {
	mt := &midtrans{ServerKey: d.Cfg.MidtransServerKey, BaseURL: payments.MidtransBaseURL(d.Cfg.MidtransProduction)}
	banners := ticketpdf.LocalBanners(d.Cfg.UploadDir, slices.Collect(maps.Keys(d.Cfg.Apps)))
	mount(r, &handlers{db: d.DB, cancelCharge: mt.Cancel, banners: banners})
}

func mount(r chi.Router, h *handlers) {
	r.Get("/api/config", h.config)
	r.Get("/api/favorites/ids", h.favoriteIDs)
	r.Get("/api/events", h.listEvents)
	r.Get("/api/events/published", h.publishedEvents)
	r.Get("/api/banners", h.activeBanners)
	r.Get("/api/events/{id}", h.getEvent)
	r.Get("/api/search", h.search)
	r.Get("/api/events/{id}/comments", h.listComments)
	r.Post("/api/vouchers/validate", h.validateVoucher)
	r.Get("/api/histori/{orderId}/ticket.pdf", h.ticketPDF)

	r.Group(func(r chi.Router) {
		r.Use(auth.Required)
		r.Get("/api/favorites", h.listFavorites)
		r.Post("/api/favorites", h.addFavorite)
		r.Delete("/api/favorites/{eventId}", h.removeFavorite)
		r.Post("/api/events/{id}/comments", h.addComment)
		r.Get("/api/orders/{orderId}", h.getOrder)
		r.Delete("/api/orders/{orderId}", h.cancelOrder)
		r.Get("/api/histori", h.histori)
		r.Get("/api/histori/{orderId}", h.historiDetail)
		r.Get("/api/profile", h.profile)
		r.Patch("/api/profile", h.updateProfile)
		r.Get("/api/profile/activity", h.profileActivity)
		r.Get("/api/profile/ticket/{orderId}", h.profileTicket)
	})
}
