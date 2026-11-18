package physicalxlsx

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExportPreservesLeadingZerosAndEmbedsQR(t *testing.T) {
	rows := make([]TicketRow, 10)
	for i := range rows {
		rows[i] = TicketRow{
			Code:         fmt.Sprintf("HXP-CODE-%04d", i+1),
			SerialNumber: fmt.Sprintf("%04d", i+1),
			AccessType:   "DAY_1",
			EventName:    "Gauland Fest",
			BatchName:    "Batch Day 1",
			BatchID:      "b1",
		}
	}

	raw, err := Export(rows)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	f, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("OpenReader failed: %v", err)
	}
	defer f.Close()

	const sheet = "Physical Tickets"
	allRows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatalf("GetRows failed: %v", err)
	}
	if len(allRows) != 11 {
		t.Fatalf("want 11 rows (1 header + 10 tickets), got %d", len(allRows))
	}

	// Verify T43: leading zeros preserved on serial number ("0001") and code matches
	code1, _ := f.GetCellValue(sheet, "A2")
	serial1, _ := f.GetCellValue(sheet, "B2")
	if code1 != "HXP-CODE-0001" || serial1 != "0001" {
		t.Fatalf("row 2 mismatch: code=%q serial=%q", code1, serial1)
	}

	// Verify QR picture is embedded in column C
	pics, err := f.GetPictures(sheet, "C2")
	if err != nil || len(pics) != 1 || pics[0].Extension != ".png" || len(pics[0].File) == 0 {
		t.Fatalf("expected 1 embedded PNG QR picture in C2, got %d (err=%v)", len(pics), err)
	}
}
