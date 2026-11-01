package eticket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/mail"
	"hectix/backend/internal/ticketpdf"
)

type Sender struct {
	DB      *pgxpool.Pool
	Mail    mail.Sender
	Banners ticketpdf.BannerLoader
}

func (s *Sender) Handle(ctx context.Context, payload []byte) error {
	var p struct {
		OrderID string `json:"orderId"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return fmt.Errorf("eticket: bad payload: %w", err)
	}
	if p.OrderID == "" {
		return errors.New("eticket: missing orderId")
	}

	var (
		ref, barcode, venue, city, banner *string
		total                             int64
		createdAt, startsAt               time.Time
		unsent                            bool
		eventName, buyerName, buyerEmail  string
	)
	err := s.DB.QueryRow(ctx, `
		select o.reference_number, o.barcode, o.total, o.created_at,
		       o.eticket_sent_at is null,
		       e.name, e.starts_at, e.venue_name, e.city, e.banner_url,
		       u.name, u.email
		from "order" o
		join event e on e.id = o.event_id
		join "user" u on u.id = o.user_id
		where o.id = $1`, p.OrderID).Scan(
		&ref, &barcode, &total, &createdAt, &unsent,
		&eventName, &startsAt, &venue, &city, &banner, &buyerName, &buyerEmail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !unsent || buyerEmail == "" || str(barcode) == "" || str(ref) == "" {
		return nil
	}

	rows, err := s.DB.Query(ctx, `select ticket_type_name, quantity from order_item where order_id = $1`, p.OrderID)
	if err != nil {
		return err
	}
	ticketType, quantity := "-", 0
	first := true
	var name string
	var qty int
	_, err = pgx.ForEachRow(rows, []any{&name, &qty}, func() error {
		if first {
			ticketType, first = name, false
		}
		quantity += qty
		return nil
	})
	if err != nil {
		return err
	}

	pdf, err := ticketpdf.Render(ticketpdf.Data{
		EventName:       eventName,
		EventStartsAt:   startsAt,
		EventVenueName:  str(venue),
		EventCity:       str(city),
		EventBannerURL:  str(banner),
		BuyerName:       buyerName,
		TicketTypeName:  ticketType,
		Quantity:        quantity,
		ReferenceNumber: *ref,
		CreatedAt:       createdAt,
		Total:           total,
		Barcode:         *barcode,
	}, s.Banners)
	if err != nil {
		return fmt.Errorf("eticket: render pdf: %w", err)
	}
	msg := mail.Eticket(mail.EticketData{EventName: eventName, ReferenceNumber: *ref, Quantity: quantity, Total: total})
	if err := s.Mail.Send(ctx, buyerEmail, msg.Subject, msg.HTML, msg.Text, mail.Attachment{
		Filename: mail.EticketFilename(*ref), Content: pdf, ContentType: "application/pdf",
	}); err != nil {
		return fmt.Errorf("eticket: send for order %s: %w", p.OrderID, err)
	}

	_, err = s.DB.Exec(context.WithoutCancel(ctx),
		`update "order" set eticket_sent_at = now() where id = $1 and eticket_sent_at is null`, p.OrderID)
	return err
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
