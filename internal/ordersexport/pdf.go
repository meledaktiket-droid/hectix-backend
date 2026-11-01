package ordersexport

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/go-pdf/fpdf"
)

const (
	pdfPageW    = 595.28
	pdfPageH    = 841.89
	pdfPad      = 32.0
	pdfContentW = pdfPageW - 2*pdfPad
	pdfBottom   = pdfPageH - pdfPad

	tableX      = pdfPad + 4
	tableInnerW = pdfContentW - 8
	headRowH    = 6 + 7.5*1.1 + 6 + 1
	bodyRowH    = 5 + 8*1.1 + 5 + 0.5
)

type rgb struct{ r, g, b int }

func hexColor(s string) rgb {
	var c rgb
	fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &c.r, &c.g, &c.b)
	return c
}

var (
	cInk      = hexColor("#0f172a")
	cMuted    = hexColor("#64748b")
	cRule     = hexColor("#e2e8f0")
	cHeadRule = hexColor("#cbd5e1")
	cRowRule  = hexColor("#eef2f6")
	cAlt      = hexColor("#fafbfc")
	cTh       = hexColor("#475569")
	cFooter   = hexColor("#94a3b8")

	statusColors = map[string]rgb{
		"paid":            hexColor("#15803d"),
		"pending_payment": hexColor("#b45309"),
		"cancelled":       hexColor("#64748b"),
		"expired":         hexColor("#94a3b8"),
	}
	cStatusOther = hexColor("#334155")
)

type pdfColumn struct {
	name     string
	header   string
	pct      float64
	padRight float64
	right    bool
	ellipsis bool
	clip     int
}

var pdfColumns = []pdfColumn{
	{"buyer", "Buyer", 15, 8, false, true, 24},
	{"event", "Event", 16, 8, false, true, 26},
	{"tickets", "Tickets", 15, 8, false, true, 24},
	{"payment", "Payment", 12, 8, false, true, 14},
	{"subtotal", "Subtotal", 12, 8, true, false, 0},
	{"voucher", "Voucher", 11, 8, false, true, 12},
	{"discount", "Discount", 11, 8, true, false, 0},
	{"status", "Status", 8, 0, false, true, 0},
}

func pdfColumnByName(name string) pdfColumn {
	for _, c := range pdfColumns {
		if c.name == name {
			return c
		}
	}
	panic("unknown column " + name)
}

func (c pdfColumn) x() float64 {
	x := tableX
	for _, o := range pdfColumns {
		if o.name == c.name {
			return x
		}
		x += o.pct * tableInnerW / 100
	}
	return x
}

func (c pdfColumn) font() *hfont {
	if c.name == "status" {
		return helvBold
	}
	return helvRegular
}

func (c pdfColumn) avail() float64 { return c.pct*tableInnerW/100 - c.padRight }

func (c pdfColumn) text(s string) glyphLine {
	if c.clip > 0 {
		s = clip(s, c.clip)
	}
	return fitCell(s, c.font(), 8, c.avail(), c.ellipsis)
}

func clip(s string, max int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= max {
		return s
	}
	return strings.TrimRightFunc(string(utf16.Decode(u[:max-1])), isJSSpace) + "…"
}

type reportPDF struct {
	*fpdf.Fpdf
}

func (p *reportPDF) color(c rgb) { p.SetTextColor(c.r, c.g, c.b) }

func (p *reportPDF) fillRect(x, y, w, h float64, c rgb) {
	p.SetFillColor(c.r, c.g, c.b)
	p.Rect(x, y, w, h, "F")
}

func simpleLine(f *hfont, size, spacing float64, s string) glyphLine {
	rs := []rune(s)
	return glyphLine{rs, f.advances(rs, size, spacing), runStarts(rs)}
}

func (p *reportPDF) drawLine(x, y float64, f *hfont, size, spacing float64, g glyphLine, box float64, right bool) {
	if len(g.rs) == 0 {
		return
	}
	adv := append([]float64{}, g.adv...)
	for i := len(g.rs) - 1; i >= 0 && g.rs[i] == ' '; i-- {
		box += adv[i]
	}
	w := g.width()
	if w > box {
		for i, d := range justify(g, box-w) {
			adv[i] += d
		}
		w = box
	}
	if right {
		x += box - w
	}
	p.SetFont(f.family, f.style, size)
	start, segX := 0, x
	for i, r := range g.rs {
		x += adv[i]
		natural := f.widths[r]*size/1000 + spacing
		if i == len(g.rs)-1 || math.Abs(adv[i]-natural) > 1e-9 || spacing != 0 {
			if seg := string(g.rs[start : i+1]); strings.TrimSpace(seg) != "" {
				p.Text(segX, y, winAnsi(seg))
			}
			start, segX = i+1, x
		}
	}
}

func (p *reportPDF) paragraph(x, y, avail float64, f *hfont, size float64, c rgb, s string) float64 {
	p.color(c)
	lh := size * 1.1
	lines := layoutLines(s, f, size, 0, avail, 0, false)
	for i, l := range lines {
		p.drawLine(x, y+float64(i)*lh+0.9*size, f, size, 0, l, avail, false)
	}
	return float64(len(lines)) * lh
}

func lineCount(s string, f *hfont, size, avail float64) int {
	return len(layoutLines(s, f, size, 0, avail, 0, false))
}

const (
	maxLogoSide   = 10000
	maxLogoPixels = 40_000_000
)

type logoImage struct {
	typ  string
	data []byte
	w, h float64
}

func decodeLogo(dataURL *string) *logoImage {
	if dataURL == nil || !strings.HasPrefix(*dataURL, "data:") {
		return nil
	}
	head, b64, ok := strings.Cut((*dataURL)[5:], ",")
	if !ok || !strings.HasSuffix(head, ";base64") {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxLogoSide || cfg.Height > maxLogoSide ||
		int64(cfg.Width)*int64(cfg.Height) > maxLogoPixels {
		return nil
	}
	typ := map[string]string{"png": "PNG", "jpeg": "JPG", "gif": "GIF"}[format]
	if typ == "" {
		return nil
	}
	probe := fpdf.New("P", "pt", "A4", "")
	probe.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: typ}, bytes.NewReader(data))
	if probe.Err() {
		return nil
	}
	return &logoImage{typ, data, float64(cfg.Width), float64(cfg.Height)}
}

func PDF(rows []Row, meta Meta) ([]byte, error) { return renderPDF(rows, meta, true) }

func renderPDF(rows []Row, meta Meta, compress bool) ([]byte, error) {
	initHelvetica()
	f := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt", Size: fpdf.SizeType{Wd: pdfPageW, Ht: pdfPageH}})
	f.SetCompression(compress)
	f.SetMargins(0, 0, 0)
	f.SetAutoPageBreak(false, 0)
	p := &reportPDF{f}
	logo := decodeLogo(meta.LogoDataURL)
	if logo != nil {
		f.RegisterImageOptionsReader("logo", fpdf.ImageOptions{ImageType: logo.typ}, bytes.NewReader(logo.data))
	}

	y := p.headerHeight(meta, logo)
	pageOf := make([]int, len(rows))
	tops := make([]float64, len(rows))
	page := 0
	y += headRowH
	for i := range rows {
		if y+bodyRowH > pdfBottom+1e-6 {
			page++
			y = pdfPad + headRowH
		}
		pageOf[i], tops[i] = page, y
		y += bodyRowH
	}
	total := page + 1

	generated := "Generated " + formatDateID(meta.GeneratedAt, wib)
	for pg := 0; pg < total; pg++ {
		f.AddPage()
		top := pdfPad
		if pg == 0 {
			top = p.drawHeader(meta, logo)
		}
		p.drawTableHead(top)
		for i, r := range rows {
			if pageOf[i] == pg {
				p.drawRow(tops[i], i, r)
			}
		}
		base := pdfPageH - 20 - 7*1.1 + 0.9*7
		p.color(cFooter)
		p.drawLine(pdfPad, base, helvRegular, 7, 0, simpleLine(helvRegular, 7, 0, generated), pdfContentW, false)
		pn := fmt.Sprintf("Page %d of %d", pg+1, total)
		p.drawLine(pdfPad, base, helvRegular, 7, 0, simpleLine(helvRegular, 7, 0, pn), pdfContentW, true)
	}

	if err := f.Error(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := f.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func orgRowHeight(meta Meta, logo *logoImage) (rowH, textH, textAvail float64) {
	textAvail = pdfContentW
	if logo != nil {
		textAvail -= 28 + 8
	}
	textH = float64(lineCount(meta.OrgName, helvBold, 14, textAvail)) * 14 * 1.1
	rowH = textH
	if logo != nil {
		rowH = max(rowH, 28)
	}
	return
}

func (p *reportPDF) headerHeight(meta Meta, logo *logoImage) float64 {
	rowH, _, _ := orgRowHeight(meta, logo)
	y := pdfPad + rowH + 4 + 8
	y += float64(lineCount("Orders Report", helvBold, 11, pdfContentW)) * 11 * 1.1
	y += 2 + float64(lineCount(meta.FilterLabel, helvRegular, 9, pdfContentW))*9*1.1
	return y + 12 + 42 + 16
}

func (p *reportPDF) drawHeader(meta Meta, logo *logoImage) float64 {
	rowH, textH, avail := orgRowHeight(meta, logo)
	x := pdfPad
	if logo != nil {
		s := min(28/logo.w, 28/logo.h)
		w, h := logo.w*s, logo.h*s
		top := pdfPad + (rowH-28)/2
		p.ImageOptions("logo", x+(28-w)/2, top+(28-h)/2, w, h, false, fpdf.ImageOptions{ImageType: logo.typ}, 0, "")
		x += 28 + 8
	}
	p.paragraph(x, pdfPad+(rowH-textH)/2, avail, helvBold, 14, cInk, meta.OrgName)

	y := pdfPad + rowH + 4 + 8
	y += p.paragraph(pdfPad, y, pdfContentW, helvBold, 11, cInk, "Orders Report")
	y += 2
	y += p.paragraph(pdfPad, y, pdfContentW, helvRegular, 9, cMuted, meta.FilterLabel)
	y += 12

	p.fillRect(pdfPad, y, pdfContentW, 1, cRule)
	p.fillRect(pdfPad, y+41, pdfContentW, 1, cRule)
	ct := y + 1 + 8
	x = pdfPad
	for _, it := range []struct{ label, value string }{
		{"Revenue (paid)", formatRupiah(float64(meta.Revenue))},
		{"Orders", strconv.Itoa(meta.OrderCount)},
	} {
		p.color(cMuted)
		p.drawLine(x, ct+0.9*8, helvRegular, 8, 0, simpleLine(helvRegular, 8, 0, it.label), pdfContentW, false)
		p.color(cInk)
		p.drawLine(x, ct+8*1.1+2+0.9*12, helvBold, 12, 0, simpleLine(helvBold, 12, 0, it.value), pdfContentW, false)
		x += max(helvRegular.width(it.label, 8, 0), helvBold.width(it.value, 12, 0)) + 24
	}
	return y + 42 + 16
}

func (p *reportPDF) drawTableHead(top float64) {
	p.fillRect(pdfPad, top+headRowH-1, pdfContentW, 1, cHeadRule)
	p.color(cTh)
	base := top + 6 + 0.9*7.5
	for _, c := range pdfColumns {
		g := layoutLines(strings.ToUpper(c.header), helvBold, 7.5, 0.3, c.avail(), 1, true)[0]
		p.drawLine(c.x(), base, helvBold, 7.5, 0.3, g, c.avail(), c.right)
	}
}

func (p *reportPDF) drawRow(top float64, i int, r Row) {
	if i%2 == 1 {
		p.fillRect(pdfPad, top, pdfContentW, bodyRowH, cAlt)
	}
	p.fillRect(pdfPad, top+bodyRowH-0.5, pdfContentW, 0.5, cRowRule)
	voucher := "—"
	if r.VoucherCode != nil {
		voucher = *r.VoucherCode
	}
	discount := "—"
	if r.VoucherDiscount > 0 {
		discount = formatRupiah(float64(r.VoucherDiscount))
	}
	vals := []string{r.BuyerName, r.EventName, r.Tickets, r.PaymentMethod,
		formatRupiah(float64(r.Subtotal)), voucher, discount, statusLabel(r.Status)}
	base := top + 5 + 0.9*8
	for ci, c := range pdfColumns {
		g := c.text(vals[ci])
		col := cInk
		if c.name == "status" {
			col = cStatusOther
			if sc, ok := statusColors[r.Status]; ok {
				col = sc
			}
		}
		p.color(col)
		p.drawLine(c.x(), base, c.font(), 8, 0, g, c.avail(), c.right)
	}
}
