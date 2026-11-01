package ordersexport

import (
	"strings"
	"time"
)

type AdminRow struct {
	ID              string    `json:"id"`
	ReferenceNumber *string   `json:"referenceNumber"`
	OrgName         string    `json:"orgName"`
	BuyerName       string    `json:"buyerName"`
	BuyerEmail      string    `json:"buyerEmail"`
	EventName       string    `json:"eventName"`
	Tickets         string    `json:"tickets"`
	Subtotal        int64     `json:"subtotal"`
	Tax             int64     `json:"tax"`
	ServiceFee      int64     `json:"serviceFee"`
	Total           int64     `json:"total"`
	VoucherCode     *string   `json:"voucherCode"`
	VoucherDiscount int64     `json:"voucherDiscount"`
	PaymentMethod   string    `json:"paymentMethod"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
}

type MidtransFees struct {
	VaFee       float64
	QrisPercent float64
}

func (f MidtransFees) perOrder(status, method string, total int64) float64 {
	if status != "paid" {
		return 0
	}
	switch method {
	case "va_bank":
		return f.VaFee
	case "qris":
		return (float64(total) * f.QrisPercent) / 100
	}
	return 0
}

const bom = "\xEF\xBB\xBF"

var adminCSVHeader = []string{
	"Reference", "Organization", "Buyer", "Email", "Event", "Tickets",
	"Subtotal", "Discount", "Voucher", "Tax", "Fee", "Total", "Midtrans", "Net", "Status", "Date",
}

func csvCell(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func neutralize(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func AdminCSV(rows []AdminRow, fees MidtransFees, loc *time.Location) []byte {
	lines := make([]string, 0, len(rows)+1)
	cells := make([]string, len(adminCSVHeader))
	for i, h := range adminCSVHeader {
		cells[i] = csvCell(h)
	}
	lines = append(lines, strings.Join(cells, ","))

	for _, r := range rows {
		ref := r.ID
		if r.ReferenceNumber != nil {
			ref = *r.ReferenceNumber
		}
		discount := ""
		if r.VoucherDiscount > 0 {
			discount = "−" + formatIDR(float64(r.VoucherDiscount))
		}
		voucher := ""
		if r.VoucherCode != nil {
			voucher = *r.VoucherCode
		}
		midtrans, net := "", ""
		if r.Status == "paid" {
			fee := fees.perOrder(r.Status, r.PaymentMethod, r.Total)
			midtrans = "−" + formatIDR(fee)
			net = formatIDR(float64(r.Total) - fee)
		}
		vals := []string{
			neutralize(ref), neutralize(r.OrgName), neutralize(r.BuyerName), neutralize(r.BuyerEmail),
			neutralize(r.EventName), neutralize(r.Tickets),
			formatIDR(float64(r.Subtotal)), discount, neutralize(voucher),
			formatIDR(float64(r.Tax)), formatIDR(float64(r.ServiceFee)), formatIDR(float64(r.Total)),
			midtrans, net, neutralize(statusLabel(r.Status)), formatDateID(r.CreatedAt, loc),
		}
		for i, v := range vals {
			cells[i] = csvCell(v)
		}
		lines = append(lines, strings.Join(cells, ","))
	}
	return []byte(bom + strings.Join(lines, "\r\n"))
}
