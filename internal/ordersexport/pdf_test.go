package ordersexport

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCellTextMatchesReactPDF(t *testing.T) {
	b, err := os.ReadFile("testdata/fit_corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Col, In, Want string
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	nonWinAnsi := regexp.MustCompile(`[^\x00-\x7f\x{a0}-\x{ff}–—…×·’‘“”•€]`)
	checked := 0
	for _, c := range cases {
		if nonWinAnsi.MatchString(c.In) {
			continue
		}
		col := pdfColumnByName(c.Col)
		in := c.In
		if c.Col == "voucher" && in == "" {
			in = "—"
		}
		if c.Col == "status" {
			in = statusLabel(in)
		}
		if got := col.text(in).String(); got != c.Want {
			t.Errorf("%s %q: got %q, want %q", c.Col, c.In, got, c.Want)
		}
		checked++
	}
	if checked < 1400 {
		t.Fatalf("only %d cases checked", checked)
	}
}

func TestPDFStructure(t *testing.T) {
	fx := loadFixture(t)
	out, err := renderPDF(fx.Rows, fx.Meta, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(out, []byte("/Type /Page\n")); n != 2 {
		t.Errorf("pages = %d, want 2", n)
	}
	var text strings.Builder
	for _, m := range regexp.MustCompile(`\((.*?)\) Tj`).FindAllSubmatch(out, -1) {
		text.Write(m[1])
	}
	for _, s := range []string{"Page 1 of 2", "Page 2 of 2", "Orders Report", "Rp5.035.439", "Acme Events Indonesia", "Generated 1 Jun 2026"} {
		if !strings.Contains(text.String(), s) {
			t.Errorf("missing %q", s)
		}
	}
	if _, err := PDF(nil, Meta{OrgName: "Acme", FilterLabel: "All orders"}); err != nil {
		t.Fatal(err)
	}
}
