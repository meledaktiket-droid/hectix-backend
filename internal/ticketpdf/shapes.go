package ticketpdf

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

func (p *doc) arc(cx, cy, r, a0, a1 float64) {
	n := int(math.Ceil(math.Abs(a1-a0) / 90))
	step := (a1 - a0) / float64(n) * math.Pi / 180
	k := 4.0 / 3 * math.Tan(step/4)
	t := a0 * math.Pi / 180
	for range n {
		t1 := t + step
		x0, y0 := cx+r*math.Cos(t), cy+r*math.Sin(t)
		x1, y1 := cx+r*math.Cos(t1), cy+r*math.Sin(t1)
		p.CurveBezierCubicTo(
			x0-k*r*math.Sin(t), y0+k*r*math.Cos(t),
			x1+k*r*math.Sin(t1), y1-k*r*math.Cos(t1),
			x1, y1)
		t = t1
	}
}

type pen struct {
	p       *doc
	x, y, s float64
}

func (n *pen) path(d string) {
	f := strings.Fields(d)
	num := func(i int) float64 { v, _ := strconv.ParseFloat(f[i], 64); return v }
	px := func(i int) (float64, float64) { return n.x + num(i)*n.s, n.y + num(i+1)*n.s }
	for i := 0; i < len(f); {
		switch f[i] {
		case "M":
			n.p.MoveTo(px(i + 1))
			i += 3
		case "L":
			n.p.LineTo(px(i + 1))
			i += 3
		case "C":
			x1, y1 := px(i + 1)
			x2, y2 := px(i + 3)
			x, y := px(i + 5)
			n.p.CurveBezierCubicTo(x1, y1, x2, y2, x, y)
			i += 7
		case "Z":
			n.p.ClosePath()
			i++
		default:
			n.p.SetError(fmt.Errorf("ticketpdf: bad path token %q", f[i]))
			return
		}
	}
	n.p.DrawPath("D")
}

func iconScan(n *pen) {
	n.path("M 4 9 L 4 4 L 9 4")
	n.path("M 15 4 L 20 4 L 20 9")
	n.path("M 20 15 L 20 20 L 15 20")
	n.path("M 9 20 L 4 20 L 4 15")
	n.path("M 4 12 L 20 12")
}

func iconNoPhoto(n *pen) {
	n.path("M 4 8 L 8 8 L 9.5 6 L 14.5 6 L 16 8 L 20 8 L 20 18 L 4 18 Z")
	n.path("M 9.5 13 C 9.5 14.380712 10.619288 15.5 12 15.5 C 13.380712 15.5 14.5 14.380712 14.5 13 " +
		"C 14.5 11.619288 13.380712 10.5 12 10.5 C 10.619288 10.5 9.5 11.619288 9.5 13")
	n.path("M 3 3 L 21 21")
}

func iconClock(n *pen) {
	n.path("M 12 3.5 C 7.30558 3.5 3.5 7.30558 3.5 12 C 3.5 16.69442 7.30558 20.5 12 20.5 " +
		"C 16.69442 20.5 20.5 16.69442 20.5 12 C 20.5 7.30558 16.69442 3.5 12 3.5")
	n.path("M 12 7 L 12 12 L 15.5 14")
}

func iconBag(n *pen) {
	n.path("M 5 8 L 19 8 L 18 20 L 6 20 Z")
	n.path("M 9 8 L 9 6 C 9 4 15 4 15 6 L 15 8")
}

func iconShield(n *pen) {
	n.p.SetLineJoinStyle("round")
	n.path("M 12 2.5 L 20 5.5 L 20 11 C 20 16 16.4 19.4 12 21 C 7.6 19.4 4 16 4 11 L 4 5.5 Z")
	n.p.SetLineCapStyle("round")
	n.path("M 8.5 12 L 11 14.5 L 15.5 9.5")
	n.p.SetLineCapStyle("butt")
	n.p.SetLineJoinStyle("miter")
}

func iconHeadset(n *pen) {
	n.p.SetLineCapStyle("round")
	n.path("M 5 14 L 5 12 C 5 8.134007 8.134007 5 12 5 C 15.865993 5 19 8.134007 19 12 L 19 14")
	n.p.SetLineCapStyle("butt")
	n.path("M 5.1 13 C 5.983656 13 6.7 13.716344 6.7 14.6 L 6.7 17 C 6.7 17.883656 5.983656 18.6 5.1 18.6 " +
		"C 4.216344 18.6 3.5 17.883656 3.5 17 L 3.5 14.6 C 3.5 13.716344 4.216344 13 5.1 13 Z")
	n.path("M 18.9 13 C 19.783656 13 20.5 13.716344 20.5 14.6 L 20.5 17 C 20.5 17.883656 19.783656 18.6 18.9 18.6 " +
		"C 18.016344 18.6 17.3 17.883656 17.3 17 L 17.3 14.6 C 17.3 13.716344 18.016344 13 18.9 13 Z")
	n.p.SetLineCapStyle("round")
	n.p.SetLineJoinStyle("round")
	n.path("M 19 18.6 L 19 19.5 C 19 21.156854 17.656854 22.5 16 22.5 L 13.5 22.5")
	n.p.SetLineCapStyle("butt")
	n.p.SetLineJoinStyle("miter")
}

func (p *doc) qr(content string, x, y, size float64) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		p.SetError(err)
		return
	}
	q.DisableBorder = true
	bm := q.Bitmap()
	m := size / float64(len(bm)+2)
	p.fill(cWhite)
	p.Rect(x, y, size, size, "F")
	p.SetFillColor(0, 0, 0)
	for r, row := range bm {
		for c := 0; c < len(row); {
			if !row[c] {
				c++
				continue
			}
			start := c
			for c < len(row) && row[c] {
				c++
			}
			x0, y0 := x+float64(start+1)*m, y+float64(r+1)*m
			x1, y1 := x+float64(c+1)*m, y0+m
			p.MoveTo(x0, y0)
			p.LineTo(x1, y0)
			p.LineTo(x1, y1)
			p.LineTo(x0, y1)
			p.ClosePath()
		}
	}
	p.DrawPath("F")
}
