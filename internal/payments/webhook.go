package payments

import (
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/httpx"
	"hectix/backend/internal/jobs"
)

type Webhook struct {
	DB        *pgxpool.Pool
	ServerKey string
}

func str(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var data map[string]json.RawMessage
	if err := httpx.Decode(r, &data); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid body")
		return
	}
	orderID := str(data["order_id"])

	sum := sha512.Sum512([]byte(orderID + str(data["status_code"]) + str(data["gross_amount"]) + h.ServerKey))
	expected := hex.EncodeToString(sum[:])
	got := str(data["signature_key"])
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
		httpx.Error(w, http.StatusForbidden, "Invalid signature")
		return
	}

	var target string
	switch tx := str(data["transaction_status"]); {
	case tx == "capture" && str(data["fraud_status"]) == "accept", tx == "settlement":
		target = "paid"
	case tx == "expire":
		target = "expired"
	case tx == "cancel", tx == "deny":
		target = "cancelled"
	}

	if target != "" {
		if err := jobs.Enqueue(r.Context(), h.DB, "order.status", map[string]string{"orderId": orderID, "status": target}); err != nil {
			slog.Error("midtrans webhook enqueue", "order", orderID, "err", err)
			httpx.Error(w, http.StatusInternalServerError, "Internal server error")
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"received": true})
}
