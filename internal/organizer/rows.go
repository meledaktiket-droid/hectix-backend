package organizer

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

type eventRow struct {
	ID             string          `json:"id"`
	OrganizationID string          `json:"organizationId"`
	Name           string          `json:"name"`
	Description    *string         `json:"description"`
	VenueName      *string         `json:"venueName"`
	Address        *string         `json:"address"`
	City           *string         `json:"city"`
	BannerURL      *string         `json:"bannerUrl"`
	Categories     json.RawMessage `json:"categories"`
	StartsAt       httpx.Time      `json:"startsAt"`
	EndsAt         *httpx.Time     `json:"endsAt"`
	Status         string          `json:"status"`
	Visibility     string          `json:"visibility"`
	AttendeeForm   bool            `json:"attendeeForm"`
	DeletedAt      *httpx.Time     `json:"deletedAt"`
	CreatedAt      httpx.Time      `json:"createdAt"`
	UpdatedAt      httpx.Time      `json:"updatedAt"`
}

const eventCols = `id, organization_id, name, description, venue_name, address, city, banner_url, categories,
	starts_at, ends_at, status, visibility, attendee_form, deleted_at, created_at, updated_at`

func scanEvent(row pgx.Row) (*eventRow, error) {
	var e eventRow
	err := row.Scan(&e.ID, &e.OrganizationID, &e.Name, &e.Description, &e.VenueName, &e.Address, &e.City,
		&e.BannerURL, &e.Categories, &e.StartsAt, &e.EndsAt, &e.Status, &e.Visibility, &e.AttendeeForm,
		&e.DeletedAt, &e.CreatedAt, &e.UpdatedAt)
	return &e, err
}

type ticketRow struct {
	ID           string          `json:"id"`
	EventID      string          `json:"eventId"`
	Name         string          `json:"name"`
	Price        int64           `json:"price"`
	Quantity     int64           `json:"quantity"`
	Sold         int64           `json:"sold"`
	Benefits     json.RawMessage `json:"benefits"`
	DisplayOrder int64           `json:"displayOrder"`
	CreatedAt    httpx.Time      `json:"createdAt"`
	UpdatedAt    httpx.Time      `json:"updatedAt"`
}

const ticketCols = `id, event_id, name, price, quantity, sold, benefits, display_order, created_at, updated_at`

func scanTicket(row pgx.Row) (*ticketRow, error) {
	var t ticketRow
	err := row.Scan(&t.ID, &t.EventID, &t.Name, &t.Price, &t.Quantity, &t.Sold, &t.Benefits, &t.DisplayOrder,
		&t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

type performerRow struct {
	ID           string      `json:"id"`
	EventID      string      `json:"eventId"`
	Name         string      `json:"name"`
	ImageURL     *string     `json:"imageUrl"`
	DisplayOrder int64       `json:"displayOrder"`
	DeletedAt    *httpx.Time `json:"deletedAt"`
	CreatedAt    httpx.Time  `json:"createdAt"`
	UpdatedAt    httpx.Time  `json:"updatedAt"`
}

const performerCols = `id, event_id, name, image_url, display_order, deleted_at, created_at, updated_at`

func scanPerformer(row pgx.Row) (*performerRow, error) {
	var p performerRow
	err := row.Scan(&p.ID, &p.EventID, &p.Name, &p.ImageURL, &p.DisplayOrder, &p.DeletedAt, &p.CreatedAt, &p.UpdatedAt)
	return &p, err
}

type voucherRow struct {
	ID         string      `json:"id"`
	EventID    string      `json:"eventId"`
	Code       string      `json:"code"`
	Type       string      `json:"type"`
	Amount     int64       `json:"amount"`
	UsageLimit *int64      `json:"usageLimit"`
	UsageCount int64       `json:"usageCount"`
	ExpiresAt  *httpx.Time `json:"expiresAt"`
	DeletedAt  *httpx.Time `json:"deletedAt"`
	CreatedAt  httpx.Time  `json:"createdAt"`
}

const voucherCols = `id, event_id, code, type, amount, usage_limit, usage_count, expires_at, deleted_at, created_at`

func scanVoucher(row pgx.Row) (*voucherRow, error) {
	var v voucherRow
	err := row.Scan(&v.ID, &v.EventID, &v.Code, &v.Type, &v.Amount, &v.UsageLimit, &v.UsageCount, &v.ExpiresAt,
		&v.DeletedAt, &v.CreatedAt)
	return &v, err
}

type payoutRow struct {
	ID                string      `json:"id"`
	OrganizationID    string      `json:"organizationId"`
	Amount            int64       `json:"amount"`
	PlatformFee       int64       `json:"platformFee"`
	BankName          string      `json:"bankName"`
	AccountNumber     string      `json:"accountNumber"`
	AccountHolderName string      `json:"accountHolderName"`
	Status            string      `json:"status"`
	CreatedAt         httpx.Time  `json:"createdAt"`
	ProcessedAt       *httpx.Time `json:"processedAt"`
	RejectionReason   *string     `json:"rejectionReason"`
	ProcessedBy       *string     `json:"processedBy"`
	ProofURL          *string     `json:"proofUrl"`
}

const payoutCols = `id, organization_id, amount, platform_fee, bank_name, account_number, account_holder_name,
	status, created_at, processed_at, rejection_reason, processed_by, proof_url`

func scanPayout(row pgx.Row) (*payoutRow, error) {
	var p payoutRow
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Amount, &p.PlatformFee, &p.BankName, &p.AccountNumber,
		&p.AccountHolderName, &p.Status, &p.CreatedAt, &p.ProcessedAt, &p.RejectionReason, &p.ProcessedBy, &p.ProofURL)
	return &p, err
}

func list[T any](ctx context.Context, db dbx.DB, scan func(pgx.Row) (*T, error), sql string, args ...any) ([]*T, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
