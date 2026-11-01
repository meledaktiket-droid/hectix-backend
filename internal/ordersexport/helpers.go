package ordersexport

import (
	"fmt"
	"strings"
	"time"
)

const (
	ContentTypeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	ContentTypePDF  = "application/pdf"
	ContentTypeCSV  = "text/csv; charset=utf-8"
)

func Filename(ext string, now time.Time) string {
	return "orders-" + now.UTC().Format("2006-01-02") + "." + ext
}

func ContentDisposition(filename string) string {
	return `attachment; filename="` + filename + `"`
}

func PaymentLabel(method string, bank *string) string {
	switch method {
	case "qris":
		return "QRIS"
	case "va_bank":
		if bank != nil && *bank != "" {
			return strings.ToUpper(*bank) + " VA"
		}
		return "Bank Transfer"
	}
	return method
}

type Item struct {
	Quantity       int
	TicketTypeName string
}

func TicketSummary(items []Item) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = fmt.Sprintf("%d× %s", it.Quantity, it.TicketTypeName)
	}
	return strings.Join(parts, ", ")
}
