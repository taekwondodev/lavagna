package diagram

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The flow example of #17, for a question with options send and inside.
const flowExample = `cli = lavagna CLI
frame = Frame sandbox
Agente -> cli: domande
cli -> Shell: pagina e token
cli -> frame: 01 e 02
Utente -> Shell: sceglie in 03
Shell -> cli: Send
Shell -> frame: id opzione ?1 [send]
Utente -> frame: sceglie di nuovo in 02 !1 [inside]
group Browser: Shell, frame`

var flowOptions = []Option{{"send", "A"}, {"inside", "B"}}

// flowExamples reads the eight examples of #13, each with its hand layout.
func flowExamples(t testing.TB) []handExample {
	b, err := os.ReadFile("testdata/flow-examples.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Examples []handExample }
	if err := json.Unmarshal(b, &file); err != nil {
		t.Fatal(err)
	}
	return file.Examples
}

type handExample struct {
	Name  string
	Nodes []struct {
		ID, Label string
		Col, Row  int
	}
	Edges  []struct{ From, To, Label string }
	Groups []struct {
		Label string
		Nodes []string
	}
}

// source writes the example in the flow grammar: an alias per node, its
// line breaks left to the measured wrapping, then edges and groups.
func (ex handExample) source() string {
	var b strings.Builder
	for _, n := range ex.Nodes {
		fmt.Fprintf(&b, "%s = %s\n", n.ID, strings.ReplaceAll(n.Label, "\n", " "))
	}
	for _, e := range ex.Edges {
		fmt.Fprintf(&b, "%s -> %s", e.From, e.To)
		if e.Label != "" {
			fmt.Fprintf(&b, ": %s", e.Label)
		}
		b.WriteString("\n")
	}
	for _, g := range ex.Groups {
		fmt.Fprintf(&b, "group %s: %s\n", g.Label, strings.Join(g.Nodes, ", "))
	}
	return b.String()
}

// hand redraws the hand cells of ex on the boxes of the automatic layout, as
// #13 did: straight edges between box centres, opposite pairs 8 px apart.
func (ex handExample) hand(auto geometry) geometry {
	cols, rows := 0, 0
	for _, n := range ex.Nodes {
		cols, rows = max(cols, n.Col+1), max(rows, n.Row+1)
	}
	colW, maxH, gap := make([]float64, cols), 0.0, gapMin
	for i, n := range ex.Nodes {
		colW[n.Col] = max(colW[n.Col], auto.nodes[i].w())
		maxH = max(maxH, auto.nodes[i].h())
	}
	for _, r := range auto.labels {
		gap = max(gap, r.w()+2*labelInset)
	}
	colX := make([]float64, cols)
	x := 0.0
	for c := range colW {
		colX[c] = x + colW[c]/2
		x += colW[c] + gap
	}
	var geo geometry
	index := map[string]int{}
	for i, n := range ex.Nodes {
		index[n.ID] = i
		w, h := auto.nodes[i].w(), auto.nodes[i].h()
		cx, cy := colX[n.Col], float64(n.Row)*(maxH+flowRowGap+20)
		geo.nodes = append(geo.nodes, rect{cx - w/2, cy - h/2, cx + w/2, cy + h/2})
	}
	pairs := map[[2]string]bool{}
	for _, e := range ex.Edges {
		pairs[[2]string{e.From, e.To}] = true
	}
	center := func(r rect) point { return point{(r.x0 + r.x1) / 2, (r.y0 + r.y1) / 2} }
	clip := func(c, t point, hw, hh float64) point {
		dx, dy := t.x-c.x, t.y-c.y
		s := math.Inf(1)
		if dx != 0 {
			s = hw / math.Abs(dx)
		}
		if dy != 0 {
			s = min(s, hh/math.Abs(dy))
		}
		return point{c.x + dx*s, c.y + dy*s}
	}
	for _, e := range ex.Edges {
		ba, bb := geo.nodes[index[e.From]], geo.nodes[index[e.To]]
		a, b := center(ba), center(bb)
		if pairs[[2]string{e.To, e.From}] {
			l := math.Hypot(b.x-a.x, b.y-a.y)
			ox, oy := -(b.y-a.y)/l*pairOff, (b.x-a.x)/l*pairOff
			a, b = point{a.x + ox, a.y + oy}, point{b.x + ox, b.y + oy}
		}
		geo.paths = append(geo.paths, []point{clip(a, b, ba.w()/2+2, ba.h()/2+2), clip(b, a, bb.w()/2+5, bb.h()/2+5)})
	}
	return geo
}

// metrics counts, on drawn geometry, the edges passing through a node other
// than their ends, the group boxes holding a non-member, the edge crossings,
// and the edge labels overlapping a node or another label or crossed by
// another edge.
type metrics struct{ through, nonMembers, crossings, labelOverlaps, labelsCrossed int }

func measureFlow(edges [][2]int, members [][]int, geo geometry) (metrics, []string) {
	var m metrics
	var details []string
	for e, p := range geo.paths {
		for v, b := range geo.nodes {
			if v == edges[e][0] || v == edges[e][1] {
				continue
			}
			for i := 0; i+1 < len(p); i++ {
				if segmentHits(p[i], p[i+1], rect{b.x0 + 0.5, b.y0 + 0.5, b.x1 - 0.5, b.y1 - 0.5}) {
					m.through++
					details = append(details, fmt.Sprintf("edge %d passes through node %d", e, v))
					break
				}
			}
		}
	}
	for g, r := range geo.groups {
		for v, b := range geo.nodes {
			if !slices.Contains(members[g], v) && overlaps(r, b) {
				m.nonMembers++
				details = append(details, fmt.Sprintf("group %d holds node %d", g, v))
			}
		}
	}
	for i := range geo.paths {
		for j := i + 1; j < len(geo.paths); j++ {
			for a := 0; a+1 < len(geo.paths[i]); a++ {
				for b := 0; b+1 < len(geo.paths[j]); b++ {
					if properCross(geo.paths[i][a], geo.paths[i][a+1], geo.paths[j][b], geo.paths[j][b+1]) {
						m.crossings++
						details = append(details, fmt.Sprintf("edges %d and %d cross", i, j))
					}
				}
			}
		}
	}
	for e, r := range geo.labels {
		if r == (rect{}) {
			continue
		}
		for _, other := range slices.Concat(geo.nodes, geo.labels[e+1:]) {
			if overlaps(r, other) {
				m.labelOverlaps++
				details = append(details, fmt.Sprintf("label %d overlaps", e))
			}
		}
		for other, p := range geo.paths {
			for i := 0; other != e && i+1 < len(p); i++ {
				if segmentHits(p[i], p[i+1], r) {
					m.labelsCrossed++
					details = append(details, fmt.Sprintf("label %d is crossed by edge %d", e, other))
					break
				}
			}
		}
	}
	return m, details
}

func cross(a, b, c point) float64 {
	v := (b.x-a.x)*(c.y-a.y) - (b.y-a.y)*(c.x-a.x)
	if math.Abs(v) < 1e-9 {
		return 0
	}
	return v
}

// properCross is true when two segments cross at one interior point.
func properCross(a, b, c, d point) bool {
	return cross(a, b, c)*cross(a, b, d) < 0 && cross(c, d, a)*cross(c, d, b) < 0
}

// laidOut parses src and lays out variant, with the edges and group members
// in the view's indices.
func laidOut(t testing.TB, f *Font, src, variant string) (*flowView, geometry, [][]int) {
	t.Helper()
	d, errs := Parse("flow", "", 1, body(src), flowOptions)
	if errs != nil {
		t.Fatal(errs)
	}
	v := d.flowView(f, variant)
	members := make([][]int, len(v.groups))
	for n, g := range v.g.group {
		if g >= 0 {
			members[g] = append(members[g], n)
		}
	}
	return v, v.geometry(f, d), members
}

func TestFlowExamplesMeetTheLayoutMetrics(t *testing.T) {
	f := testFont(t)
	for _, ex := range flowExamples(t) {
		t.Run(ex.Name, func(t *testing.T) {
			v, auto, members := laidOut(t, f, ex.source(), Now)
			if len(v.nodes) != len(ex.Nodes) || len(v.edges) != len(ex.Edges) || len(v.groups) != len(ex.Groups) {
				t.Fatalf("parsed %d nodes, %d edges, %d groups", len(v.nodes), len(v.edges), len(v.groups))
			}
			got, details := measureFlow(v.g.edges, members, auto)
			hand, _ := measureFlow(v.g.edges, members, ex.hand(auto))
			t.Logf("automatic %+v, hand %+v", got, hand)
			// #13 measured one hand crossing, in wayfinder-map, and none
			// elsewhere; the redrawn hand layouts must agree.
			if want := map[string]int{"wayfinder-map": 1}[ex.Name]; hand.crossings != want {
				t.Errorf("hand layout has %d crossings, #13 measured %d", hand.crossings, want)
			}
			if got.through != 0 || got.nonMembers != 0 || got.crossings > hand.crossings {
				t.Errorf("automatic %+v against hand %+v: %v", got, hand, details)
			}
			// Not required by #33, but kept: labels clear of nodes, other
			// labels and other edges.
			if got.labelOverlaps != 0 || got.labelsCrossed != 0 {
				t.Errorf("labels: %+v: %v", got, details)
			}
		})
	}
}

// randomFlow is a deterministic flow of nodes nodes and edges labelled edges,
// with four groups of up to size nodes each.
func randomFlow(seed uint32, nodes, edges, size int) string {
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % n
	}
	var b strings.Builder
	seen := map[[2]int]bool{}
	for len(seen) < edges {
		from, to := next(nodes), next(nodes)
		if from == to || seen[[2]int{from, to}] {
			continue
		}
		seen[[2]int{from, to}] = true
		fmt.Fprintf(&b, "Nodo %d -> Nodo %d: passo %d\n", from, to, len(seen))
	}
	for n := range nodes {
		fmt.Fprintf(&b, "Nodo %d\n", n)
	}
	grouped := map[int]bool{}
	for g := range maxGroups {
		var members []string
		for range size {
			if n := next(nodes); !grouped[n] {
				grouped[n] = true
				members = append(members, fmt.Sprintf("Nodo %d", n))
			}
		}
		if members != nil {
			fmt.Fprintf(&b, "group Gruppo %d: %s\n", g, strings.Join(members, ", "))
		}
	}
	return b.String()
}

// TestFlowLayoutKeepsEdgesOutOfNodesAndGroups checks the guarantees beyond the
// examples of #13, up to the limits of #17.
func TestFlowLayoutKeepsEdgesOutOfNodesAndGroups(t *testing.T) {
	f := testFont(t)
	for seed := range uint32(300) {
		nodes := 4 + int(seed%17)
		edges := min(maxEdges, nodes-1+int(seed*7%23), nodes*(nodes-1)/2)
		if seed%10 == 0 {
			nodes, edges = maxNodes, maxEdges
		}
		v, geo, members := laidOut(t, f, randomFlow(seed, nodes, edges, 1+int(seed%6)), Now)
		m, details := measureFlow(v.g.edges, members, geo)
		groupOverlaps := 0
		for i := range geo.groups {
			for j := i + 1; j < len(geo.groups); j++ {
				if overlaps(geo.groups[i], geo.groups[j]) {
					groupOverlaps++
					details = append(details, fmt.Sprintf("groups %d and %d overlap", i, j))
				}
			}
		}
		if m.through != 0 || m.nonMembers != 0 || groupOverlaps != 0 {
			t.Errorf("seed %d, %d nodes, %d edges: %+v: %v", seed, nodes, edges, m, details)
		}
	}
}

func BenchmarkFlowAtTheLimit(b *testing.B) {
	f := testFont(b)
	d, errs := Parse("flow", "", 1, body(randomFlow(0, maxNodes, maxEdges, 5)), flowOptions)
	if errs != nil {
		b.Fatal(errs)
	}
	for b.Loop() {
		d.SVG(f, Now)
	}
}

func TestFlowExampleDrawsEachVariant(t *testing.T) {
	f := testFont(t)
	d, errs := Parse("flow", "Chi conosce la scelta prima del Send", 1, body(flowExample), flowOptions)
	if errs != nil {
		t.Fatal(errs)
	}
	if !d.Varies() || strings.Join(d.Variants(), " ") != "now send inside" {
		t.Fatalf("variants %v, varies %v", d.Variants(), d.Varies())
	}
	cases := []struct {
		variant string
		tones   map[string]string // label: tone class, "" when absent
	}{
		{"now", map[string]string{
			"lavagna CLI": "dg-neutral", "Frame sandbox": "dg-neutral", "Agente": "dg-neutral", "Browser": "dg-neutral",
			"pagina e token": "dg-neutral", "id opzione": "", "sceglie di nuovo in 02": "",
		}},
		{"send", map[string]string{"id opzione": "dg-risk", "sceglie di nuovo in 02": "", "Send": "dg-neutral"}},
		{"inside", map[string]string{"id opzione": "", "sceglie di nuovo in 02": "dg-problem"}},
	}
	for _, variant := range d.Variants() {
		v, geo, members := laidOut(t, f, flowExample, variant)
		// In inside, lavagna CLI and Utente both feed Shell and Frame
		// sandbox: one crossing is unavoidable.
		if m, details := measureFlow(v.g.edges, members, geo); m.through+m.nonMembers+m.labelOverlaps+m.labelsCrossed != 0 || m.crossings > 1 {
			t.Errorf("%s: %+v: %v", variant, m, details)
		}
	}
	badge := regexp.MustCompile(`<circle class="dg-badge"[^>]*/><text class="dg-badge-text"[^>]*>1</text>`)
	for _, tc := range cases {
		svg := d.SVG(f, tc.variant)
		for label, want := range tc.tones {
			if got := toneOf(svg, label); got != want {
				t.Errorf("%s: %q drawn %q, want %q", tc.variant, label, got, want)
			}
		}
		if n, want := len(badge.FindAllString(svg, -1)), map[string]int{"now": 0, "send": 1, "inside": 1}[tc.variant]; n != want {
			t.Errorf("%s draws %d badges, want %d", tc.variant, n, want)
		}
		if strings.Count(svg, `class="dg-group"`) != 1 || strings.Count(svg, `class="dg-box"`) != 5 {
			t.Errorf("%s: want the Browser group and five nodes: %s", tc.variant, svg)
		}
	}
}

func TestFlowNodesTakeTheTagOfTheirDeclaration(t *testing.T) {
	f := testFont(t)
	d, errs := Parse("flow", "", 1, body("A -> b: scrive [send]\nA --> C\nb = Nuovo file +1 [send]"), flowOptions)
	if errs != nil {
		t.Fatal(errs)
	}
	if svg := d.SVG(f, Now); toneOf(svg, "Nuovo file") != "" || toneOf(svg, "A") != "dg-neutral" || !strings.Contains(svg, `stroke-dasharray="5 4"`) {
		t.Errorf("now: %s", svg)
	}
	send := d.SVG(f, "send")
	if toneOf(send, "Nuovo file") != "dg-change" || toneOf(send, "scrive") != "dg-change" || !strings.Contains(send, `class="dg-badge"`) {
		t.Errorf("send: %s", send)
	}
	if got := strings.Join([]string{d.nodes[0].label, d.nodes[1].label, d.nodes[2].label}, ", "); got != "A, Nuovo file, C" {
		t.Errorf("nodes in order of first mention: %s", got)
	}
}

func TestFlowGrammarErrorsCarryTheirLine(t *testing.T) {
	many := func(n int, format string) string {
		var lines []string
		for i := range n {
			lines = append(lines, fmt.Sprintf(format, i, i))
		}
		return strings.Join(lines, "\n")
	}
	cases := []struct {
		name, src string
		line      int
		want      string
	}{
		{"twenty-one nodes", many(21, "N%d%d"), 22, "at most 20 nodes"},
		{"thirty-one edges", many(31, "A -> B: %d%d"), 32, "at most 30 edges"},
		{"five groups", "A\nB\nC\nD\nE\ngroup G1: A\ngroup G2: B\ngroup G3: C\ngroup G4: D\ngroup G5: E", 11, "at most 4 groups"},
		{"unknown group node", "A -> B\ngroup G: A, X", 3, `unknown node or alias "X"`},
		{"node in two groups", "A -> B\ngroup G: A\ngroup H: A, B", 4, `node "A" is already in the group on line 3`},
		{"group twice", "A\nB\ngroup G: A\ngroup G: B", 5, `group "G" is defined on line 4`},
		{"alias twice", "a = Uno\na = Due", 3, `alias "a" is defined twice`},
		{"node declared twice", "a = Uno\nUno", 3, `node "Uno" is declared on line 2`},
		{"self edge", "A -> A: ancora", 2, `an edge cannot join "A" to itself`},
		{"alias hiding a declared node", "x = Uno\nx\nx -> Due", 3, `node "x" has the name of the alias on line 2`},
		{"alias hiding an aliased label", "x = Uno\ny = x\ny -> x", 3, `node "x" has the name of the alias on line 2`},
		{"group without colon", "A\ngroup Browser A", 3, "a group is group Name: A, B"},
		{"node absent from a variant", "A [send]\nA -> B: x", 3, `node "A" is absent from variant now`},
		{"unknown variant", "A -> B [later]", 2, `unknown variant "later"`},
		{"long node", strings.Repeat("x", 81), 2, "longer than 80 characters"},
		{"long alias label", "a = " + strings.Repeat("x", 81) + "\na -> b", 2, "longer than 80 characters"},
		{"empty", "", 1, "at least one node"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := Parse("flow", "", 1, body(tc.src), flowOptions)
			for _, e := range errs {
				if e.Line == tc.line && strings.Contains(e.Msg, tc.want) {
					return
				}
			}
			t.Errorf("want line %d %q, got %v", tc.line, tc.want, errs)
		})
	}
}
