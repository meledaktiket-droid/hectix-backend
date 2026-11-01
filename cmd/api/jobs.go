package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/config"
	"hectix/backend/internal/eticket"
	"hectix/backend/internal/jobs"
	"hectix/backend/internal/mail"
	"hectix/backend/internal/payments"
	"hectix/backend/internal/ticketpdf"
)

func jobHandlers(cfg config.Config, pool *pgxpool.Pool) map[string]jobs.Handler {
	return map[string]jobs.Handler{
		"order.status": func(ctx context.Context, payload []byte) error {
			var p struct {
				OrderID string `json:"orderId"`
				Status  string `json:"status"`
			}
			if err := json.Unmarshal(payload, &p); err != nil {
				return err
			}
			if p.OrderID == "" || p.Status == "" {
				return errors.New("order.status: orderId and status are required")
			}
			return payments.ApplyStatus(ctx, pool, p.OrderID, p.Status)
		},

		"eticket.send": (&eticket.Sender{
			DB:      pool,
			Mail:    &mail.Mailer{APIKey: cfg.ResendAPIKey, From: cfg.MailFrom},
			Banners: ticketpdf.LocalBanners(cfg.UploadDir, slices.Collect(maps.Keys(cfg.Apps))),
		}).Handle,
	}
}
