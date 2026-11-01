package orders

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"hectix/backend/internal/auth"
	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/payments"
	"hectix/backend/internal/settings"
)

type Charger interface {
	Charge(ctx context.Context, orderID string, total int, method, bank string) (va, qris string, err error)
}

type Checkout struct {
	DB       *pgxpool.Pool
	Midtrans Charger
}

type checkoutBody struct {
	EventID string `json:"eventId"`
	Items   []struct {
		TicketTypeID string  `json:"ticketTypeId"`
		Quantity     float64 `json:"quantity"`
	} `json:"items"`
	PaymentMethod string  `json:"paymentMethod"`
	Bank          *string `json:"bank"`
	VoucherCode   *string `json:"voucherCode"`
}

type lineItem struct {
	ticketTypeID, name string
	price, qty         int
}

var vaBanks = map[string]bool{"bca": true, "mandiri": true, "bri": true, "bsi": true, "bni": true}

var (
	errSoldOut      = errors.New("sold out")
	errVoucherLimit = errors.New("voucher limit reached")
)

func jsRound(x float64) int { return int(math.Floor(x + 0.5)) }

func (c *Checkout) Create(w http.ResponseWriter, r *http.Request) {
	c.create(w, r, auth.UserFrom(r.Context()).ID)
}

func (c *Checkout) create(w http.ResponseWriter, r *http.Request, userID string) {
	ctx := r.Context()
	fail := func(where string, err error) {
		slog.Error("checkout: "+where, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "Internal server error")
	}

	var body checkoutBody
	if err := httpx.Decode(r, &body); err != nil {
		httpx.Error(w, http.StatusBadRequest, "Invalid body")
		return
	}
	if len(body.Items) == 0 {
		httpx.Error(w, http.StatusBadRequest, "No items")
		return
	}
	for _, it := range body.Items {
		if it.Quantity < 1 || it.Quantity != math.Trunc(it.Quantity) || it.Quantity > math.MaxInt32 {
			httpx.Error(w, http.StatusBadRequest, "Invalid quantity")
			return
		}
	}

	switch {
	case body.PaymentMethod == "qris":
		body.Bank = nil
	case body.PaymentMethod == "va_bank" && body.Bank != nil && vaBanks[*body.Bank]:
	default:
		httpx.Error(w, http.StatusBadRequest, "Metode pembayaran tidak valid")
		return
	}

	var ended bool
	err := c.DB.QueryRow(ctx, `select coalesce(ends_at, starts_at) <= now() from event
		where id=$1 and status='published' and deleted_at is null`, body.EventID).Scan(&ended)
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.Error(w, http.StatusNotFound, "Acara tidak ditemukan")
		return
	}
	if err != nil {
		fail("event", err)
		return
	}
	if ended {
		httpx.Error(w, http.StatusBadRequest, "Acara telah berakhir, tiket tidak bisa dibeli.")
		return
	}

	ids := make([]string, len(body.Items))
	for i, it := range body.Items {
		ids[i] = it.TicketTypeID
	}
	rows, err := c.DB.Query(ctx, `select id, name, price from ticket_type where event_id=$1 and id = any($2)`, body.EventID, ids)
	if err != nil {
		fail("ticket types", err)
		return
	}
	types := map[string]lineItem{}
	var tt lineItem
	if _, err := pgx.ForEachRow(rows, []any{&tt.ticketTypeID, &tt.name, &tt.price}, func() error {
		types[tt.ticketTypeID] = tt
		return nil
	}); err != nil {
		fail("ticket types", err)
		return
	}

	subtotal := 0
	items := make([]lineItem, len(body.Items))
	for i, it := range body.Items {
		t, ok := types[it.TicketTypeID]
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "Ticket type not found")
			return
		}
		t.qty = int(it.Quantity)
		items[i] = t
		subtotal += t.price * t.qty
	}

	voucherDiscount := 0
	var voucherID, voucherCode *string
	if body.VoucherCode != nil && *body.VoucherCode != "" {
		code := strings.ToUpper(strings.TrimSpace(*body.VoucherCode))
		var (
			id, typ string
			amount  int
			usable  bool
		)
		err := c.DB.QueryRow(ctx, `select id, type, amount,
				(expires_at is null or expires_at > now()) and (coalesce(usage_limit, 0) = 0 or usage_count < usage_limit)
			from voucher where event_id=$1 and code=$2 and deleted_at is null`, body.EventID, code).Scan(&id, &typ, &amount, &usable)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			fail("voucher", err)
			return
		}
		if err == nil && usable {
			if typ == "percent" {
				voucherDiscount = jsRound(float64(subtotal) * float64(amount) / 100)
			} else {
				voucherDiscount = amount
			}
			voucherDiscount = min(voucherDiscount, subtotal)
			voucherID, voucherCode = &id, &code
		}
	}

	st, err := settings.Get(ctx, c.DB)
	if err != nil {
		fail("settings", err)
		return
	}
	discounted := subtotal - voucherDiscount
	tax := jsRound(float64(discounted) * st.TaxPercent / 100)
	serviceFee := jsRound(st.ServiceFee)
	total := discounted + tax + serviceFee
	orderID := dbx.UUID()

	reserve := slices.Clone(items)
	slices.SortStableFunc(reserve, func(a, b lineItem) int { return strings.Compare(a.ticketTypeID, b.ticketTypeID) })
	err = dbx.Tx(ctx, c.DB, func(tx pgx.Tx) error {
		for _, it := range reserve {
			tag, err := tx.Exec(ctx, `update ticket_type set sold = sold + $1 where id=$2 and sold + $1 <= quantity`, it.qty, it.ticketTypeID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errSoldOut
			}
		}
		if _, err := tx.Exec(ctx, `insert into "order"(id,user_id,event_id,status,payment_method,bank,subtotal,tax,service_fee,total,
				voucher_code,voucher_discount,send_eticket,expires_at,created_at,updated_at)
			values($1,$2,$3,'pending_payment',$4,$5,$6,$7,$8,$9,$10,$11,$12,now()+interval '15 minutes',now(),now())`,
			orderID, userID, body.EventID, body.PaymentMethod, body.Bank, discounted, tax, serviceFee, total,
			voucherCode, voucherDiscount, true); err != nil {
			return err
		}
		for _, it := range items {
			if _, err := tx.Exec(ctx, `insert into order_item(id,order_id,ticket_type_id,ticket_type_name,price,quantity) values($1,$2,$3,$4,$5,$6)`,
				dbx.UUID(), orderID, it.ticketTypeID, it.name, it.price, it.qty); err != nil {
				return err
			}
		}
		if voucherID != nil {
			tag, err := tx.Exec(ctx, `update voucher set usage_count = usage_count + 1
				where id=$1 and deleted_at is null and (usage_limit is null or usage_limit = 0 or usage_count < usage_limit)`, *voucherID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return errVoucherLimit
			}
		}
		return nil
	})
	if errors.Is(err, errSoldOut) {
		httpx.Error(w, http.StatusConflict, "Tiket habis")
		return
	}
	if errors.Is(err, errVoucherLimit) {
		httpx.Error(w, http.StatusConflict, "Kuota voucher habis")
		return
	}
	if err != nil {
		fail("reserve", err)
		return
	}

	ctx = context.WithoutCancel(ctx)
	bank := ""
	if body.Bank != nil {
		bank = *body.Bank
	}
	va, qris, err := c.Midtrans.Charge(ctx, orderID, total, body.PaymentMethod, bank)
	if err != nil {
		slog.Error("checkout: charge", "order", orderID, "err", err)
		if err := dbx.Tx(ctx, c.DB, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `update "order" set status='cancelled', updated_at=now() where id=$1 and status='pending_payment'`, orderID)
			if err != nil || tag.RowsAffected() == 0 {
				return err
			}
			return payments.Release(ctx, tx, orderID)
		}); err != nil {
			slog.Error("checkout: release after failed charge", "order", orderID, "err", err)
		}
		httpx.Error(w, http.StatusBadGateway, "Gagal membuat pembayaran")
		return
	}

	if _, err := c.DB.Exec(ctx, `update "order" set va_number=nullif($1,''), qris_url=nullif($2,''), updated_at=now() where id=$3`, va, qris, orderID); err != nil {
		fail("save payment", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]string{"orderId": orderID})
}
