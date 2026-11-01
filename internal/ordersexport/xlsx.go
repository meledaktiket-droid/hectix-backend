package ordersexport

import (
	"bytes"
	"strconv"

	"github.com/xuri/excelize/v2"
)

const sheet = "Orders"

var xlsxColumns = []struct {
	header string
	width  float64
}{
	{"Reference", 22}, {"Buyer", 24}, {"Email", 28}, {"Event", 24}, {"Tickets", 30},
	{"Payment", 16}, {"Subtotal", 16}, {"Voucher", 16}, {"Discount", 14}, {"Status", 14}, {"Date", 16},
}

func excelSerial(ms int64) float64 { return 25569 + float64(ms)/(24*3600*1000) }

func XLSX(rows []Row, meta Meta) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	if err := f.SetDocProps(&excelize.DocProperties{Creator: meta.OrgName}); err != nil {
		return nil, err
	}

	rupiah := `"Rp"#,##0`
	date := "dd mmm yyyy"
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F1F5F9"}},
	})
	if err != nil {
		return nil, err
	}
	moneyStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &rupiah})
	if err != nil {
		return nil, err
	}
	dateStyle, err := f.NewStyle(&excelize.Style{CustomNumFmt: &date})
	if err != nil {
		return nil, err
	}

	for i, c := range xlsxColumns {
		col, _ := excelize.ColumnNumberToName(i + 1)
		if err := f.SetColWidth(sheet, col, col, c.width); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, col+"1", c.header); err != nil {
			return nil, err
		}
	}
	if err := f.SetRowStyle(sheet, 1, 1, headerStyle); err != nil {
		return nil, err
	}
	if err := f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft",
		Selection: []excelize.Selection{{Pane: "bottomLeft"}},
	}); err != nil {
		return nil, err
	}

	for i, r := range rows {
		voucher := "—"
		if r.VoucherCode != nil {
			voucher = *r.VoucherCode
		}
		n := i + 2
		values := []any{
			r.ReferenceNumber, r.BuyerName, r.BuyerEmail, r.EventName, r.Tickets, r.PaymentMethod,
			r.Subtotal, voucher, r.VoucherDiscount, statusLabel(r.Status), excelSerial(r.CreatedAt.UnixMilli()),
		}
		for c, v := range values {
			cell, _ := excelize.CoordinatesToCellName(c+1, n)
			var err error
			switch v := v.(type) {
			case string:
				err = f.SetCellStr(sheet, cell, v)
			case int64:
				err = f.SetCellInt(sheet, cell, v)
			case float64:
				err = f.SetCellFloat(sheet, cell, v, -1, 64)
			}
			if err != nil {
				return nil, err
			}
		}
		for _, s := range []struct {
			cell  string
			style int
		}{{"G", moneyStyle}, {"I", moneyStyle}, {"K", dateStyle}} {
			if err := f.SetCellStyle(sheet, s.cell+strconv.Itoa(n), s.cell+strconv.Itoa(n), s.style); err != nil {
				return nil, err
			}
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
