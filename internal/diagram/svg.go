package diagram

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

const (
	margin      = 12.0
	badgeRadius = 7.0
	// badgeSpace is the room a badge takes after the text it follows.
	badgeSpace = 4 + 2*badgeRadius
)

// style is the size and weight a label is set in; leading is its line height.
type style struct{ size, weight, leading float64 }

var (
	nameStyle   = style{12, 700, 15}
	textStyle   = style{11, 400, 14}
	noteStyle   = style{11, 650, 14}
	barStyle    = style{12, 400, 15}
	valueStyle  = style{12, 650, 15}
	legendStyle = style{11, 400, 14}
)

// num formats a coordinate to a hundredth of a pixel.
func num(v float64) string {
	return strconv.FormatFloat(math.Round(v*100)/100, 'f', -1, 64)
}

// wrap breaks label into lines no wider than width, at spaces, or inside a
// word longer than the line.
func (f *Font) wrap(label string, width float64, s style) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(label) {
		if line != "" && f.Width(line+" "+word, s.size, s.weight) <= width {
			line += " " + word
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		line = ""
		for _, r := range word {
			if line != "" && f.Width(line+string(r), s.size, s.weight) > width {
				lines = append(lines, line)
				line = ""
			}
			line += string(r)
		}
	}
	if line != "" || len(lines) == 0 {
		lines = append(lines, line)
	}
	return lines
}

// widest is the width of the longest of lines.
func (f *Font) widest(lines []string, s style) float64 {
	w := 0.0
	for _, l := range lines {
		w = max(w, f.Width(l, s.size, s.weight))
	}
	return w
}

// canvas collects the elements of one SVG.
type canvas struct {
	b strings.Builder
}

func (c *canvas) printf(format string, args ...any) { fmt.Fprintf(&c.b, format, args...) }

// text writes lines with their first baseline at y. anchor is start, middle
// or end.
func (c *canvas) text(x, y float64, lines []string, s style, anchor, class string) {
	c.printf(`<text class="%s" x="%s" y="%s" font-size="%s" font-weight="%s" text-anchor="%s">`, class, num(x), num(y), num(s.size), num(s.weight), anchor)
	for i, l := range lines {
		if len(lines) == 1 {
			c.b.WriteString(html.EscapeString(l))
			break
		}
		c.printf(`<tspan x="%s" y="%s">%s</tspan>`, num(x), num(y+float64(i)*s.leading), html.EscapeString(l))
	}
	c.b.WriteString(`</text>`)
}

// badge draws the circled number n centred on x, y.
func (c *canvas) badge(x, y float64, n int) {
	c.printf(`<circle class="dg-badge" cx="%s" cy="%s" r="%s"/><text class="dg-badge-text" x="%s" y="%s" font-size="10" font-weight="800" text-anchor="middle">%d</text>`,
		num(x), num(y), num(badgeRadius), num(x), num(y+3.5), n)
}

// legend draws one entry per tone the drawing uses, wrapped to width, from
// y down, and returns the bottom of the legend.
func (c *canvas) legend(f *Font, used [4]bool, variant string, options []Option, y, width float64) float64 {
	if !used[change] && !used[risk] && !used[problem] {
		return y
	}
	labels := [4]string{"invariato", "cambiamento", "rischio", "problema"}
	if variant == Now {
		labels[neutral] = "attuale"
	}
	for _, o := range options {
		if o.ID == variant {
			labels[change] = "cambia con " + o.Key
		}
	}
	x := margin
	y += legendStyle.leading
	for _, t := range []tone{problem, risk, change, neutral} {
		if !used[t] {
			continue
		}
		w := 18 + 6 + f.Width(labels[t], legendStyle.size, legendStyle.weight)
		if x > margin && x+w > width-margin {
			x, y = margin, y+legendStyle.leading+4
		}
		c.printf(`<g class="%s"><line class="dg-swatch" x1="%s" y1="%s" x2="%s" y2="%s"/></g>`, toneClass[t], num(x), num(y-4), num(x+18), num(y-4))
		c.text(x+24, y, []string{labels[t]}, legendStyle, "start", "dg-legend")
		x += w + 16
	}
	return y + 4
}

// svg wraps the drawing of width by height.
func (c *canvas) svg(title string, width, height float64) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" class="diagram-svg" viewBox="0 0 %[1]s %[2]s" width="%[1]s" height="%[2]s" role="img" aria-label="%[3]s">%[4]s</svg>`,
		num(math.Ceil(width)), num(math.Ceil(height)), html.EscapeString(title), c.b.String())
}
