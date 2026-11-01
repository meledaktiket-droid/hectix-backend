package ordersexport

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/xuri/excelize/v2"
)

type fixture struct {
	Rows []Row `json:"rows"`
	Meta Meta  `json:"meta"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	b, err := os.ReadFile("testdata/rows.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func cellFormat(t *testing.T, f *excelize.File, cell string) (numFmt string, bold bool, fill string) {
	t.Helper()
	id, err := f.GetCellStyle("Orders", cell)
	if err != nil {
		t.Fatal(err)
	}
	st, err := f.GetStyle(id)
	if err != nil {
		t.Fatal(err)
	}
	if st.CustomNumFmt != nil {
		numFmt = *st.CustomNumFmt
	}
	if st.Font != nil {
		bold = st.Font.Bold
	}
	if st.Fill.Pattern == 1 && len(st.Fill.Color) > 0 {
		fill = st.Fill.Color[0]
	}
	return
}

func TestXLSXMatchesReference(t *testing.T) {
	fx := loadFixture(t)
	out, err := XLSX(fx.Rows, fx.Meta)
	if err != nil {
		t.Fatal(err)
	}
	got, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	want, err := excelize.OpenFile("testdata/reference.xlsx")
	if err != nil {
		t.Fatal(err)
	}

	if g, w := got.GetSheetList(), want.GetSheetList(); len(g) != 1 || g[0] != w[0] {
		t.Fatalf("sheets %v, want %v", g, w)
	}
	gp, _ := got.GetDocProps()
	if gp.Creator != fx.Meta.OrgName {
		t.Errorf("creator %q", gp.Creator)
	}
	gPanes, _ := got.GetPanes("Orders")
	wPanes, _ := want.GetPanes("Orders")
	if !gPanes.Freeze || gPanes.YSplit != wPanes.YSplit || gPanes.TopLeftCell != wPanes.TopLeftCell || gPanes.XSplit != 0 {
		t.Errorf("panes %+v, want %+v", gPanes, wPanes)
	}

	for c := 1; c <= 12; c++ {
		col, _ := excelize.ColumnNumberToName(c)
		gw, _ := got.GetColWidth("Orders", col)
		ww, _ := want.GetColWidth("Orders", col)
		if gw != ww {
			t.Errorf("col %s width %v, want %v", col, gw, ww)
		}
	}

	wantRows, _ := want.GetRows("Orders", excelize.Options{RawCellValue: true})
	if len(wantRows) != len(fx.Rows)+1 {
		t.Fatalf("reference has %d rows", len(wantRows))
	}
	for r := 1; r <= len(wantRows)+1; r++ {
		for c := 1; c <= 12; c++ {
			cell, _ := excelize.CoordinatesToCellName(c, r)
			gv, _ := got.GetCellValue("Orders", cell, excelize.Options{RawCellValue: true})
			wv, _ := want.GetCellValue("Orders", cell, excelize.Options{RawCellValue: true})
			if gv != wv {
				t.Errorf("%s value %q, want %q", cell, gv, wv)
			}
			gt, _ := got.GetCellType("Orders", cell)
			wt, _ := want.GetCellType("Orders", cell)
			if gt != wt {
				t.Errorf("%s type %v, want %v", cell, gt, wt)
			}
			gn, gb, gf := cellFormat(t, got, cell)
			wn, wb, wf := cellFormat(t, want, cell)
			if gn != wn || gb != wb || gf != wf {
				t.Errorf("%s style (%q,%v,%q), want (%q,%v,%q)", cell, gn, gb, gf, wn, wb, wf)
			}
		}
	}
}

func TestXLSXEmpty(t *testing.T) {
	out, err := XLSX(nil, Meta{OrgName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.GetRows("Orders")
	if len(rows) != 1 || rows[0][0] != "Reference" || rows[0][10] != "Date" {
		t.Errorf("rows %v", rows)
	}
}

func TestXLSXFormulaTextIsString(t *testing.T) {
	b, err := XLSX([]Row{{ReferenceNumber: "=1+1", BuyerName: "@SUM(A1)", Status: "paid"}}, Meta{OrgName: "O"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	for cell, want := range map[string]string{"A2": "=1+1", "B2": "@SUM(A1)"} {
		formula, err := f.GetCellFormula("Orders", cell)
		if err != nil || formula != "" {
			t.Fatalf("%s formula %q %v", cell, formula, err)
		}
		typ, _ := f.GetCellType("Orders", cell)
		v, _ := f.GetCellValue("Orders", cell)
		if v != want || (typ != excelize.CellTypeSharedString && typ != excelize.CellTypeInlineString) {
			t.Fatalf("%s = %q type %v", cell, v, typ)
		}
	}
}
