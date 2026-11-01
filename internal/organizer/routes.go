package organizer

import (
	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/deps"
)

func Mount(r chi.Router, d deps.Deps) {
	h := &handlers{db: d.DB, uploadDir: d.Cfg.UploadDir, apps: d.Cfg.Apps}
	r.Get("/api/dashboard", h.dashboard)
	r.Get("/api/events", h.listEvents)
	r.Post("/api/events", h.createEvent)
	r.Get("/api/events/{id}", h.getEvent)
	r.Patch("/api/events/{id}", h.updateEvent)
	r.Delete("/api/events/{id}", h.deleteEvent)
	r.Post("/api/events/{id}/publish", h.publishEvent)
	r.Post("/api/events/{id}/unpublish", h.unpublishEvent)
	r.Post("/api/events/{id}/duplicate", h.duplicateEvent)
	r.Get("/api/events/{id}/tickets", h.listTickets)
	r.Post("/api/events/{id}/tickets", h.createTicket)
	r.Patch("/api/events/{id}/tickets/{ticketId}", h.updateTicket)
	r.Delete("/api/events/{id}/tickets/{ticketId}", h.deleteTicket)
	r.Get("/api/events/{id}/performers", h.listPerformers)
	r.Post("/api/events/{id}/performers", h.createPerformer)
	r.Delete("/api/events/{id}/performers/{performerId}", h.deletePerformer)
	r.Get("/api/events/{id}/vouchers", h.listVouchers)
	r.Post("/api/events/{id}/vouchers", h.createVoucher)
	r.Delete("/api/events/{id}/vouchers", h.deleteVoucher)
	r.Get("/api/events/{id}/orders", h.eventOrders)
	r.Get("/api/orders", h.listOrders)
	r.Get("/api/orders/export", h.exportOrders)
	r.Get("/api/finance", h.finance)
	r.Post("/api/finance", h.requestPayout)
	r.Get("/api/org/payout", h.getPayoutAccount)
	r.Patch("/api/org/payout", h.updatePayoutAccount)
	r.Get("/api/org/status", h.orgStatus)
	r.Patch("/api/org/verify", h.verifyOrg)
	r.Post("/api/org/log-creation", h.logOrgCreation)
	r.Post("/api/invitations/inspect", h.inspectInvite)
}
