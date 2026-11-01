package ordersexport

import "time"

type Row struct {
	ReferenceNumber string    `json:"referenceNumber"`
	BuyerName       string    `json:"buyerName"`
	BuyerEmail      string    `json:"buyerEmail"`
	EventName       string    `json:"eventName"`
	Tickets         string    `json:"tickets"`
	PaymentMethod   string    `json:"paymentMethod"`
	Total           int64     `json:"total"`
	Subtotal        int64     `json:"subtotal"`
	VoucherCode     *string   `json:"voucherCode"`
	VoucherDiscount int64     `json:"voucherDiscount"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
}

type Meta struct {
	OrgName     string    `json:"orgName"`
	LogoDataURL *string   `json:"logoDataUrl"`
	FilterLabel string    `json:"filterLabel"`
	GeneratedAt time.Time `json:"generatedAt"`
	Revenue     int64     `json:"revenue"`
	OrderCount  int       `json:"orderCount"`
}

var statusLabels = map[string]string{
	"paid": "Paid", "pending_payment": "Pending", "cancelled": "Cancelled", "expired": "Expired",
}

func statusLabel(s string) string {
	if l, ok := statusLabels[s]; ok {
		return l
	}
	return s
}
