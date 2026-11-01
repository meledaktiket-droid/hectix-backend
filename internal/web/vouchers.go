package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/httpx"
)

func voucherInvalid(w http.ResponseWriter, msg string) {
	httpx.JSON(w, http.StatusOK, map[string]any{"valid": false, "message": msg})
}

func (h *handlers) validateVoucher(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EventID  string   `json:"eventId"`
		Code     string   `json:"code"`
		Subtotal *float64 `json:"subtotal"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid body")
		return
	}
	if body.EventID == "" || strings.TrimSpace(body.Code) == "" {
		voucherInvalid(w, "Kode tidak valid")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))

	var (
		vCode, typ      string
		amount          int
		expired, usedUp bool
	)
	err := h.db.QueryRow(r.Context(), `select code, type, amount,
			expires_at is not null and expires_at <= now(),
			coalesce(usage_limit, 0) <> 0 and usage_count >= usage_limit
		from voucher where event_id=$1 and code=$2 and deleted_at is null`, body.EventID, code).
		Scan(&vCode, &typ, &amount, &expired, &usedUp)
	if errors.Is(err, pgx.ErrNoRows) {
		voucherInvalid(w, "Kode tidak ditemukan")
		return
	}
	if err != nil {
		serverError(w, "validate voucher", err)
		return
	}
	if expired {
		voucherInvalid(w, "Kode sudah kedaluwarsa")
		return
	}
	if usedUp {
		voucherInvalid(w, "Kode sudah habis dipakai")
		return
	}

	subtotal := 0.0
	if body.Subtotal != nil {
		subtotal = *body.Subtotal
	}
	discount := amount
	if typ == "percent" {
		discount = jsRound(subtotal * float64(amount) / 100)
	}
	discount = min(discount, int(math.Max(subtotal, 0)))
	label := fmt.Sprintf("%d%%", amount)
	if typ != "percent" {
		label = "Rp " + formatIDR(discount)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"valid":    true,
		"code":     vCode,
		"type":     typ,
		"amount":   amount,
		"discount": discount,
		"message":  fmt.Sprintf("Voucher %s diterapkan (%s)", vCode, label),
	})
}

func formatIDR(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
