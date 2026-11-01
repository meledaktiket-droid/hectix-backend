package ordersexport

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

type adminFixtureRow struct {
	AdminRow
	Items [][2]any `json:"items"`
}

func TestAdminCSVMatchesReference(t *testing.T) {
	b, err := os.ReadFile("testdata/admin_rows.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		MidtransVaFee       float64           `json:"midtransVaFee"`
		MidtransQrisPercent float64           `json:"midtransQrisPercent"`
		Rows                []adminFixtureRow `json:"rows"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	rows := make([]AdminRow, len(fx.Rows))
	for i, r := range fx.Rows {
		var items []Item
		for _, it := range r.Items {
			items = append(items, Item{int(it[0].(float64)), it[1].(string)})
		}
		r.AdminRow.Tickets = TicketSummary(items)
		rows[i] = r.AdminRow
	}
	got := AdminCSV(rows, MidtransFees{VaFee: fx.MidtransVaFee, QrisPercent: fx.MidtransQrisPercent}, time.UTC)
	want, err := os.ReadFile("testdata/admin_reference.csv")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("csv mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestAdminCSVEmpty(t *testing.T) {
	got := string(AdminCSV(nil, MidtransFees{}, time.UTC))
	want := "\uFEFF\"Reference\",\"Organization\",\"Buyer\",\"Email\",\"Event\",\"Tickets\",\"Subtotal\",\"Discount\",\"Voucher\",\"Tax\",\"Fee\",\"Total\",\"Midtrans\",\"Net\",\"Status\",\"Date\""
	if got != want {
		t.Errorf("got %q", got)
	}
}

func TestAdminCSVInfiniteFee(t *testing.T) {
	got := string(AdminCSV([]AdminRow{{ID: "x", Status: "paid", PaymentMethod: "va_bank", Total: 1000}},
		MidtransFees{VaFee: math.Inf(1)}, time.UTC))
	if !bytes.Contains([]byte(got), []byte(`"−Rp∞","-Rp∞"`)) {
		t.Errorf("got %q", got)
	}
}
