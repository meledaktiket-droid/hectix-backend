package organizer

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
)

const (
	msgVoucherExpired = "Expiry date cannot be in the past"
)

func startOfWIBDay(now time.Time) time.Time {
	y, m, d := now.In(wib).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, wib).UTC()
}

func (h *handlers) listVouchers(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	vs, err := list(r.Context(), h.db, scanVoucher, `select `+voucherCols+` from voucher where event_id = $1 and deleted_at is null`, id)
	if err != nil {
		serverError(w, "vouchers", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"vouchers": vs})
}

func (h *handlers) createVoucher(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if !h.requireEvent(w, r, c, id) {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	code, _ := b.str("code")
	if code = strings.TrimSpace(code); code == "" {
		httpx.Error(w, http.StatusBadRequest, "Code is required")
		return
	}
	typ, _ := b.str("type")
	if typ != "percent" && typ != "fixed" {
		httpx.Error(w, http.StatusBadRequest, "Type must be 'percent' or 'fixed'")
		return
	}
	amount, ok := b.integer("amount")
	if !ok || amount < 0 || (typ == "percent" && amount > 100) {
		httpx.Error(w, http.StatusBadRequest, "Valid amount is required")
		return
	}
	var usageLimit *int64
	if !b.isNull("usageLimit") {
		v, ok := b.integer("usageLimit")
		if !ok || v < 1 {
			httpx.Error(w, http.StatusBadRequest, "Usage limit must be at least 1")
			return
		}
		usageLimit = &v
	}
	var expiresAt *time.Time
	if b.truthy("expiresAt") {
		t, ok := b.date("expiresAt")
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Invalid expiresAt")
			return
		}
		if t.Before(startOfWIBDay(h.clock())) {
			httpx.Error(w, http.StatusBadRequest, msgVoucherExpired)
			return
		}
		expiresAt = &t
	}
	v, err := scanVoucher(h.db.QueryRow(r.Context(), `insert into voucher(id, event_id, code, type, amount, usage_limit,
		usage_count, expires_at, created_at) values($1, $2, $3, $4, $5, $6, 0, $7, $8) returning `+voucherCols,
		dbx.UUID(), id, strings.ToUpper(code), typ, amount, usageLimit, expiresAt, time.Now().UTC()))
	if pc, _ := pgCode(err); pc == "23505" {
		httpx.Error(w, http.StatusConflict, voucherCodeTaken(strings.ToUpper(code)))
		return
	}
	if err != nil {
		serverError(w, "create voucher", err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"voucher": v})
}

func (h *handlers) deleteVoucher(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	if !h.requireEvent(w, r, c, id) {
		return
	}
	voucherID, _ := b.str("voucherId")
	if _, err := h.db.Exec(r.Context(), `update voucher set deleted_at = $3 where id = $1 and event_id = $2 and deleted_at is null`,
		voucherID, id, time.Now().UTC()); err != nil {
		serverError(w, "delete voucher", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func voucherCodeTaken(code string) string {
	return "Kode voucher " + code + " sudah dipakai di event ini"
}
