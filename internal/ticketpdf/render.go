package ticketpdf

import (
	"bytes"
	"embed"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-pdf/fpdf"
)

//go:embed assets
var assets embed.FS

type Data struct {
	EventName       string
	EventCategory   string
	EventStartsAt   time.Time
	EventVenueName  string
	EventCity       string
	EventBannerURL  string
	BuyerName       string
	TicketTypeName  string
	Quantity        int
	ReferenceNumber string
	CreatedAt       time.Time
	Total           int64
	Barcode         string
}

type rgb struct{ r, g, b int }

func hex(s string) rgb {
	var c rgb
	fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &c.r, &c.g, &c.b)
	return c
}

var (
	cPage       = hex("#eef1f5")
	cBorder     = hex("#e5e7eb")
	cHeader     = hex("#0a0f1f")
	cBadge      = hex("#16a34a")
	cWhite      = hex("#ffffff")
	cNavy       = hex("#0C2D57")
	cTitle      = hex("#2E2E33")
	cMeta       = hex("#303030")
	cFallback   = hex("#e5e7eb")
	cSeam       = hex("#94a3b8")
	cNotch      = hex("#cbd5e1")
	cDetailsBdr = hex("#d4dae3")
	cLabel      = hex("#6b7280")
	cValue      = hex("#171717")
	cBlue       = hex("#2a8af6")
	cMaroon     = hex("#9B2C5E")
	cInstr      = hex("#4b5563")
	cFooter     = hex("#1f3f6b")
	cFooterText = hex("#cbd5e1")
	gradTop     = []stop{{0, hex("#527599")}, {0.68, hex("#D4D7D9")}, {1, hex("#E5E5E5")}}
	gradBottom  = []stop{{0, hex("#E5E5E5")}, {0.32, hex("#D4D7D9")}, {1, hex("#527599")}}
)

type stop struct {
	at float64
	c  rgb
}

type font struct {
	family, style string
	ascent        float64
	core          bool
}

var (
	fMedium   = font{"pretendard", "", 0.952148438, false}
	fSemiBold = font{"pretendardsb", "", 0.952148438, false}
	fPlexBold = font{"plexkr", "", 1.085, false}
	fHelv     = font{"Helvetica", "", 0.9, true}
	fHelvBold = font{"Helvetica", "B", 0.9, true}
)

const (
	pageW = 595.28
	pageH = 841.89

	cardX  = 24.0
	cardW  = 547.28
	innerX = 25.0
	innerW = 545.28
	cardR  = 16.0

	helvLH = 1.1
)

var instructions = []struct {
	icon  func(p *pen)
	lines []string
}{
	{iconScan, []string{"Tunjukan QR Code", "ini saat masuk acara.", "Satu QR Code hanya", "berlaku untuk satu kali", "masuk."}},
	{iconNoPhoto, []string{"Dilarang melakukan", "screenshot atau", "membagikan tiket", "kepada orang lain."}},
	{iconClock, []string{"Datang lebih awal dan", "siapkan tiket sebelum", "menuju pintu masuk", "acara."}},
	{iconBag, []string{"Periksa barang", "bawaan anda sesuai", "dengan peraturan", "acara yang berlaku."}},
}

func Render(d Data, banners BannerLoader) ([]byte, error) { return render(d, banners, true) }

type doc struct {
	*fpdf.Fpdf
	tr func(string) string
}

func render(d Data, banners BannerLoader, compress bool) ([]byte, error) {
	pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt", Size: fpdf.SizeType{Wd: pageW, Ht: pageH}})
	pdf.SetCompression(compress)
	pdf.SetMargins(0, 0, 0)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetProducer("hectix", false)
	for _, f := range []struct{ fam, file string }{
		{fMedium.family, "Pretendard-Medium.ttf"},
		{fSemiBold.family, "Pretendard-SemiBold.ttf"},
		{fPlexBold.family, "IBMPlexSansKR-Bold.ttf"},
	} {
		b, err := assets.ReadFile("assets/" + f.file)
		if err != nil {
			return nil, err
		}
		pdf.AddUTF8FontFromBytes(f.fam, "", b)
	}
	for _, name := range []string{"header-bg.png", "logo-white.png", "mask-group.png"} {
		b, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return nil, err
		}
		pdf.RegisterImageOptionsReader(name, fpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(b))
	}
	pdf.AddPage()
	p := &doc{Fpdf: pdf, tr: pdf.UnicodeTranslatorFromDescriptor("")}
	banner := loadBanner(banners, d.EventBannerURL)
	if banner != nil {
		pdf.RegisterImageOptionsReader("banner", fpdf.ImageOptions{ImageType: banner.typ}, bytes.NewReader(banner.data))
	}

	p.fill(cPage)
	pdf.Rect(0, 0, pageW, pageH, "F")

	seamY := p.topCard(d, banner)
	p.bottomCard(d, seamY)

	if err := pdf.Error(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (p *doc) topCard(d Data, banner *bannerImage) float64 {
	const heroY = 89.0
	col := p.heroColumn(d, 0, true)
	heroH := 40 + max(120, col)
	topH := 1 + 64 + heroH
	seamY := cardX + topH

	p.fill(cWhite)
	p.roundRect(cardX, cardX, cardW, topH, cardR, cardR, 0, 0, "F")
	p.stroke(cBorder, 1)
	p.MoveTo(cardX+0.5, seamY)
	p.LineTo(cardX+0.5, cardX+cardR)
	p.arc(cardX+cardR, cardX+cardR, cardR-0.5, 180, 270)
	p.LineTo(cardX+cardW-cardR, cardX+0.5)
	p.arc(cardX+cardW-cardR, cardX+cardR, cardR-0.5, 270, 360)
	p.LineTo(cardX+cardW-0.5, seamY)
	p.DrawPath("D")

	p.ClipRoundedRectExt(cardX, cardX, cardW, topH, cardR, cardR, 0, 0, false)

	p.fill(cHeader)
	p.Rect(innerX, innerX, innerW, 64, "F")
	p.ClipRect(innerX, innerX, innerW, 64, false)
	bgH := innerW * 290 / 2384
	p.image("header-bg.png", innerX, innerX+(64-bgH)/2, innerW, bgH)
	p.ClipEnd()
	p.image("logo-white.png", innerX+20+(102-24*1890.0/446)/2, innerX+20, 24*1890.0/446, 24)
	badgeW := p.width(fMedium, 10, 0, "Valid Ticket") + 20
	badgeX := innerX + innerW - 20 - badgeW
	badgeH := 8 + 10*1.193359375
	badgeY := innerX + 32 - badgeH/2
	p.fill(cBadge)
	p.roundRect(badgeX, badgeY, badgeW, badgeH, badgeH/2, badgeH/2, badgeH/2, badgeH/2, "F")
	p.text(fMedium, 10, cWhite, 0, badgeX+10, badgeY+4, "Valid Ticket")

	p.gradient(innerX, heroY, innerW, heroH, gradTop)
	wmH := func(w float64) float64 { return w * 252 / 262 }
	p.image("mask-group.png", innerX+255.28, heroY-60+(250-wmH(250))/2, 250, wmH(250))
	p.image("mask-group.png", innerX+10, heroY+heroH-25-130+(130-wmH(130))/2, 130, wmH(130))
	bx, by := innerX+20, heroY+20
	if banner != nil {
		p.ClipRoundedRect(bx, by, 120, 120, 12, false)
		scale := max(120/banner.w, 120/banner.h)
		w, h := banner.w*scale, banner.h*scale
		p.image("banner", bx+(120-w)/2, by+(120-h)/2, w, h)
		p.ClipEnd()
	} else {
		p.fill(cFallback)
		p.roundRect(bx, by, 120, 120, 12, 12, 12, 12, "F")
	}
	p.heroColumn(d, heroY+20, false)

	p.notch(innerX, seamY, true)
	p.notch(innerX+innerW, seamY, false)
	p.ClipEnd()
	return seamY
}

func (p *doc) heroColumn(d Data, top float64, measureOnly bool) float64 {
	x := innerX + 156
	colW := innerW - 40 - 120 - 16
	y := top
	if d.EventCategory != "" {
		h := 10 + 12*1.193359375
		w := p.width(fMedium, 12, 0, d.EventCategory) + 28
		if !measureOnly {
			p.fill(cNavy)
			p.roundRect(x, y, w, h, h/2, h/2, h/2, h/2, "F")
			p.text(fMedium, 12, cWhite, 0, x+14, y+5, d.EventCategory)
		}
		y += h + 10
	}
	block := func(f font, size, lh, spacing float64, c rgb, s string, wrapW float64) {
		lines := p.wrap(f, size, spacing, s, wrapW)
		if !measureOnly {
			for i, ln := range lines {
				p.text(f, size, c, spacing, x, y+float64(i)*lh, ln)
			}
		}
		y += lh * float64(len(lines))
	}
	block(fPlexBold, 22, 30, -0.22, cTitle, d.EventName, colW)
	y += 8
	y += 6
	block(fMedium, 12, 18, 0.12, cMeta, "Tanggal", colW)
	block(fSemiBold, 14, 22, 0, cMeta, formatDateID(d.EventStartsAt), colW)
	y += 6
	block(fMedium, 12, 18, 0.12, cMeta, "Lokasi", colW)
	block(fSemiBold, 14, 22, 0, cMeta, location(d.EventVenueName, d.EventCity), colW)
	return y - top
}

func (p *doc) bottomCard(d Data, seamY float64) {
	const instrH = 26 + 5*12.325 + 22
	const footerH = 97.1

	fields := []struct {
		label, value string
		blue         bool
	}{
		{"Nama Pembeli", d.BuyerName, false},
		{"Jenis Tiket", d.TicketTypeName, false},
		{"Jumlah Tiket", fmt.Sprintf("%d Tiket", d.Quantity), false},
		{"Order ID", d.ReferenceNumber, true},
		{"Tanggal Pembelian", formatDateID(d.CreatedAt), false},
		{"Harga", formatRupiah(d.Total), false},
	}
	const detailsW = 289.28
	valueW := detailsW - 2 - 32
	type fieldLines struct{ label, value []string }
	var fl []fieldLines
	detailsH := 2 + 32 + 8*helvLH
	for _, f := range fields {
		l := fieldLines{p.wrap(fHelv, 10, 0, f.label, valueW), p.wrap(fHelvBold, 10, 0, f.value, valueW)}
		fl = append(fl, l)
		detailsH += float64(len(l.label)+len(l.value))*10*helvLH + 1 + 12
	}
	bodyH := max(detailsH, 200)
	lowerTopH := 40 + bodyH
	lbY := seamY + lowerTopH
	footerY := lbY + instrH
	bottomH := lowerTopH + instrH + footerH + 1

	p.fill(cWhite)
	p.roundRect(cardX, seamY, cardW, bottomH, 0, 0, cardR, cardR, "F")
	bottom := seamY + bottomH
	p.stroke(cBorder, 1)
	p.MoveTo(cardX+cardW-0.5, seamY)
	p.LineTo(cardX+cardW-0.5, bottom-cardR)
	p.arc(cardX+cardW-cardR, bottom-cardR, cardR-0.5, 0, 90)
	p.LineTo(cardX+cardR, bottom-0.5)
	p.arc(cardX+cardR, bottom-cardR, cardR-0.5, 90, 180)
	p.LineTo(cardX+0.5, seamY)
	p.DrawPath("D")

	p.ClipRoundedRectExt(cardX, seamY, cardW, bottomH, 0, 0, cardR, cardR, false)

	p.gradient(innerX, seamY, innerW, lowerTopH, gradTop)
	p.image("mask-group.png", innerX+160, lbY-120+(240-240*252.0/262)/2, 240, 240*252.0/262)
	p.seam(seamY)

	dx, dy := innerX+20, seamY+20
	p.fill(cWhite)
	p.roundRect(dx, dy, detailsW, bodyH, 14, 14, 14, 14, "F")
	p.stroke(cDetailsBdr, 1)
	p.roundRect(dx+0.5, dy+0.5, detailsW-1, bodyH-1, 13.5, 13.5, 13.5, 13.5, "D")
	fx, fy := dx+17, dy+17
	for i, f := range fields {
		for _, ln := range fl[i].label {
			p.text(fHelv, 10, cLabel, 0, fx, fy, ln)
			fy += 10 * helvLH
		}
		fy++
		c := cValue
		if f.blue {
			c = cBlue
		}
		for _, ln := range fl[i].value {
			p.text(fHelvBold, 10, c, 0, fx, fy, ln)
			fy += 10 * helvLH
		}
		fy += 12
	}
	p.text(fHelv, 8, cLabel, 0, fx, fy, "(Termasuk pajak & biaya layanan)")

	qx, qy := innerX+325.28, dy+(bodyH-200)/2
	p.qr(d.Barcode, qx+15, qy, 170)
	for i, ln := range []string{"Scan QR code di atas", "saat masuk acara"} {
		w := p.width(fHelv, 10, 0, ln)
		p.text(fHelv, 10, cLabel, 0, qx+(200-w)/2, qy+178+float64(i)*10*helvLH, ln)
	}

	p.gradient(innerX, lbY, innerW, instrH, gradBottom)
	p.seam(lbY)
	itemW := (innerW - 40 - 3*14) / 4
	for i, in := range instructions {
		ix, iy := innerX+20+float64(i)*(itemW+14), lbY+26
		pn := &pen{p, ix, iy, 22.0 / 24}
		p.SetLineCapStyle("round")
		p.SetLineJoinStyle("round")
		p.stroke(cMaroon, 1.6*pn.s)
		in.icon(pn)
		p.SetLineCapStyle("butt")
		p.SetLineJoinStyle("miter")
		for j, ln := range in.lines {
			p.text(fMedium, 8.5, cInstr, 0, ix+30, iy+float64(j)*8.5*1.45, ln)
		}
	}

	p.footer(footerY, footerH)
	p.ClipEnd()
}

func (p *doc) footer(y, h float64) {
	p.fill(cFooter)
	p.Rect(innerX, y, innerW, h, "F")

	lx, ly := innerX+24, y+27.45
	p.stroke(cWhite, 1.5*1.25)
	iconShield(&pen{p, lx, ly + 6.1, 1.25})
	p.text(fHelvBold, 11, cWhite, 0, lx+42, ly, "TIKET AMAN & TERPERCAYA")
	p.text(fHelv, 9, cFooterText, 0, lx+42, ly+16.1, "Tiket ini dienkripsi secara digital dan")
	p.text(fHelv, 9, cFooterText, 0, lx+42, ly+16.1+13.05, "hanya berlaku untuk satu kali penggunaan.")

	email, site := "Email : meledaktiket@gmail.com", "Website : Hectix.id/bantuan"
	rw := max(p.width(fHelvBold, 11, 0, "Butuh Bantuan?"),
		40+max(p.width(fHelv, 9, 0, email), p.width(fHelv, 9, 0, site)))
	rx, ry := innerX+innerW-24-rw, y+22
	p.text(fHelvBold, 11, cWhite, 0, rx, ry, "Butuh Bantuan?")
	p.stroke(cWhite, 1.5*1.25)
	iconHeadset(&pen{p, rx, ry + 23.1, 1.25})
	p.text(fHelv, 9, cFooterText, 0, rx+40, ry+23.1+1.95, email)
	p.text(fHelv, 9, cFooterText, 0, rx+40, ry+23.1+1.95+13.05, site)
}

func (p *doc) seam(y float64) {
	p.stroke(cSeam, 1.5)
	p.SetDashPattern([]float64{3, 1.8}, 0)
	p.Line(innerX+20, y+0.75, innerX+innerW-20, y+0.75)
	p.SetDashPattern([]float64{}, 0)
	p.notch(innerX, y, true)
	p.notch(innerX+innerW, y, false)
}

func (p *doc) notch(cx, cy float64, left bool) {
	p.fill(cPage)
	p.Circle(cx, cy, 9, "F")
	p.stroke(cNotch, 1)
	p.MoveTo(cx, cy-9)
	if left {
		p.arc(cx, cy, 9, 270, 450)
	} else {
		p.arc(cx, cy, 9, 270, 90)
	}
	p.DrawPath("D")
}

func (p *doc) gradient(x, y, w, h float64, stops []stop) {
	p.SetAlpha(0.5, "Normal")
	for i := 0; i+1 < len(stops); i++ {
		a, b := stops[i], stops[i+1]
		y0 := snap(y + h*a.at)
		y1 := snap(y + h*b.at)
		p.LinearGradient(x, y0, w, y1-y0, a.c.r, a.c.g, a.c.b, b.c.r, b.c.g, b.c.b, 0, 1, 0, 0)
	}
	p.SetAlpha(1, "Normal")
}

func snap(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

func (p *doc) roundRect(x, y, w, h, rTL, rTR, rBR, rBL float64, style string) {
	p.RoundedRectExt(x, y, w, h, rTL, rTR, rBR, rBL, style)
	p.RawWriteStr("Q")
}

func (p *doc) fill(c rgb) { p.SetFillColor(c.r, c.g, c.b) }

func (p *doc) stroke(c rgb, w float64) {
	p.SetDrawColor(c.r, c.g, c.b)
	p.SetLineWidth(w)
}

func (p *doc) image(name string, x, y, w, h float64) {
	p.ImageOptions(name, x, y, w, h, false, fpdf.ImageOptions{AllowNegativePosition: true}, 0, "")
}

func (p *doc) useFont(f font, size float64, s string) (string, font) {
	if f.core {
		if t := p.tr(s); !lossy(s, t) {
			p.SetFont(f.family, f.style, size)
			return t, f
		}
		bold := f.style == "B"
		f = fMedium
		if bold {
			f = fSemiBold
		}
	}
	p.SetFont(f.family, f.style, size)
	return s, f
}

func lossy(s, t string) bool {
	return strings.Count(t, ".") != strings.Count(s, ".")
}

func (p *doc) width(f font, size, spacing float64, s string) float64 {
	s, _ = p.useFont(f, size, s)
	return p.GetStringWidth(s) + spacing*float64(utf8.RuneCountInString(s))
}

func (p *doc) text(f font, size float64, c rgb, spacing, x, top float64, s string) {
	s, f = p.useFont(f, size, s)
	p.SetTextColor(c.r, c.g, c.b)
	base := top + f.ascent*size
	if spacing == 0 {
		p.Text(x, base, s)
		return
	}
	p.RawWriteStr(fmt.Sprintf("%.4f Tc", spacing))
	p.Text(x, base, s)
	p.RawWriteStr("0 Tc")
}

func (p *doc) wrap(f font, size, spacing float64, s string, maxW float64) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			try := word
			if line != "" {
				try = line + " " + word
			}
			if p.width(f, size, spacing, try) <= maxW || line == "" && p.width(f, size, spacing, word) <= maxW {
				line = try
				continue
			}
			if line != "" {
				out = append(out, line)
			}
			line = ""
			for _, r := range word {
				if line != "" && p.width(f, size, spacing, line+string(r)) > maxW {
					out = append(out, line)
					line = ""
				}
				line += string(r)
			}
		}
		out = append(out, line)
	}
	return out
}
