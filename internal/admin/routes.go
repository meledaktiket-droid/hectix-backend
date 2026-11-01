package admin

import (
	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/deps"
)

const AcceptPath = "/api/team/invitations/accept"

func Mount(r chi.Router, d deps.Deps) {
	h := &handlers{d: d}
	content := r.With(RequireStaff(CanManageContent))

	r.Get("/api/dashboard", h.dashboard)

	r.Get("/api/banners", h.listBanners)
	content.Post("/api/banners", h.createBanner)
	content.Put("/api/banners/{id}", h.updateBanner)
	content.Delete("/api/banners/{id}", h.deleteBanner)

	r.Get("/api/events", h.listEvents)
	r.Get("/api/events/{id}", h.getEvent)

	r.Get("/api/orders", h.listOrders)
	r.Get("/api/orders/export", h.exportOrders)

	r.Get("/api/orgs", h.listOrgs)
	r.Get("/api/orgs/{id}", h.getOrg)
	r.With(RequireStaff(CanVerifyOrgs)).Patch("/api/orgs/{id}", h.patchOrg)

	r.Get("/api/payouts", h.listPayouts)
	r.Get("/api/payouts/{id}", h.getPayout)
	payouts := r.With(RequireStaff(CanManagePayouts))
	payouts.Post("/api/payouts/{id}/process", h.processPayout)
	payouts.Post("/api/payouts/{id}/reject", h.rejectPayout)

	r.Get("/api/users", h.listUsers)
	r.Get("/api/users/{id}", h.getUser)

	r.Get("/api/settings", h.getSettings)
	content.Put("/api/settings", h.putSettings)

	team := r.With(RequireStaff(CanManageTeam))
	team.Get("/api/team/invitations", h.listInvitations)
	team.Post("/api/team/invitations", h.createInvitation)
	team.Post("/api/team/invitations/{id}", h.resendInvitation)
	team.Delete("/api/team/invitations/{id}", h.revokeInvitation)
	team.Get("/api/team/members", h.listMembers)
	team.Patch("/api/team/members/{id}", h.patchMember)
	team.Delete("/api/team/members/{id}", h.deleteMember)
	r.Post(AcceptPath, h.acceptInvitation)
}
