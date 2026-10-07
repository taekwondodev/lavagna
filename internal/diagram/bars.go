package diagram

import (
	"regexp"
	"strconv"
)

const (
	maxBars    = 12
	barLabel   = 200.0 // a bar label wraps beyond this
	barArea    = 280.0 // the longest bar
	barHeight  = 14.0
	barGap     = 10.0
	barSpacing = 10.0 // between rows
)

type bar struct {
	element
	value float64
	text  string // the value as written, with its unit
}

var barRE = regexp.MustCompile(`^(.+?)\s*:\s*(-?[0-9]+(?:\.[0-9]+)?)(?:\s+(\S.*))?$`)

func (p *parser) barList(n int, lines []Line) {
	unitLine := 0
	for _, l := range lines {
		e, rest := p.element(l.N, l.Text)
		m := barRE.FindStringSubmatch(rest)
		if m == nil {
			p.fail(l.N, "a bar is Label: value unit, for example Scrittura: 4 ms")
			continue
		}
		e.label = m[1]
		p.label(l.N, "bar label", e.label)
		value, err := strconv.ParseFloat(m[2], 64)
		if err != nil || value < 0 {
			p.fail(l.N, "bar value %s must be a non-negative number", m[2])
			continue
		}
		switch {
		case unitLine == 0:
			p.d.unit, unitLine = m[3], l.N
		case m[3] != p.d.unit:
			p.fail(l.N, "unit %q differs from %q on line %d: every bar shares one unit", m[3], p.d.unit, unitLine)
		}
		text := m[2]
		if m[3] != "" {
			text += " " + m[3]
		}
		p.d.bars = append(p.d.bars, bar{element: e, value: value, text: text})
	}
	switch {
	case len(p.d.bars) > maxBars:
		p.fail(p.d.bars[maxBars].line, "::: bars has at most %d bars", maxBars)
	case len(p.d.bars) == 0 && len(p.errs) == 0:
		p.fail(n, "::: bars needs at least one bar")
	}
}

// drawBars draws the bars of variant on the scale and columns of every
// variant, so switching variants keeps the geometry.
func (d *Diagram) drawBars(f *Font, variant string) string {
	labels := make([]wrapped, len(d.bars))
	labelW, valueW, top := 0.0, 0.0, 0.0
	for i, b := range d.bars {
		labels[i] = f.measure(element{label: b.label}, barLabel, barStyle)
		labelW = max(labelW, labels[i].width)
		w := f.Width(b.text, valueStyle.size, valueStyle.weight)
		if b.badge > 0 {
			w += badgeSpace
		}
		valueW = max(valueW, w)
		top = max(top, b.value)
	}
	scale := 0.0
	if top > 0 {
		scale = barArea / top
	}
	start := margin + labelW + barGap
	width := start + barArea + 6 + valueW + margin

	var used [4]bool
	cv := &canvas{}
	y := margin
	for i, b := range d.bars {
		if !b.in(variant) {
			continue
		}
		t := b.toneIn(variant)
		used[t] = true
		h := max(float64(len(labels[i].lines))*barStyle.leading, barHeight)
		mid := y + h/2
		cv.printf(`<g class="%s">`, toneClass[t])
		first := mid - float64(len(labels[i].lines)-1)*barStyle.leading/2 + 4
		cv.text(start-barGap, first, labels[i].lines, barStyle, "end", "dg-name")
		cv.printf(`<rect class="dg-bar" x="%s" y="%s" width="%s" height="%s" rx="2"/>`, num(start), num(mid-barHeight/2), num(b.value*scale), num(barHeight))
		end := start + b.value*scale + 6
		cv.text(end, mid+4, []string{b.text}, valueStyle, "start", "dg-text")
		if b.badge > 0 {
			cv.badge(end+f.Width(b.text, valueStyle.size, valueStyle.weight)+badgeSpace-badgeRadius, mid, b.badge)
		}
		cv.b.WriteString(`</g>`)
		y += h + barSpacing
	}
	height := cv.legend(f, used, variant, d.options, y-barSpacing+6, width) + margin
	return cv.svg(d.title, width, height)
}
