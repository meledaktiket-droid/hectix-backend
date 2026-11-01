package admin

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"time"

	"hectix/backend/internal/httpx"
	"hectix/backend/internal/ordersexport"
	"hectix/backend/internal/settings"
)

type orderParams struct {
	Status, OrgID, From, To, Q string
	Page, PageSize             int
}

var orderStatuses = []string{"paid", "pending_payment", "cancelled", "expired"}

var dateRe = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

func isValidDate(v string) bool {
	if !dateRe.MatchString(v) {
		return false
	}
	t, err := time.Parse("2006-01-02", v)
	return err == nil && t.Year() >= 100
}

func parseOrderParams(q url.Values) orderParams {
	p := orderParams{
		Q:     jsTrim(q.Get("q")),
		OrgID: jsTrim(q.Get("orgId")),
	}
	if s := q.Get("status"); slices.Contains(orderStatuses, s) {
		p.Status = s
	}
	if v := q.Get("from"); isValidDate(v) {
		p.From = v
	}
	if v := q.Get("to"); isValidDate(v) {
		p.To = v
	}
	intOr := func(key, def string, fallback int) int {
		v := def
		if q.Has(key) {
			v = q.Get(key)
		}
		if n, ok := parseIntPrefix(v); ok && n != 0 {
			return n
		}
		return fallback
	}
	p.Page = max(1, intOr("page", "1", 1))
	p.PageSize = min(100, max(1, intOr("pageSize", "25", 25)))
	return p
}

func wibStart(date string) time.Time {
	t, _ := time.Parse("2006-01-02", date)
	return t.Add(-7 * time.Hour)
}

func (p orderParams) conditions() (base, withStatus conds) {
	if p.OrgID != "" {
		base.add("e.organization_id = ?", p.OrgID)
	}
	if p.From != "" {
		base.add("o.created_at >= ?", wibStart(p.From))
	}
	if p.To != "" {
		base.add("o.created_at < ?", wibStart(p.To).AddDate(0, 0, 1))
	}
	if p.Q != "" {
		like := likeContains(p.Q)
		base.add(`(u.name ilike ? escape '\' or u.email ilike ? escape '\')`, like, like)
	}
	withStatus = conds{parts: slices.Clone(base.parts), args: slices.Clone(base.args)}
	if p.Status != "" {
		withStatus.add("o.status = ?", p.Status)
	}
	return base, withStatus
}

const ordersFrom = ` from "order" o join "user" u on o.user_id = u.id join event e on o.event_id = e.id
	join organization org on e.organization_id = org.id`

func midtransFeeFor(st settings.Settings, status, method string, total int64) float64 {
	if status != "paid" {
		return 0
	}
	switch method {
	case "va_bank":
		return st.MidtransVaFee
	case "qris":
		return float64(total) * st.MidtransQrisPercent / 100
	}
	return 0
}

type adminOrder struct {
	ID              string             `json:"id"`
	Status          string             `json:"status"`
	Subtotal        int64              `json:"subtotal"`
	Tax             int64              `json:"tax"`
	ServiceFee      int64              `json:"serviceFee"`
	Total           int64              `json:"total"`
	VoucherCode     *string            `json:"voucherCode"`
	VoucherDiscount int64              `json:"voucherDiscount"`
	CreatedAt       httpx.Time         `json:"createdAt"`
	PaymentMethod   string             `json:"paymentMethod"`
	Bank            *string            `json:"bank"`
	MidtransFee     float64            `json:"midtransFee"`
	ReferenceNumber string             `json:"referenceNumber"`
	OrgName         string             `json:"orgName"`
	EventName       string             `json:"eventName"`
	BuyerName       string             `json:"buyerName"`
	BuyerEmail      string             `json:"buyerEmail"`
	Items           []orderItemSummary `json:"items"`
}

type ordersSummary struct {
	GrossRevenue      int64            `json:"grossRevenue"`
	ServiceFeeRevenue int64            `json:"serviceFeeRevenue"`
	MidtransFee       int64            `json:"midtransFee"`
	NetRevenue        int64            `json:"netRevenue"`
	Counts            map[string]int64 `json:"counts"`
	VoucherCount      int64            `json:"voucherCount"`
	DiscountTotal     int64            `json:"discountTotal"`
}

func (h *handlers) listOrders(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	p := parseOrderParams(r.URL.Query())
	base, filtered := p.conditions()

	type group struct {
		Status                                                   string
		Cnt, Gross, Fee, Voucher, VoucherCnt, VaCount, QrisTotal int64
	}
	groups, err := list[group](ctx, db, `select o.status, count(*),
		coalesce(sum(o.total), 0), coalesce(sum(o.service_fee), 0), coalesce(sum(o.voucher_discount), 0),
		count(*) filter (where o.voucher_discount > 0),
		count(*) filter (where o.payment_method = 'va_bank'),
		coalesce(sum(o.total) filter (where o.payment_method = 'qris'), 0)`+
		ordersFrom+base.where()+` group by o.status`, base.args...)
	if err != nil {
		serverError(w, "orders", err)
		return
	}
	sum := ordersSummary{Counts: map[string]int64{}}
	var paidVa, paidQris int64
	for _, g := range groups {
		sum.Counts[g.Status] = g.Cnt
		if g.Status == "paid" {
			sum.GrossRevenue, sum.ServiceFeeRevenue, sum.DiscountTotal, sum.VoucherCount = g.Gross, g.Fee, g.Voucher, g.VoucherCnt
			paidVa, paidQris = g.VaCount, g.QrisTotal
		}
	}
	st, err := settings.Get(ctx, db)
	if err != nil {
		serverError(w, "orders", err)
		return
	}
	sum.MidtransFee = int64(jsRound(float64(paidVa)*st.MidtransVaFee + float64(paidQris)*st.MidtransQrisPercent/100))
	sum.NetRevenue = sum.GrossRevenue - sum.MidtransFee

	var total int64
	if err := db.QueryRow(ctx, `select count(*)`+ordersFrom+filtered.where(), filtered.args...).Scan(&total); err != nil {
		serverError(w, "orders", err)
		return
	}

	type row struct {
		ID                        string
		Status                    string
		Subtotal, Tax, Fee, Total int64
		VoucherCode               *string
		VoucherDiscount           int64
		CreatedAt                 httpx.Time
		PaymentMethod             string
		Bank, ReferenceNumber     *string
		EventName, OrgName        string
		BuyerName, BuyerEmail     string
	}
	rows, err := list[row](ctx, db, `select o.id, o.status, o.subtotal, o.tax, o.service_fee, o.total,
		o.voucher_code, o.voucher_discount, o.created_at, o.payment_method, o.bank, o.reference_number,
		e.name, org.name, u.name, u.email`+ordersFrom+filtered.where()+`
		order by o.created_at desc limit `+itoa(p.PageSize)+` offset `+itoa((p.Page-1)*p.PageSize), filtered.args...)
	if err != nil {
		serverError(w, "orders", err)
		return
	}
	ids := make([]string, len(rows))
	for i, o := range rows {
		ids[i] = o.ID
	}
	items, err := itemsByOrder(r, h, ids)
	if err != nil {
		serverError(w, "orders", err)
		return
	}
	out := make([]adminOrder, len(rows))
	for i, o := range rows {
		ref := o.ID
		if o.ReferenceNumber != nil {
			ref = *o.ReferenceNumber
		}
		out[i] = adminOrder{o.ID, o.Status, o.Subtotal, o.Tax, o.Fee, o.Total, o.VoucherCode, o.VoucherDiscount,
			o.CreatedAt, o.PaymentMethod, o.Bank, midtransFeeFor(st, o.Status, o.PaymentMethod, o.Total), ref,
			o.OrgName, o.EventName, o.BuyerName, o.BuyerEmail, items.of(o.ID)}
	}
	httpx.JSON(w, http.StatusOK, struct {
		Orders  []adminOrder  `json:"orders"`
		Total   int64         `json:"total"`
		Summary ordersSummary `json:"summary"`
	}{out, total, sum})
}

func (h *handlers) exportOrders(w http.ResponseWriter, r *http.Request) {
	ctx, db := r.Context(), h.d.DB
	_, filtered := parseOrderParams(r.URL.Query()).conditions()
	rows, err := list[ordersexport.AdminRow](ctx, db, `select o.id, o.reference_number, org.name, u.name, u.email, e.name,
		'', o.subtotal, o.tax, o.service_fee, o.total, o.voucher_code, o.voucher_discount, o.payment_method, o.status,
		o.created_at`+ordersFrom+filtered.where()+` order by o.created_at desc`, filtered.args...)
	if err != nil {
		serverError(w, "orders export", err)
		return
	}
	ids := make([]string, len(rows))
	for i, o := range rows {
		ids[i] = o.ID
	}
	items, err := itemsByOrder(r, h, ids)
	if err != nil {
		serverError(w, "orders export", err)
		return
	}
	for i := range rows {
		var its []ordersexport.Item
		for _, it := range items[rows[i].ID] {
			its = append(its, ordersexport.Item{Quantity: int(it.Quantity), TicketTypeName: it.TicketTypeName})
		}
		rows[i].Tickets = ordersexport.TicketSummary(its)
	}
	st, err := settings.Get(ctx, db)
	if err != nil {
		serverError(w, "orders export", err)
		return
	}
	csv := ordersexport.AdminCSV(rows, ordersexport.MidtransFees{VaFee: st.MidtransVaFee, QrisPercent: st.MidtransQrisPercent}, time.UTC)
	w.Header().Set("Content-Type", ordersexport.ContentTypeCSV)
	w.Header().Set("Content-Disposition", ordersexport.ContentDisposition(ordersexport.Filename("csv", time.Now())))
	w.Write(csv)
}
