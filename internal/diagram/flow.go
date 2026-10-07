package diagram

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
)

const (
	maxNodes  = 20
	maxEdges  = 30
	maxGroups = 4
)

type edge struct {
	element
	from, to int
	dashed   bool
}

type group struct {
	element
	members []int
}

var (
	groupRE = regexp.MustCompile(`^group\s+([^:]+?)\s*:\s*(.*)$`)
	aliasRE = regexp.MustCompile(`^([^\s=]+)\s*=\s*(.+)$`)
)

// flowLine is a flow line split from its variant tag and marker. A line that
// is neither an edge nor a group declares the node label, through alias when
// it has one.
type flowLine struct {
	element
	edge, group []string
	alias       string
}

// flow reads a flow block. A line names a node by its label or its alias. An
// edge adds the nodes it names, in order of first mention; a node declared on
// its own line takes that line's tag and marker wherever it is first named. A
// group names only nodes that other lines define. An alias may not repeat the
// label of another node, which it would hide.
func (p *parser) flow(n int, lines []Line) {
	var ls []flowLine
	declared := map[string]flowLine{} // by label
	aliases := map[string]flowLine{}  // by alias
	for _, l := range lines {
		e, rest := p.element(l.N, l.Text)
		fl := flowLine{element: e}
		switch m := aliasRE.FindStringSubmatch(rest); {
		case groupRE.MatchString(rest):
			fl.group = groupRE.FindStringSubmatch(rest)
		case strings.HasPrefix(rest, "group "):
			p.fail(l.N, "a group is group Name: A, B")
			continue
		case messageRE.MatchString(rest):
			fl.edge = messageRE.FindStringSubmatch(rest)
		case m != nil:
			fl.alias, fl.label = m[1], m[2]
			if _, ok := aliases[fl.alias]; ok {
				p.fail(l.N, "alias %q is defined twice", fl.alias)
				continue
			}
			aliases[fl.alias] = fl
		default:
			fl.label = rest
		}
		if fl.edge == nil && fl.group == nil {
			if d, ok := declared[fl.label]; ok {
				p.fail(l.N, "node %q is declared on line %d", fl.label, d.line)
				continue
			}
			declared[fl.label] = fl
		}
		ls = append(ls, fl)
	}
	for _, fl := range ls {
		if a, ok := aliases[fl.label]; ok && fl.edge == nil && fl.group == nil && a.line != fl.line {
			p.fail(fl.line, "node %q has the name of the alias on line %d", fl.label, a.line)
		}
	}
	resolve := func(name string) string {
		if a, ok := aliases[name]; ok {
			return a.label
		}
		return name
	}
	find := func(name string) int {
		label := resolve(name)
		return slices.IndexFunc(p.d.nodes, func(e element) bool { return e.label == label })
	}
	node := func(line int, name string) int {
		if i := find(name); i >= 0 {
			return i
		}
		e := element{line: line, label: resolve(name)}
		if d, ok := declared[e.label]; ok {
			e = d.element
		}
		p.label(e.line, "node", e.label)
		p.d.nodes = append(p.d.nodes, e)
		if len(p.d.nodes) == maxNodes+1 {
			p.fail(line, "a flow has at most %d nodes", maxNodes)
		}
		return len(p.d.nodes) - 1
	}
	for _, fl := range ls {
		switch {
		case fl.edge != nil:
			m := fl.edge
			e := edge{element: fl.element, from: node(fl.line, m[1]), to: node(fl.line, m[3]), dashed: m[2] == "-->"}
			e.label = m[4]
			if e.from == e.to {
				p.fail(fl.line, "an edge cannot join %q to itself", p.d.nodes[e.from].label)
				continue
			}
			if e.label != "" {
				p.label(fl.line, "edge", e.label)
			}
			p.presentIn(fl.line, e.element, "node", p.d.nodes, e.from, e.to)
			p.d.edges = append(p.d.edges, e)
			if len(p.d.edges) == maxEdges+1 {
				p.fail(fl.line, "a flow has at most %d edges", maxEdges)
			}
		case fl.group == nil:
			node(fl.line, fl.label)
		}
	}
	inGroup := map[int]int{} // node to the line of its group
	for _, fl := range ls {
		if fl.group == nil {
			continue
		}
		g := group{element: fl.element}
		g.label = fl.group[1]
		p.label(fl.line, "group", g.label)
		if i := slices.IndexFunc(p.d.groups, func(o group) bool { return o.label == g.label }); i >= 0 {
			p.fail(fl.line, "group %q is defined on line %d", g.label, p.d.groups[i].line)
		}
		for _, name := range strings.Split(fl.group[2], ",") {
			name = strings.TrimSpace(name)
			i := find(name)
			switch {
			case name == "":
				p.fail(fl.line, "group %q lists an empty name", g.label)
			case i < 0:
				p.fail(fl.line, "unknown node or alias %q: a group lists nodes that other lines define", name)
			case inGroup[i] != 0:
				p.fail(fl.line, "node %q is already in the group on line %d", p.d.nodes[i].label, inGroup[i])
			default:
				inGroup[i] = fl.line
				g.members = append(g.members, i)
			}
		}
		p.d.groups = append(p.d.groups, g)
		if len(p.d.groups) == maxGroups+1 {
			p.fail(fl.line, "a flow has at most %d groups", maxGroups)
		}
	}
	if len(p.d.nodes) == 0 && len(p.errs) == 0 {
		p.fail(n, "::: flow needs at least one node")
	}
}

const (
	nodeMinWidth  = 80.0
	nodeMinHeight = 36.0
	nodeWidth     = 150.0 // a node label wraps beyond this
	edgeWidth     = 130.0 // an edge label wraps beyond this
	flowRowGap    = 26.0
	groupTitlePad = 6.0
)

var groupStyle = style{11, 650, 14}

// flowView is the flow one variant draws: the elements present in it, their
// measured labels and the graph the layout reads.
type flowView struct {
	g                    graph
	nodes, edges, groups []int // indices into the diagram's
	names, texts         []wrapped
	titles               []wrapped // set by geometry, at the width of each group
}

func (d *Diagram) flowView(f *Font, variant string) *flowView {
	v := &flowView{}
	local := map[int]int{}
	for i, e := range d.nodes {
		if e.in(variant) {
			local[i] = len(v.nodes)
			v.nodes = append(v.nodes, i)
			name := f.measure(e, nodeWidth, nameStyle)
			v.names = append(v.names, name)
			v.g.nodes = append(v.g.nodes, point{
				max(nodeMinWidth, name.width+2*boxPadX),
				max(nodeMinHeight, float64(len(name.lines))*nameStyle.leading+2*boxPadY),
			})
			v.g.group = append(v.g.group, -1)
		}
	}
	for i, e := range d.edges {
		if !e.in(variant) {
			continue
		}
		v.edges = append(v.edges, i)
		v.g.edges = append(v.g.edges, [2]int{local[e.from], local[e.to]})
		text := f.measure(e.element, edgeWidth, textStyle)
		v.texts = append(v.texts, text)
		size := point{text.width, float64(len(text.lines)) * textStyle.leading}
		if text.lines == nil && e.badge > 0 {
			size = point{2 * badgeRadius, 2 * badgeRadius}
		}
		v.g.labels = append(v.g.labels, size)
	}
	for i, gr := range d.groups {
		if !gr.in(variant) {
			continue
		}
		present := false
		for _, m := range gr.members {
			if n, ok := local[m]; ok {
				v.g.group[n], present = len(v.groups), true
			}
		}
		if present {
			v.groups = append(v.groups, i)
		}
	}
	v.g.groups = len(v.groups)
	return v
}

// geometry lays the view out. The coordinates may be negative; the drawing
// translates them.
func (v *flowView) geometry(f *Font, d *Diagram) geometry {
	if len(v.nodes) == 0 {
		return geometry{}
	}
	l := layout(&v.g)
	geo := l.fit(flowRowGap, nil)
	if len(v.groups) == 0 {
		return geo
	}
	// Titles wrap to the width of their box, which the row gap does not
	// change; the row gap then makes room for the tallest title between a
	// group and the one below it.
	v.titles = make([]wrapped, len(v.groups))
	titles := make([]float64, len(v.groups))
	for i, gi := range v.groups {
		v.titles[i] = f.measure(d.groups[gi].element, geo.groups[i].w()-2*groupTitlePad, groupStyle)
		titles[i] = float64(len(v.titles[i].lines))*groupStyle.leading + 2*groupTitlePad
	}
	return l.fit(max(flowRowGap, slices.Max(titles)+groupPadY+8), titles)
}

func (d *Diagram) drawFlow(f *Font, variant string) string {
	v := d.flowView(f, variant)
	geo := v.geometry(f, d)

	// Translate the drawing to the margin.
	lo, hi := point{math.Inf(1), math.Inf(1)}, point{math.Inf(-1), math.Inf(-1)}
	grow := func(r rect) {
		lo = point{min(lo.x, r.x0), min(lo.y, r.y0)}
		hi = point{max(hi.x, r.x1), max(hi.y, r.y1)}
	}
	for _, r := range slices.Concat(geo.nodes, geo.groups, geo.labels) {
		if r != (rect{}) {
			grow(r)
		}
	}
	for _, p := range geo.paths {
		for _, q := range p {
			grow(rect{q.x, q.y, q.x, q.y})
		}
	}
	if len(v.nodes) == 0 {
		lo, hi = point{}, point{}
	}
	dx, dy := margin-lo.x, margin-lo.y
	at := func(r rect) rect { return rect{r.x0 + dx, r.y0 + dy, r.x1 + dx, r.y1 + dy} }
	width := hi.x - lo.x + 2*margin

	var used [4]bool
	cv := &canvas{}
	for i, gi := range v.groups {
		gr, r := d.groups[gi], at(geo.groups[i])
		t := gr.toneIn(variant)
		used[t] = true
		cv.printf(`<g class="%s"><rect class="dg-group" x="%s" y="%s" width="%s" height="%s" rx="10"/>`, toneClass[t], num(r.x0), num(r.y0), num(r.w()), num(r.h()))
		title := v.titles[i]
		y := r.y0 + groupTitlePad + groupStyle.size
		cv.text(r.x0+groupTitlePad, y, title.lines, groupStyle, "start", "dg-group-name")
		if gr.badge > 0 {
			last := f.Width(title.lines[len(title.lines)-1], groupStyle.size, groupStyle.weight)
			cv.badge(r.x0+groupTitlePad+last+badgeSpace-badgeRadius, y+float64(len(title.lines)-1)*groupStyle.leading-4, gr.badge)
		}
		cv.b.WriteString(`</g>`)
	}
	for i, ei := range v.edges {
		e := d.edges[ei]
		t := e.toneIn(variant)
		used[t] = true
		p := slices.Clone(geo.paths[i])
		for k := range p {
			p[k] = point{p[k].x + dx, p[k].y + dy}
		}
		cv.printf(`<g class="%s">`, toneClass[t])
		cv.arrow(p, e.dashed)
		if r := geo.labels[i]; r != (rect{}) {
			r = at(r)
			if text := v.texts[i]; text.lines != nil {
				cv.label(f, e.element, text, (r.x0+r.x1)/2, r.y0+textStyle.size, textStyle)
			} else {
				cv.badge((r.x0+r.x1)/2, (r.y0+r.y1)/2, e.badge)
			}
		}
		cv.b.WriteString(`</g>`)
	}
	for i, ni := range v.nodes {
		e, r, name := d.nodes[ni], at(geo.nodes[i]), v.names[i]
		t := e.toneIn(variant)
		used[t] = true
		cv.printf(`<g class="%s"><rect class="dg-box" x="%s" y="%s" width="%s" height="%s" rx="6"/>`, toneClass[t], num(r.x0), num(r.y0), num(r.w()), num(r.h()))
		first := r.y0 + (r.h()-float64(len(name.lines))*nameStyle.leading)/2 + nameStyle.size
		cv.text((r.x0+r.x1)/2, first, name.lines, nameStyle, "middle", "dg-name")
		if e.badge > 0 {
			cv.badge(r.x1-2, r.y0+2, e.badge)
		}
		cv.b.WriteString(`</g>`)
	}
	height := cv.legend(f, used, variant, d.options, hi.y+dy+6, width) + margin
	return cv.svg(d.title, width, height)
}

// arrow draws the polyline p with an arrowhead at its last point, along its
// last segment.
func (c *canvas) arrow(p []point, dashed bool) {
	// A stub shorter than the head would turn the head away from the line
	// that reaches it.
	if n := len(p); n > 2 && math.Hypot(p[n-1].x-p[n-2].x, p[n-1].y-p[n-2].y) < arrowHead+2 {
		p = append(p[:n-2], p[n-1])
	}
	tip, from := p[len(p)-1], p[len(p)-2]
	length := math.Hypot(tip.x-from.x, tip.y-from.y)
	ux, uy := (tip.x-from.x)/length, (tip.y-from.y)/length
	base := point{tip.x - ux*arrowHead, tip.y - uy*arrowHead}
	var d strings.Builder
	for i, q := range p[:len(p)-1] {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&d, "%s%s %s", cmd, num(q.x), num(q.y))
	}
	fmt.Fprintf(&d, "L%s %s", num(base.x), num(base.y))
	c.printf(`<path class="dg-line" d="%s" fill="none"%s/>`, d.String(), dash(dashed))
	c.printf(`<path class="dg-head" d="M%s %sL%s %sL%s %sZ"/>`, num(tip.x), num(tip.y),
		num(base.x-uy*4), num(base.y+ux*4), num(base.x+uy*4), num(base.y-ux*4))
}
