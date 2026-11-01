# Golden capture status

Captured 2026-10-02 from the TypeScript apps against a local copy of prod data, then PII-scrubbed. Path ids replaced with {param}.

app | method path | status | file
--- | --- | --- | ---
web | GET /api/auth/get-session | 200 | web/get-session.json
web | GET /api/config | 200 | web/routes/config.json
web | GET /api/events | 200 | web/routes/events.json
web | GET /api/events/{id} | 200 | web/routes/events-{id}.json
web | GET /api/events/{id}/comments | 200 | web/routes/events-{id}-comments.json
web | GET /api/favorites | 200 | web/routes/favorites.json
web | GET /api/favorites/ids | 200 | web/routes/favorites-ids.json
web | GET /api/histori | 200 | web/routes/histori.json
web | GET /api/histori/{id} | 200 | web/routes/histori-{orderId}.json
web | GET /api/orders/{id} | 200 | web/routes/orders-{orderId}.json
web | GET /api/profile | 200 | web/routes/profile.json
web | GET /api/profile/activity | 200 | web/routes/profile-activity.json
web | GET /api/profile/ticket/{id} | 200 | web/routes/profile-ticket-{orderId}.json
web | GET /api/search?q={term} | 200 | web/routes/search.json
organizer | GET /api/auth/get-session | 200 | organizer/get-session.json
organizer | GET /api/auth/list-sessions | 200 | organizer/list-sessions.json
organizer | GET /api/auth/organization/list | 200 | organizer/organization-list.json
organizer | GET /api/auth/organization/get-full-organization | 200 | organizer/organization-get-full-organization.json
organizer | GET /api/auth/organization/list-invitations | 200 | organizer/organization-list-invitations.json
organizer | GET /api/dashboard | 200 | organizer/routes/dashboard.json
organizer | GET /api/events | 200 | organizer/routes/events.json
organizer | GET /api/events/{id} | 200 | organizer/routes/events-{id}.json
organizer | GET /api/events/{id}/orders | 200 | organizer/routes/events-{id}-orders.json
organizer | GET /api/events/{id}/performers | 200 | organizer/routes/events-{id}-performers.json
organizer | GET /api/events/{id}/tickets | 200 | organizer/routes/events-{id}-tickets.json
organizer | GET /api/events/{id}/vouchers | 200 | organizer/routes/events-{id}-vouchers.json
organizer | GET /api/finance | 200 | organizer/routes/finance.json
organizer | GET /api/orders | 200 | organizer/routes/orders.json
organizer | GET /api/org/payout | 200 | organizer/routes/org-payout.json
organizer | GET /api/org/status | 200 | organizer/routes/org-status.json
admin | GET /api/auth/get-session | 200 | admin/get-session.json
admin | GET /api/banners | 200 | admin/routes/banners.json
admin | GET /api/dashboard | 200 | admin/routes/dashboard.json
admin | GET /api/events | 200 | admin/routes/events.json
admin | GET /api/events/{id} | 200 | admin/routes/events-{id}.json
admin | GET /api/orders | 200 | admin/routes/orders.json
admin | GET /api/orgs | 200 | admin/routes/orgs.json
admin | GET /api/orgs/{id} | 200 | admin/routes/orgs-{id}.json
admin | GET /api/payouts | 200 | admin/routes/payouts.json
admin | GET /api/payouts/{id} | 200 | admin/routes/payouts-{id}.json
admin | GET /api/settings | 200 | admin/routes/settings.json
admin | GET /api/team/invitations | 200 | admin/routes/team-invitations.json
admin | GET /api/team/members | 200 | admin/routes/team-members.json
admin | GET /api/users | 200 | admin/routes/users.json
admin | GET /api/users/{id} | 200 | admin/routes/users-{id}.json
web | GET /api/histori/{orderId}/ticket.pdf | 200 | skipped (application/pdf)
organizer | GET /api/orders/export | 200 | skipped (xlsx)
admin | GET /api/orders/export | 200 | skipped (text/csv)
