package scanner

import (
	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/deps"
)

func Mount(r chi.Router, d deps.Deps) {
	h := &handlers{db: d.DB}
	r.Get("/api/scanner/events", h.listEvents)
	r.Get("/api/scanner/sessions", h.listSessions)
	r.Post("/api/scanner/lookup-eticket", h.scannerLookupEticket)
	r.Post("/api/scanner/redeem", h.scannerRedeem)
	r.Post("/api/scanner/ots", h.scannerOTS)
	r.Post("/api/scanner/gate-in", h.scannerGateIn)
	r.Post("/api/scanner/gate-out", h.scannerGateOut)
	r.Get("/api/scanner/physical-tickets", h.scannerPhysicalTickets)
	r.Get("/api/scanner/etickets", h.scannerEtickets)
	r.Post("/api/scanner/supervisor/replace", h.scannerReplaceWristband)
	r.Get("/api/scanner/reconciliation", h.scannerReconciliation)
	r.Get("/api/scanner/reconciliation/export", h.scannerExportReconciliation)
}
