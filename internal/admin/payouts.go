package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/mail"
)

var payoutStatuses = []string{"pending", "processed", "rejected"}

type payoutRow struct {
	ID                string     `json:"id"`
	OrganizationID    string     `json:"organizationId"`
	OrgName           string     `json:"orgName"`
	Amount            int64      `json:"amount"`
	BankName          string     `json:"bankName"`
	AccountNumber     string     `json:"accountNumber"`
	AccountHolderName string     `json:"accountHolderName"`
	Status            string     `json:"status"`
	CreatedAt         httpx.Time `json:"createdAt"`
}

const payoutCols = `p.id, p.organization_id, o.name, p.amount, p.bank_name, p.account_number,
	p.account_holder_name, p.status, p.created_at`

func (h *handlers) listPayouts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := pageParam(r, "page")
	status := r.URL.Query().Get("status")
	if status != "" && !slices.Contains(payoutStatuses, status) {
		httpx.Error(w, http.StatusBadRequest, "Invalid status")
		return
	}
	var c conds
	if status != "" {
		c.add("p.status = ?", status)
	}
	rows, err := list[payoutRow](ctx, h.d.DB, `select `+payoutCols+`
		from payout_request p join organization o on p.organization_id = o.id`+c.where()+`
		order by case p.status when 'pending' then 1 when 'processed' then 2 when 'rejected' then 3 else 4 end, p.created_at
		limit `+itoa(pageLimit)+` offset `+itoa((p-1)*pageLimit), c.args...)
	if err != nil {
		serverError(w, "payouts", err)
		return
	}
	var total int64
	if err := h.d.DB.QueryRow(ctx, `select count(*) from payout_request p`+c.where(), c.args...).Scan(&total); err != nil {
		serverError(w, "payouts", err)
		return
	}
	httpx.JSON(w, http.StatusOK, page[payoutRow]{rows, total, p, totalPages(total, pageLimit)})
}

func (h *handlers) getPayout(w http.ResponseWriter, r *http.Request) {
	type detail struct {
		payoutRow
		ProcessedAt     *httpx.Time `json:"processedAt"`
		RejectionReason *string     `json:"rejectionReason"`
		ProofURL        *string     `json:"proofUrl"`
		ProcessedBy     *string     `json:"processedBy"`
		ProcessedByName *string     `json:"processedByName"`
	}
	ctx := r.Context()
	var d detail
	pr := &d.payoutRow
	err := h.d.DB.QueryRow(ctx, `select `+payoutCols+`, p.processed_at, p.rejection_reason, p.proof_url, p.processed_by, u.name
		from payout_request p join organization o on p.organization_id = o.id
		left join "user" u on p.processed_by = u.id where p.id = $1`, chi.URLParam(r, "id")).Scan(
		&pr.ID, &pr.OrganizationID, &pr.OrgName, &pr.Amount, &pr.BankName, &pr.AccountNumber, &pr.AccountHolderName,
		&pr.Status, &pr.CreatedAt, &d.ProcessedAt, &d.RejectionReason, &d.ProofURL, &d.ProcessedBy, &d.ProcessedByName)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Not found")
		return
	}
	if err != nil {
		serverError(w, "payout", err)
		return
	}
	httpx.JSON(w, http.StatusOK, d)
}

func (h *handlers) orgContact(ctx context.Context, orgID string) (email, name string, err error) {
	var e, n *string
	err = h.d.DB.QueryRow(ctx, `select
		(select u.email from member m join "user" u on m.user_id = u.id
			where m.organization_id = $1 order by (m.role = 'owner') desc limit 1),
		(select name from organization where id = $1)`, orgID).Scan(&e, &n)
	if e != nil {
		email = *e
	}
	name = "your organization"
	if n != nil {
		name = *n
	}
	return email, name, err
}

func (h *handlers) notifyOrg(ctx context.Context, what, orgID string, build func(orgName string) mail.Message) {
	to, name, err := h.orgContact(ctx, orgID)
	if err == nil && to != "" {
		err = h.send(ctx, to, build(name))
	}
	if err != nil {
		log.Printf("admin: payout %s email failed: %v", what, err)
	}
}

func (h *handlers) processPayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		ProofURL any `json:"proofUrl"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	proof, _ := body.ProofURL.(string)
	if proof == "" {
		httpx.Error(w, http.StatusBadRequest, "Transfer proof is required")
		return
	}
	if !isAssetURL(proof) {
		httpx.Error(w, http.StatusBadRequest, "Invalid transfer proof URL")
		return
	}
	var orgID string
	var amount int64
	err := h.d.DB.QueryRow(ctx, `update payout_request set status = 'processed', processed_at = $3, processed_by = $4, proof_url = $2
		where id = $1 and status = 'pending' returning organization_id, amount`,
		chi.URLParam(r, "id"), proof, time.Now().UTC(), auth.UserFrom(ctx).ID).Scan(&orgID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusConflict, "This payout is no longer pending")
		return
	}
	if err != nil {
		serverError(w, "process payout", err)
		return
	}
	h.notifyOrg(ctx, "processed", orgID, func(orgName string) mail.Message {
		return mail.PayoutProcessed(orgName, formatIDR(amount), proof)
	})
	ok(w)
}

func (h *handlers) rejectPayout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body struct {
		Reason any `json:"reason"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		badBody(w)
		return
	}
	reason, _ := body.Reason.(string)
	reason = jsTrim(reason)
	if reason == "" {
		httpx.Error(w, http.StatusBadRequest, "A rejection reason is required")
		return
	}
	var orgID string
	var amount int64
	err := h.d.DB.QueryRow(ctx, `update payout_request set status = 'rejected', rejection_reason = $2, processed_by = $3
		where id = $1 and status = 'pending' returning organization_id, amount`,
		chi.URLParam(r, "id"), reason, auth.UserFrom(ctx).ID).Scan(&orgID, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusConflict, "This payout is no longer pending")
		return
	}
	if err != nil {
		serverError(w, "reject payout", err)
		return
	}
	h.notifyOrg(ctx, "rejected", orgID, func(orgName string) mail.Message {
		return mail.PayoutRejected(orgName, formatIDR(amount), reason)
	})
	ok(w)
}
