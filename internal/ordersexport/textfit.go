package ordersexport

import (
	"math"
	"strings"
	"sync"
	"unicode"

	"github.com/go-pdf/fpdf"
)

type hfont struct {
	family, style string
	widths        map[rune]float64
	kern          map[[2]rune]int
}

var (
	helvOnce    sync.Once
	helvRegular = &hfont{family: "Helvetica", kern: kernRegular}
	helvBold    = &hfont{family: "Helvetica", style: "B", kern: kernBold}
	winAnsi     func(string) string
)

func initHelvetica() {
	helvOnce.Do(func() {
		pdf := fpdf.NewCustom(&fpdf.InitType{UnitStr: "pt"})
		winAnsi = pdf.UnicodeTranslatorFromDescriptor("")
		for _, f := range []*hfont{helvRegular, helvBold} {
			pdf.SetFont(f.family, f.style, 1000)
			f.widths = map[rune]float64{}
			for r := rune(0x20); r <= 0x2122; r++ {
				b := winAnsi(string(r))
				if r >= 0x7f && r < 0xa0 || len(b) != 1 || (b == "." && r != '.') {
					continue
				}
				f.widths[r] = pdf.GetStringWidth(b)
			}
		}
	})
}

func (f *hfont) advances(rs []rune, size, spacing float64) []float64 {
	adv := make([]float64, len(rs))
	starts := runStarts(rs)
	for i, r := range rs {
		a := f.widths[r]
		if i+1 < len(rs) && !starts[i+1] {
			a += float64(f.kern[[2]rune{r, rs[i+1]}])
		}
		adv[i] = a*size/1000 + spacing
	}
	return adv
}

func (f *hfont) width(s string, size, spacing float64) float64 {
	w := 0.0
	for _, a := range f.advances([]rune(s), size, spacing) {
		w += a
	}
	return w
}

var (
	scriptMu    sync.Mutex
	scriptCache = map[rune]string{}
)

func scriptOf(r rune) string {
	if unicode.In(r, unicode.Common, unicode.Inherited) {
		return ""
	}
	scriptMu.Lock()
	defer scriptMu.Unlock()
	if s, ok := scriptCache[r]; ok {
		return s
	}
	s := ""
	for name, t := range unicode.Scripts {
		if name != "Common" && name != "Inherited" && unicode.Is(t, r) {
			s = name
			break
		}
	}
	scriptCache[r] = s
	return s
}

func runStarts(rs []rune) []bool {
	starts := make([]bool, len(rs))
	last := ""
	for i, r := range rs {
		if sc := scriptOf(r); sc != "" && sc != last {
			starts[i] = i > 0
			last = sc
		}
	}
	return starts
}

type kpNode struct {
	kind                   byte
	start, end             int
	width, stretch, shrink float64
	penalty                float64
	flagged                float64
}

const kpInfinity = 10000.0

func isJSSpace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

type lineSpan struct {
	start, end int
	hyphen     bool
}

func wrapLines(rs []rune, adv []float64, avail float64) []lineSpan {
	sum := func(a, b int) float64 {
		w := 0.0
		for i := a; i < b; i++ {
			w += adv[i]
		}
		return w
	}
	var syl [][2]int
	for i := 0; i < len(rs); {
		j := i
		sp := rs[i] == ' '
		for j < len(rs) && (rs[j] == ' ') == sp {
			j++
		}
		syl = append(syl, [2]int{i, j})
		i = j
	}
	var nodes []kpNode
	for k, s := range syl {
		w := sum(s[0], s[1])
		if strings.TrimFunc(string(rs[s[0]:s[1]]), isJSSpace) == "" {
			nodes = append(nodes, kpNode{kind: 'g', start: s[0], end: s[1], width: w, stretch: w * 3 / 6, shrink: w * 3 / 9})
			continue
		}
		hyphenated := k+1 >= len(syl) || string(rs[syl[k+1][0]:syl[k+1][1]]) != " "
		nodes = append(nodes, kpNode{kind: 'b', start: s[0], end: s[1], width: w})
		if k+1 < len(syl) && hyphenated {
			nodes = append(nodes, kpNode{kind: 'p', width: 5, penalty: 600, flagged: 1})
		}
	}
	nodes = append(nodes,
		kpNode{kind: 'g', start: len(rs), end: len(rs), stretch: kpInfinity},
		kpNode{kind: 'p', penalty: -kpInfinity, flagged: 1})

	widths := []float64{avail, avail}
	tol := 4.0
	breaks := knuthPlass(nodes, widths, tol)
	for len(breaks) == 0 && tol < 50 {
		tol += 5
		breaks = knuthPlass(nodes, widths, tol)
	}
	if len(breaks) == 0 || (len(breaks) == 1 && breaks[0] == 0) {
		breaks = bestFit(nodes, widths)
	}

	var lines []lineSpan
	start := 0
	for _, bp := range breaks[1:] {
		if bp == len(nodes)-1 {
			continue
		}
		if n := nodes[bp]; n.kind == 'p' {
			end := nodes[bp-1].end
			lines = append(lines, lineSpan{start, end, true})
			start = end
		} else {
			lines = append(lines, lineSpan{start, n.end, false})
			start = n.end
		}
	}
	return append(lines, lineSpan{start, len(rs), false})
}

type kpBreak struct {
	position     int
	demerits     float64
	line         int
	fitnessClass int
	totals       [3]float64
	previous     *kpBreak
}

func knuthPlass(nodes []kpNode, lineLengths []float64, tolerance float64) []int {
	active := []*kpBreak{{}}
	var sum [3]float64

	computeCost := func(end int, a *kpBreak, currentLine int) float64 {
		width := sum[0] - a.totals[0]
		lineLength := lineLengths[len(lineLengths)-1]
		if currentLine < len(lineLengths) {
			lineLength = lineLengths[currentLine-1]
		}
		if nodes[end].kind == 'p' {
			width += nodes[end].width
		}
		if width < lineLength {
			if st := sum[1] - a.totals[1]; st > 0 {
				return (lineLength - width) / st
			}
			return kpInfinity
		}
		if width > lineLength {
			if sh := sum[2] - a.totals[2]; sh > 0 {
				return (lineLength - width) / sh
			}
			return kpInfinity
		}
		return 0
	}
	computeSum := func(idx int) [3]float64 {
		r := sum
		for i := idx; i < len(nodes); i++ {
			n := nodes[i]
			if n.kind == 'g' {
				r[0] += n.width
				r[1] += n.stretch
				r[2] += n.shrink
			} else if n.kind == 'b' || (n.kind == 'p' && n.penalty == -kpInfinity && i > idx) {
				break
			}
		}
		return r
	}
	mainLoop := func(node kpNode, index int) {
		ai := 0
		for ai < len(active) {
			currentLine := 0
			type cand struct {
				a        *kpBreak
				demerits float64
			}
			cands := [4]cand{{nil, math.Inf(1)}, {nil, math.Inf(1)}, {nil, math.Inf(1)}, {nil, math.Inf(1)}}
			for ai < len(active) {
				a := active[ai]
				currentLine = a.line + 1
				ratio := computeCost(index, a, currentLine)
				removed := false
				if ratio < -1 || (node.kind == 'p' && node.penalty == -kpInfinity) {
					active = append(active[:ai], active[ai+1:]...)
					removed = true
				}
				if ratio >= -1 && ratio <= tolerance {
					badness := 100 * math.Pow(math.Abs(ratio), 3)
					var dem float64
					switch {
					case node.kind == 'p' && node.penalty >= 0:
						dem = math.Pow(10+badness, 2) + math.Pow(node.penalty, 2)
					case node.kind == 'p' && node.penalty != -kpInfinity:
						dem = math.Pow(10+badness, 2) - math.Pow(node.penalty, 2)
					default:
						dem = math.Pow(10+badness, 2)
					}
					if node.kind == 'p' && nodes[a.position].kind == 'p' {
						dem += 100 * node.flagged * nodes[a.position].flagged
					}
					var class int
					switch {
					case ratio < -0.5:
						class = 0
					case ratio <= 0.5:
						class = 1
					case ratio <= 1:
						class = 2
					default:
						class = 3
					}
					if abs(class-a.fitnessClass) > 1 {
						dem += 3000
					}
					dem += a.demerits
					if dem < cands[class].demerits {
						cands[class] = cand{a, dem}
					}
				}
				if !removed {
					ai++
				}
				if ai < len(active) && active[ai].line >= currentLine {
					break
				}
			}
			tmpSum := computeSum(index)
			for class, c := range cands {
				if math.IsInf(c.demerits, 1) {
					continue
				}
				nb := &kpBreak{position: index, demerits: c.demerits, line: c.a.line + 1, fitnessClass: class, totals: tmpSum, previous: c.a}
				active = append(active[:ai], append([]*kpBreak{nb}, active[ai:]...)...)
				ai++
			}
		}
	}
	for i, n := range nodes {
		switch n.kind {
		case 'b':
			sum[0] += n.width
		case 'g':
			if i > 0 && nodes[i-1].kind == 'b' {
				mainLoop(n, i)
			}
			sum[0] += n.width
			sum[1] += n.stretch
			sum[2] += n.shrink
		case 'p':
			if n.penalty != kpInfinity {
				mainLoop(n, i)
			}
		}
	}
	if len(active) == 0 {
		return nil
	}
	best := &kpBreak{demerits: math.Inf(1)}
	for _, a := range active {
		if a.demerits < best.demerits {
			best = a
		}
	}
	var out []int
	for b := best; b != nil; b = b.previous {
		out = append(out, b.position)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func bestFit(nodes []kpNode, widths []float64) []int {
	count, lineNumber := 0, 0
	sub := nodes
	bps := []int{0}
	for len(sub) > 0 {
		bp := nextBreakpoint(sub, widths, lineNumber)
		if bp < 0 {
			break
		}
		count += bp
		bps = append(bps, count)
		sub = sub[bp+1:]
		count++
		lineNumber++
	}
	return bps
}

func nextBreakpoint(sub []kpNode, widths []float64, lineNumber int) int {
	position := -1
	minBad := math.Inf(1)
	var w, st, sh float64
	lineLength := widths[min(lineNumber, len(widths)-1)]
	ratioOf := func(n kpNode) float64 {
		if w < lineLength {
			if n.kind != 'g' || n.stretch == 0 {
				return kpInfinity
			}
			if st-n.stretch > 0 {
				return (lineLength - w) / st
			}
			return kpInfinity
		}
		if w > lineLength {
			if n.kind != 'g' || n.shrink == 0 {
				return kpInfinity
			}
			if sh-n.shrink > 0 {
				return (lineLength - w) / sh
			}
			return kpInfinity
		}
		return 0
	}
	hyphenWidth := 0.0
	for i, n := range sub {
		if n.kind == 'b' {
			w += n.width
		}
		if n.kind == 'g' {
			w += n.width
			st += n.stretch
			sh += n.shrink
		}
		j := i + 1
		for ; j < len(sub); j++ {
			if sub[j].kind != 'g' && sub[j].kind != 'p' {
				break
			}
		}
		hyphenWidth = 0
		if sub[j-1].kind == 'p' {
			hyphenWidth = sub[j-1].width
		}
		if w-sh+hyphenWidth > lineLength {
			if position < 0 {
				k := i
				if i == 0 {
					k = 1
				}
				for k < len(sub) && (sub[k].kind == 'g' || sub[k].kind == 'p') {
					k++
				}
				position = k - 1
			}
			break
		}
		if n.kind == 'p' || n.kind == 'g' {
			r := ratioOf(n)
			pen := 0.0
			if n.kind == 'p' {
				pen = n.penalty
			}
			bad := 100*math.Pow(math.Abs(r), 3) + pen
			if minBad >= bad {
				position = i
				minBad = bad
			}
		}
	}
	if w-sh+hyphenWidth > lineLength {
		return position
	}
	return -1
}

type glyphLine struct {
	rs     []rune
	adv    []float64
	starts []bool
}

func (g glyphLine) slice(a, b int) glyphLine {
	return glyphLine{g.rs[a:b:b], g.adv[a:b:b], g.starts[a:b:b]}
}

func (g glyphLine) appendGlyph(r rune, size float64) glyphLine {
	return glyphLine{append(g.rs, r), append(g.adv, 400*size/1000), append(g.starts, false)}
}

func (g glyphLine) String() string { return string(g.rs) }

func (g glyphLine) width() float64 {
	w := 0.0
	for _, a := range g.adv {
		w += a
	}
	return w
}

func layoutLines(s string, f *hfont, size, spacing, avail float64, maxLines int, ellipsis bool) []glyphLine {
	initHelvetica()
	var out []glyphLine
	for _, para := range strings.SplitAfter(s, "\n") {
		if para == "" {
			continue
		}
		rs := []rune(para)
		adv := f.advances(rs, size, spacing)
		all := glyphLine{rs, adv, runStarts(rs)}
		spans := wrapLines(rs, adv, avail)
		for i, sp := range spans {
			g := all.slice(sp.start, sp.end)
			if sp.hyphen {
				g = g.appendGlyph('-', size)
			}
			if n := len(g.rs); n > 0 && g.rs[n-1] == '\n' {
				g = g.slice(0, n-1)
			}
			truncated := maxLines > 0 && len(out)+1 == maxLines && i < len(spans)-1
			if truncated && ellipsis {
				a, b := 0, len(g.rs)
				for a < b && isJSSpace(g.rs[a]) {
					a++
				}
				for b > a && isJSSpace(g.rs[b-1]) {
					b--
				}
				g = g.slice(a, b).appendGlyph('…', size)
			}
			for len(g.rs) > 0 && g.rs[0] == ' ' {
				g = g.slice(1, len(g.rs))
			}
			out = append(out, g)
			if maxLines > 0 && len(out) == maxLines {
				return out
			}
		}
	}
	if len(out) == 0 {
		out = []glyphLine{{}}
	}
	return out
}

func fitCell(s string, f *hfont, size, avail float64, ellipsis bool) glyphLine {
	return layoutLines(s, f, size, 0, avail, 1, ellipsis)[0]
}

func justify(g glyphLine, gap float64) []float64 {
	rs := g.rs
	type factor struct {
		before, after float64
		priority      int
	}
	fs := make([]factor, len(rs))
	runStart := 0
	for i, r := range rs {
		if g.starts[i] {
			runStart = i
		}
		runLast := i == len(rs)-1 || g.starts[i+1]
		if r == ' ' {
			fs[i] = factor{-0.5, -0.5, 1}
			if runLast {
				fs[i].before = 0
				if i > runStart {
					fs[i-1].after = 0
				}
			}
		} else {
			fs[i] = factor{-0.04296875, -0.04296875, 2}
		}
	}
	fs[0].before = 0
	fs[len(fs)-1].after = 0

	var pr [4]float64
	for _, f := range fs {
		pr[f.priority] += f.before + f.after
	}
	rem := gap
	p := 0
	for ; p <= 3; p++ {
		if pr[p] == 0 {
			continue
		}
		if math.Abs(rem) <= math.Abs(pr[p]) {
			pr[p] = rem / pr[p]
			rem = 0
			break
		}
		rem -= pr[p]
		pr[p] = 1
	}
	for q := p + 1; q <= 3; q++ {
		pr[q] = 0
	}
	d := make([]float64, len(fs))
	for i, f := range fs {
		d[i] = f.after * pr[f.priority]
		if i+1 < len(fs) {
			d[i] += fs[i+1].before * pr[fs[i+1].priority]
		}
	}
	return d
}
