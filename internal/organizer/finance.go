package organizer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hectix/backend/internal/dbx"
	"hectix/backend/internal/httpx"
	"hectix/backend/internal/settings"
)

func availableBalance(ctx context.Context, db dbx.DB, orgID string) (int64, error) {
	var bal int64
	err := db.QueryRow(ctx, `select
		coalesce((select sum(o.subtotal) from "order" o join event e on e.id = o.event_id
			where e.organization_id = $1 and o.status = 'paid'), 0)
		- coalesce((select sum(amount) + sum(platform_fee) from payout_request
			where organization_id = $1 and status = 'processed'), 0)`, orgID).Scan(&bal)
	return bal, err
}

func (h *handlers) finance(w http.ResponseWriter, r *http.Request) {
	c, ok := h.org(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	bal, err := availableBalance(ctx, h.db, c.orgID)
	if err != nil {
		serverError(w, "finance balance", err)
		return
	}
	st, err := settings.Get(ctx, h.db)
	if err != nil {
		serverError(w, "finance settings", err)
		return
	}
	history, err := list(ctx, h.db, scanPayout, `select `+payoutCols+` from payout_request
		where organization_id = $1 order by created_at`, c.orgID)
	if err != nil {
		serverError(w, "finance history", err)
		return
	}
	httpx.JSON(w, http.StatusOK, struct {
		AvailableBalance int64        `json:"availableBalance"`
		PlatformFee      float64      `json:"platformFee"`
		MinPayout        float64      `json:"minPayout"`
		History          []*payoutRow `json:"history"`
	}{bal, st.PlatformFee, st.MinPayout, history})
}

func bankField(vd map[string]any, key string) (string, bool) {
	switch v := vd[key].(type) {
	case string:
		return v, v != ""
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), v != 0 && !math.IsNaN(v)
	case bool:
		return strconv.FormatBool(v), v
	case nil:
		return "", false
	}
	return "", true
}

type payoutErr struct {
	status int
	msg    string
}

func (e *payoutErr) Error() string { return e.msg }

func (h *handlers) requestPayout(w http.ResponseWriter, r *http.Request) {
	c, ok := h.editor(w, r)
	if !ok || !h.requireActiveOrg(w, r, c) {
		return
	}
	ctx := r.Context()
	var amount float64 = math.NaN()
	var raw json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(&raw) == nil {
		if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' {
			var b map[string]json.RawMessage
			if json.Unmarshal(t, &b) == nil {
				v, ok := b["amount"]
				amount = jsNumber(v, ok)
			}
		}
	}

	err := dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var vdText *string
		err := tx.QueryRow(ctx, `select verification_data from organization where id = $1 for update`, c.orgID).Scan(&vdText)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var pending bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from payout_request where organization_id = $1 and status = 'pending')`,
			c.orgID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return &payoutErr{http.StatusConflict, "A payout request is already pending"}
		}
		var vd map[string]any
		if vdText != nil {
			_ = json.Unmarshal([]byte(*vdText), &vd)
		}
		bankName, ok1 := bankField(vd, "bankName")
		accountNumber, ok2 := bankField(vd, "accountNumber")
		holder, ok3 := bankField(vd, "accountHolderName")
		if !ok1 || !ok2 || !ok3 {
			return &payoutErr{http.StatusBadRequest, "Bank account not set up"}
		}
		bal, err := availableBalance(ctx, tx, c.orgID)
		if err != nil {
			return err
		}
		st, err := settings.Get(ctx, tx)
		if err != nil {
			return err
		}
		switch {
		case math.IsNaN(amount) || math.IsInf(amount, 0) || amount != math.Trunc(amount) || amount <= 0:
			return &payoutErr{http.StatusBadRequest, "Jumlah tidak valid"}
		case amount < st.MinPayout:
			return &payoutErr{http.StatusBadRequest, "Minimal pencairan Rp " + localeID(st.MinPayout)}
		case amount <= st.PlatformFee:
			return &payoutErr{http.StatusBadRequest, "Jumlah harus lebih besar dari biaya platform"}
		case amount > float64(bal):
			return &payoutErr{http.StatusBadRequest, "Jumlah melebihi saldo tersedia"}
		}
		_, err = tx.Exec(ctx, `insert into payout_request(id, organization_id, amount, platform_fee, bank_name,
			account_number, account_holder_name, status, created_at) values($1, $2, $3, $4, $5, $6, $7, 'pending', $8)`,
			dbx.UUID(), c.orgID, int64(amount-st.PlatformFee), int64(st.PlatformFee), bankName, accountNumber, holder,
			time.Now().UTC())
		return err
	})
	var pe *payoutErr
	if errors.As(err, &pe) {
		httpx.Error(w, pe.status, pe.msg)
		return
	}
	if err != nil {
		serverError(w, "payout request", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handlers) owner(w http.ResponseWriter, r *http.Request) (*orgCtx, bool) {
	c, ok := h.org(w, r)
	if !ok {
		return nil, false
	}
	if c.role != "owner" {
		forbidden(w)
		return nil, false
	}
	return c, true
}

type kv struct {
	k string
	v json.RawMessage
}

func parseObject(s string) ([]kv, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil
	}
	var out []kv
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = setKV(out, kt.(string), v)
	}
	return out, nil
}

func setKV(list []kv, k string, v json.RawMessage) []kv {
	for i := range list {
		if list[i].k == k {
			list[i].v = v
			return list
		}
	}
	return append(list, kv{k, v})
}

func getKV(list []kv, k string) (json.RawMessage, bool) {
	for _, e := range list {
		if e.k == k {
			return e.v, true
		}
	}
	return nil, false
}

func stringifyKV(list []kv) (string, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range list {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, _ := marshal(e.k)
		buf.Write(k)
		buf.WriteByte(':')
		dec := json.NewDecoder(bytes.NewReader(e.v))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			return "", err
		}
		b, err := marshal(v)
		if err != nil {
			return "", err
		}
		buf.Write(b)
	}
	buf.WriteByte('}')
	return buf.String(), nil
}

func (h *handlers) verificationData(ctx context.Context, orgID string) ([]kv, error) {
	var vd *string
	err := h.db.QueryRow(ctx, `select verification_data from organization where id = $1`, orgID).Scan(&vd)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && vd == nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseObject(*vd)
}

var bankKeys = []string{"bankName", "accountNumber", "accountHolderName"}

func (h *handlers) getPayoutAccount(w http.ResponseWriter, r *http.Request) {
	c, ok := h.owner(w, r)
	if !ok {
		return
	}
	vd, err := h.verificationData(r.Context(), c.orgID)
	if err != nil {
		serverError(w, "payout account", err)
		return
	}
	out := map[string]string{}
	for _, k := range bankKeys {
		var s string
		if v, ok := getKV(vd, k); ok {
			_ = json.Unmarshal(v, &s)
		}
		out[k] = s
	}
	httpx.JSON(w, http.StatusOK, struct {
		BankName          string `json:"bankName"`
		AccountNumber     string `json:"accountNumber"`
		AccountHolderName string `json:"accountHolderName"`
	}{out["bankName"], out["accountNumber"], out["accountHolderName"]})
}

func (h *handlers) updatePayoutAccount(w http.ResponseWriter, r *http.Request) {
	c, ok := h.owner(w, r)
	if !ok {
		return
	}
	b, err := decodeObject(r)
	if err != nil {
		badJSON(w)
		return
	}
	fields := map[string]json.RawMessage{}
	for _, k := range bankKeys {
		if b.isNull(k) {
			continue
		}
		s, ok := b.str(k)
		if s = strings.TrimSpace(s); !ok || s == "" {
			httpx.Error(w, http.StatusBadRequest, "Invalid "+k)
			return
		}
		fields[k], _ = marshal(s)
	}
	ctx := r.Context()
	err = dbx.Tx(ctx, h.db, func(tx pgx.Tx) error {
		var vdText *string
		if err := tx.QueryRow(ctx, `select verification_data from organization where id = $1 for update`, c.orgID).Scan(&vdText); err != nil {
			return err
		}
		var vd []kv
		if vdText != nil {
			if vd, err = parseObject(*vdText); err != nil {
				return err
			}
		}
		for _, k := range bankKeys {
			if v, ok := fields[k]; ok {
				vd = setKV(vd, k, v)
			}
		}
		s, err := stringifyKV(vd)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `update organization set verification_data = $2 where id = $1`, c.orgID, s)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if err != nil {
		serverError(w, "payout account", err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}
