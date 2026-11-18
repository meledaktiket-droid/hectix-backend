package physicalxlsx

import (
	"bytes"
	"fmt"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
	"github.com/xuri/excelize/v2"
)

const ContentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

type TicketRow struct {
	Code         string
	SerialNumber string
	AccessType   string
	EventName    string
	BatchName    string
	BatchID      string
}

func Export(rows []TicketRow) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	const sheet = "Physical Tickets"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}

	textFmt := "@"
	textStyle, err := f.NewStyle(&excelize.Style{
		NumFmt:       49,
		CustomNumFmt: &textFmt,
		Alignment: &excelize.Alignment{
			Vertical: "center",
		},
	})
	if err != nil {
		return nil, err
	}
	headerStyle, err := f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true},
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"F1F5F9"}},
		Alignment: &excelize.Alignment{
			Vertical: "center",
		},
	})
	if err != nil {
		return nil, err
	}

	cols := []struct {
		col    string
		header string
		width  float64
	}{
		{"A", "code", 24},
		{"B", "nomor", 14},
		{"C", "QR", 18},
		{"D", "jenis_akses", 22},
		{"E", "event", 28},
		{"F", "batch", 24},
	}
	for _, c := range cols {
		if err := f.SetColWidth(sheet, c.col, c.col, c.width); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, c.col+"1", c.header); err != nil {
			return nil, err
		}
	}
	if err := f.SetRowHeight(sheet, 1, 24); err != nil {
		return nil, err
	}
	if err := f.SetRowStyle(sheet, 1, 1, headerStyle); err != nil {
		return nil, err
	}

	for i, r := range rows {
		rowNum := i + 2
		rowStr := strconv.Itoa(rowNum)

		if err := f.SetRowHeight(sheet, rowNum, 92); err != nil {
			return nil, err
		}
		if err := f.SetCellStyle(sheet, "A"+rowStr, "F"+rowStr, textStyle); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, "A"+rowStr, r.Code); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, "B"+rowStr, r.SerialNumber); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, "D"+rowStr, r.AccessType); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, "E"+rowStr, r.EventName); err != nil {
			return nil, err
		}
		if err := f.SetCellStr(sheet, "F"+rowStr, r.BatchName); err != nil {
			return nil, err
		}

		pngBytes, err := qrcode.Encode(r.Code, qrcode.Medium, 160)
		if err != nil {
			return nil, fmt.Errorf("encode qr for %s: %w", r.SerialNumber, err)
		}
		if err := f.AddPictureFromBytes(sheet, "C"+rowStr, &excelize.Picture{
			Extension: ".png",
			File:      pngBytes,
			Format: &excelize.GraphicOptions{
				OffsetX: 6,
				OffsetY: 6,
				ScaleX:  0.65,
				ScaleY:  0.65,
			},
		}); err != nil {
			return nil, fmt.Errorf("embed qr image at row %d: %w", rowNum, err)
		}
	}

	var buf bytes.Buffer
	if err := f.Write(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
