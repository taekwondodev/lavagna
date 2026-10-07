package diagram

import (
	"regexp"
	"slices"
	"strings"
)

const (
	maxParticipants  = 8
	maxSequenceLines = 40

	boxPadX, boxPadY = 12.0, 8.0
	boxMinWidth      = 80.0
	nameWidth        = 140.0 // a participant name wraps beyond this
	columnGap        = 32.0
	messageWidth     = 200.0
	labelPad         = 16.0 // between a message label and the lifelines it spans
	notePadX         = 10.0
	notePadY         = 5.0
	noteWidth        = 180.0
	loopWidth        = 24.0
	loopHeight       = 14.0
	arrowHead        = 8.0
	stepGap          = 14.0
)

// step is a message or, with note set, a note on participant from.
type step struct {
	element
	note     bool
	from, to int
	dashed   bool
}

var (
	messageRE = regexp.MustCompile(`^(.+?)\s*(-->|->)\s*([^:]+?)\s*(?::\s*(.*))?$`)
	noteRE    = regexp.MustCompile(`^note\s+([^:]+?)\s*:\s*(.*)$`)
)

func (p *parser) sequence(n int, lines []Line) {
	if len(lines) > maxSequenceLines {
		p.fail(lines[maxSequenceLines].N, "a sequence has at most %d lines", maxSequenceLines)
		return
	}
	declared := false
	if len(lines) > 0 && !noteRE.MatchString(lines[0].Text) && !messageRE.MatchString(lines[0].Text) {
		declared = true
		for _, segment := range strings.Split(lines[0].Text, "|") {
			e, name := p.element(lines[0].N, strings.TrimSpace(segment))
			p.label(e.line, "participant", name)
			e.label = name
			if slices.ContainsFunc(p.d.participants, func(o element) bool { return o.label == name }) {
				p.fail(e.line, "participant %q is listed twice", name)
			}
			p.d.participants = append(p.d.participants, e)
		}
		lines = lines[1:]
	}
	participant := func(n int, name string) int {
		i := slices.IndexFunc(p.d.participants, func(e element) bool { return e.label == name })
		switch {
		case i >= 0:
			return i
		case declared:
			p.fail(n, "unknown participant %q", name)
			return -1
		}
		p.label(n, "participant", name)
		p.d.participants = append(p.d.participants, element{line: n, label: name})
		return len(p.d.participants) - 1
	}
	for _, l := range lines {
		e, rest := p.element(l.N, l.Text)
		s := step{element: e}
		if m := noteRE.FindStringSubmatch(rest); m != nil {
			s.note, s.label = true, m[2]
			s.from = participant(l.N, m[1])
			p.label(l.N, "note", s.label)
		} else if m := messageRE.FindStringSubmatch(rest); m != nil {
			s.from, s.to, s.dashed, s.label = participant(l.N, m[1]), participant(l.N, m[3]), m[2] == "-->", m[4]
			if s.label != "" {
				p.label(l.N, "message", s.label)
			}
		} else {
			p.fail(l.N, "a sequence line is A -> B: text, A --> B: text or note A: text")
			continue
		}
		if s.from < 0 || s.to < 0 {
			continue
		}
		p.presentIn(l.N, s.element, "participant", p.d.participants, s.from, s.to)
		p.d.steps = append(p.d.steps, s)
	}
	if len(p.d.participants) > maxParticipants {
		p.fail(p.d.participants[maxParticipants].line, "a sequence has at most %d participants", maxParticipants)
	}
	if len(p.d.steps) == 0 && len(p.errs) == 0 {
		p.fail(n, "::: sequence needs at least one message or note")
	}
}

// label of a step, wrapped and measured.
type wrapped struct {
	lines []string
	width float64 // widest line, with the badge after the last one
}

func (f *Font) measure(e element, width float64, s style) wrapped {
	if e.label == "" {
		return wrapped{}
	}
	lines := f.wrap(e.label, width, s)
	w := f.widest(lines, s)
	if e.badge > 0 {
		last := f.Width(lines[len(lines)-1], s.size, s.weight)
		// A centred label keeps room for the badge on both sides of its last line.
		w = max(w, last+2*badgeSpace)
	}
	return wrapped{lines, w}
}

func (d *Diagram) drawSequence(f *Font, variant string) string {
	var cols []int // participants drawn, in order
	for i, e := range d.participants {
		if e.in(variant) {
			cols = append(cols, i)
		}
	}
	column := map[int]int{}
	for c, i := range cols {
		column[i] = c
	}
	var steps []step
	for _, s := range d.steps {
		if s.in(variant) {
			steps = append(steps, s)
		}
	}

	names := make([]wrapped, len(cols))
	boxW := make([]float64, len(cols))
	boxH := 0.0
	for c, i := range cols {
		names[c] = f.measure(d.participants[i], nameWidth, nameStyle)
		boxW[c] = max(boxMinWidth, names[c].width+2*boxPadX)
		boxH = max(boxH, float64(len(names[c].lines))*nameStyle.leading+2*boxPadY)
	}
	labels := make([]wrapped, len(steps))
	for k, s := range steps {
		if s.note {
			labels[k] = f.measure(s.element, noteWidth, noteStyle)
		} else {
			labels[k] = f.measure(s.element, messageWidth, textStyle)
		}
	}

	// Place each lifeline as far left as the boxes, notes and the labels of
	// messages ending on it allow.
	x := make([]float64, len(cols))
	right := 0.0
	for c := range cols {
		x[c] = margin + boxW[c]/2
		if c > 0 {
			x[c] = max(x[c], x[c-1]+boxW[c-1]/2+columnGap+boxW[c]/2)
		}
		for k, s := range steps {
			from, to := column[s.from], column[s.to]
			switch {
			case s.note && from == c:
				x[c] = max(x[c], margin+labels[k].width/2+notePadX)
			case !s.note && from == to && from == c-1:
				x[c] = max(x[c], x[c-1]+loopWidth+8+labels[k].width+labelPad)
			case !s.note && from != to && max(from, to) == c:
				x[c] = max(x[c], x[min(from, to)]+labels[k].width+2*labelPad)
			}
		}
	}
	for c := range cols {
		right = max(right, x[c]+boxW[c]/2)
	}
	for k, s := range steps {
		c := column[s.from]
		switch {
		case s.note:
			right = max(right, x[c]+labels[k].width/2+notePadX)
		case s.from == s.to:
			right = max(right, x[c]+loopWidth+8+labels[k].width)
		}
	}
	width := right + margin

	var used [4]bool
	cv := &canvas{}
	top := margin
	bottom := top + boxH
	y := bottom + 18
	var body canvas
	for k, s := range steps {
		t := s.toneIn(variant)
		used[t] = true
		l := labels[k]
		lines := float64(len(l.lines))
		body.printf(`<g class="%s">`, toneClass[t])
		from := x[column[s.from]]
		switch {
		case s.note:
			h := lines*noteStyle.leading + 2*notePadY
			w := l.width + 2*notePadX
			rx := 8.0
			if len(l.lines) == 1 {
				rx = h / 2
			}
			body.printf(`<rect class="dg-note" x="%s" y="%s" width="%s" height="%s" rx="%s"/>`, num(from-w/2), num(y), num(w), num(h), num(rx))
			body.label(f, s.element, l, from, y+notePadY+noteStyle.size, noteStyle)
			y += h + stepGap
		case s.from == s.to:
			ya := y + 4
			body.printf(`<path class="dg-line" d="M%s %sh%sv%sH%s" fill="none"%s/>`, num(from), num(ya), num(loopWidth), num(loopHeight), num(from+arrowHead), dash(s.dashed))
			body.head(from, ya+loopHeight, -1)
			if l.lines != nil {
				body.text(from+loopWidth+8, y+textStyle.size, l.lines, textStyle, "start", "dg-text")
				if s.badge > 0 {
					last := f.Width(l.lines[len(l.lines)-1], textStyle.size, textStyle.weight)
					body.badge(from+loopWidth+8+last+badgeSpace-badgeRadius, y+textStyle.size+(lines-1)*textStyle.leading-4, s.badge)
				}
			}
			y = max(ya+loopHeight, y+lines*textStyle.leading) + stepGap
		default:
			to := x[column[s.to]]
			dir := 1.0
			if to < from {
				dir = -1
			}
			ya := y + lines*textStyle.leading + 6
			if l.lines != nil {
				body.label(f, s.element, l, (from+to)/2, y+textStyle.size, textStyle)
			} else {
				ya = y + 4
			}
			body.printf(`<line class="dg-line" x1="%s" y1="%s" x2="%s" y2="%s"%s/>`, num(from), num(ya), num(to-dir*arrowHead), num(ya), dash(s.dashed))
			body.head(to, ya, dir)
			y = ya + stepGap + 4
		}
		body.b.WriteString(`</g>`)
	}
	end := y - stepGap + 8
	for c, i := range cols {
		e := d.participants[i]
		t := e.toneIn(variant)
		used[t] = true
		cv.printf(`<g class="%s"><line class="dg-life" x1="%s" y1="%s" x2="%s" y2="%s"/>`, toneClass[t], num(x[c]), num(bottom), num(x[c]), num(end))
		cv.printf(`<rect class="dg-box" x="%s" y="%s" width="%s" height="%s" rx="6"/>`, num(x[c]-boxW[c]/2), num(top), num(boxW[c]), num(boxH))
		first := top + (boxH-float64(len(names[c].lines))*nameStyle.leading)/2 + nameStyle.size
		cv.text(x[c], first, names[c].lines, nameStyle, "middle", "dg-name")
		if e.badge > 0 {
			cv.badge(x[c]+boxW[c]/2-2, top+2, e.badge)
		}
		cv.b.WriteString(`</g>`)
	}
	cv.b.WriteString(body.b.String())
	height := cv.legend(f, used, variant, d.options, end+6, width) + margin
	return cv.svg(d.title, width, height)
}

// label writes a centred label with its badge after the last line.
func (c *canvas) label(f *Font, e element, l wrapped, x, y float64, s style) {
	c.text(x, y, l.lines, s, "middle", "dg-text")
	if e.badge > 0 {
		last := f.Width(l.lines[len(l.lines)-1], s.size, s.weight)
		c.badge(x+last/2+badgeSpace-badgeRadius, y+float64(len(l.lines)-1)*s.leading-4, e.badge)
	}
}

// head draws an arrowhead whose tip is at x, y, pointing in direction dir.
func (c *canvas) head(x, y, dir float64) {
	c.printf(`<path class="dg-head" d="M%s %sL%s %sL%s %sZ"/>`, num(x), num(y), num(x-dir*arrowHead), num(y-4), num(x-dir*arrowHead), num(y+4))
}

func dash(dashed bool) string {
	if dashed {
		return ` stroke-dasharray="5 4"`
	}
	return ""
}
