package diagram

// The flow layout is layered, left to right, after Sugiyama et al. 1981 and
// Gansner et al. 1993, trimmed for graphs of about 20 nodes as measured in #13:
//
//  1. cycle removal: DFS in input order reverses back edges (Gansner §2.1);
//     parallel and opposite edges merge into one layered edge;
//  2. layering: longest path, single-node moves that shorten edges, then
//     balanced nodes move to the least crowded feasible layer (Gansner
//     fig. 2-1, remark 8);
//  3. dummy vertices split edges longer than one layer (Gansner §3);
//  4. ordering: weighted median sweeps and transpose (Gansner fig. 3-1), then
//     exhaustive search over adjacent layer pairs; members of a group stay
//     contiguous in a layer and groups keep one order in every layer
//     (Forster 2002, R1 and R2);
//  5. rows: integer grid rows by iterated weighted medians and isotonic
//     regression, then each group's band is cleared of non-members;
//  6. routing: ports spread along each node side by the row of the other end,
//     and each layer-to-layer hop is a straight segment in the gap between
//     columns, so no edge passes through a node.

import (
	"math"
	"slices"
	"sort"
)

const (
	pairOff = 8.0 // between parallel edges and between ports
	// maxOrders caps the orders of a pair of adjacent layers searched at once.
	maxOrders = 50000
)

type point struct{ x, y float64 }

type rect struct{ x0, y0, x1, y1 float64 }

func (r rect) w() float64 { return r.x1 - r.x0 }
func (r rect) h() float64 { return r.y1 - r.y0 }

func overlaps(a, b rect) bool { return a.x0 < b.x1 && b.x0 < a.x1 && a.y0 < b.y1 && b.y0 < a.y1 }

// segmentHits reports whether the segment from a to b enters r, by clipping
// it to r (Liang and Barsky).
func segmentHits(a, b point, r rect) bool {
	t0, t1 := 0.0, 1.0
	dx, dy := b.x-a.x, b.y-a.y
	for _, c := range [][2]float64{{-dx, a.x - r.x0}, {dx, r.x1 - a.x}, {-dy, a.y - r.y0}, {dy, r.y1 - a.y}} {
		p, q := c[0], c[1]
		if p == 0 {
			if q < 0 {
				return false
			}
			continue
		}
		if t := q / p; p < 0 {
			t0 = max(t0, t)
		} else {
			t1 = min(t1, t)
		}
	}
	return t0 < t1
}

// graph is a flow as one variant draws it: node and edge label sizes, edges as
// pairs of node indices and the group of each node, or -1.
type graph struct {
	nodes  []point // width and height of each node box
	edges  [][2]int
	labels []point // width and height of each edge label, zero without one
	group  []int
	groups int
}

// geometry is a laid-out graph, in the graph's order.
type geometry struct {
	nodes  []rect
	paths  [][]point // from the edge's source to the tip of its arrow
	labels []rect    // zero for an edge without a label
	groups []rect    // with the title on top
}

type layered struct {
	g        *graph
	n        int   // real vertices are 0..n-1, dummies follow
	group    []int // per vertex
	layer    []int
	layers   [][]int
	pos      []int // index of each vertex in its layer
	row      []int
	up, down [][]int
	reversed []bool // per edge
	// groupOrder ranks the groups; members of a group are contiguous in
	// every layer and the groups follow this order in all of them.
	groupOrder []int
	chains     [][]int   // per layered edge: its vertices from left to right
	origs      [][]int   // per layered edge: the edges drawn along it
	widen      []float64 // per gap after a layer: width added for crowded labels
}

// omega weights the straightness of an edge segment by how many of its ends
// are dummies (Gansner p. 18), so long edges run straight.
func (l *layered) omega(a, b int) float64 {
	switch {
	case a >= l.n && b >= l.n:
		return 8
	case a >= l.n || b >= l.n:
		return 2
	}
	return 1
}

func layout(g *graph) *layered {
	l := &layered{g: g, n: len(g.nodes), group: slices.Clone(g.group)}
	l.breakCycles()
	succ, pred := l.merge()
	l.rank(succ, pred)
	l.addDummies()
	l.order()
	l.assignRows()
	return l
}

func (l *layered) breakCycles() {
	out := make([][]int, l.n)
	indeg := make([]int, l.n)
	for e, ed := range l.g.edges {
		out[ed[0]] = append(out[ed[0]], e)
		indeg[ed[1]]++
	}
	l.reversed = make([]bool, len(l.g.edges))
	state := make([]int, l.n) // 0 unvisited, 1 on the stack, 2 done
	var dfs func(v int)
	dfs = func(v int) {
		state[v] = 1
		for _, e := range out[v] {
			switch w := l.g.edges[e][1]; state[w] {
			case 1:
				l.reversed[e] = true
			case 0:
				dfs(w)
			}
		}
		state[v] = 2
	}
	for _, sourcesOnly := range []bool{true, false} {
		for v := range l.n {
			if state[v] == 0 && (!sourcesOnly || indeg[v] == 0) {
				dfs(v)
			}
		}
	}
}

// merge orients every edge and merges parallel ones, so a pair of opposite
// edges becomes one layered edge drawn twice.
func (l *layered) merge() (succ, pred [][]int) {
	succ, pred = make([][]int, l.n), make([][]int, l.n)
	key := map[[2]int]int{}
	for e, ed := range l.g.edges {
		a, b := ed[0], ed[1]
		if l.reversed[e] {
			a, b = b, a
		}
		k, ok := key[[2]int{a, b}]
		if !ok {
			k = len(l.chains)
			key[[2]int{a, b}] = k
			l.chains = append(l.chains, []int{a, b})
			l.origs = append(l.origs, nil)
			succ[a], pred[b] = append(succ[a], b), append(pred[b], a)
		}
		l.origs[k] = append(l.origs[k], e)
	}
	return succ, pred
}

func (l *layered) rank(succ, pred [][]int) {
	l.layer = make([]int, l.n)
	indeg := make([]int, l.n)
	var queue []int
	for v := range l.n {
		if indeg[v] = len(pred[v]); indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range succ[v] {
			l.layer[w] = max(l.layer[w], l.layer[v]+1)
			if indeg[w]--; indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	// A node with more out- than in-edges shortens the total edge length by
	// moving right up to its nearest successor, and the converse to the left.
	for changed, i := true, 0; changed && i < 4*l.n; i++ {
		changed = false
		for v := range l.n {
			switch {
			case len(succ[v]) > len(pred[v]):
				to := math.MaxInt
				for _, w := range succ[v] {
					to = min(to, l.layer[w]-1)
				}
				if to > l.layer[v] {
					l.layer[v], changed = to, true
				}
			case len(pred[v]) > len(succ[v]):
				to := 0
				for _, u := range pred[v] {
					to = max(to, l.layer[u]+1)
				}
				if to < l.layer[v] {
					l.layer[v], changed = to, true
				}
			}
		}
	}
	// A node with as many in- as out-edges moves to the feasible layer with
	// the fewest nodes, which spreads isolated nodes instead of stacking them.
	last, count := 0, map[int]int{}
	for _, r := range l.layer {
		last = max(last, r)
		count[r]++
	}
	for v := range l.n {
		if len(pred[v]) != len(succ[v]) {
			continue
		}
		lo, hi := 0, last
		for _, u := range pred[v] {
			lo = max(lo, l.layer[u]+1)
		}
		for _, w := range succ[v] {
			hi = min(hi, l.layer[w]-1)
		}
		best := l.layer[v]
		for r := lo; r <= hi; r++ {
			if count[r] < count[best] {
				best = r
			}
		}
		count[l.layer[v]]--
		count[best]++
		l.layer[v] = best
	}
}

func (l *layered) addDummies() {
	for k, c := range l.chains {
		// A dummy takes the group of a grouped end: the edge enters that box
		// anyway, so it may run inside it instead of around it.
		a, b := c[0], c[1]
		g := max(l.group[a], l.group[b])
		if l.group[a] >= 0 && l.group[b] >= 0 && l.group[a] != l.group[b] {
			g = -1
		}
		chain := []int{a}
		for r := l.layer[a] + 1; r < l.layer[b]; r++ {
			chain = append(chain, len(l.layer))
			l.layer = append(l.layer, r)
			l.group = append(l.group, g)
		}
		l.chains[k] = append(chain, b)
	}
	nv := len(l.layer)
	l.up, l.down = make([][]int, nv), make([][]int, nv)
	for _, c := range l.chains {
		for i := 0; i+1 < len(c); i++ {
			l.down[c[i]] = append(l.down[c[i]], c[i+1])
			l.up[c[i+1]] = append(l.up[c[i+1]], c[i])
		}
	}
	// The first order is breadth-first from the first layer, in input order.
	l.layers = make([][]int, slices.Max(l.layer)+1)
	seen := make([]bool, nv)
	var queue []int
	visit := func(v int) {
		if !seen[v] {
			seen[v] = true
			queue = append(queue, v)
		}
	}
	for v := range l.n {
		if l.layer[v] == 0 {
			visit(v)
		}
	}
	for v := range nv {
		visit(v)
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			l.layers[l.layer[u]] = append(l.layers[l.layer[u]], u)
			for _, w := range l.down[u] {
				visit(w)
			}
		}
	}
	l.pos = make([]int, nv)
	for r := range l.layers {
		l.index(r)
	}
}

// index records the position of each vertex of layer r.
func (l *layered) index(r int) {
	for i, v := range l.layers[r] {
		l.pos[v] = i
	}
}

// crossingsAt counts the crossings between layer r and the next one.
func (l *layered) crossingsAt(r int) int {
	if r < 0 || r+1 >= len(l.layers) {
		return 0
	}
	c := 0
	ly := l.layers[r]
	for i, a := range ly {
		for _, b := range ly[i+1:] {
			for _, x := range l.down[a] {
				for _, y := range l.down[b] {
					if l.pos[x] > l.pos[y] {
						c++
					}
				}
			}
		}
	}
	return c
}

// crossings counts the crossings between layers from and to, inclusive.
func (l *layered) crossings(from, to int) int {
	c := 0
	for r := from; r < to; r++ {
		c += l.crossingsAt(r)
	}
	return c
}

func (l *layered) total() int { return l.crossings(0, len(l.layers)) }

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	m := len(s) / 2
	if len(s)%2 == 1 {
		return s[m]
	}
	return (s[m-1] + s[m]) / 2
}

// arrange sorts layer r by weight while keeping each group contiguous and the
// groups in the order rank gives them (Forster's R1 and R2).
func (l *layered) arrange(r int, w []float64, rank []int) {
	type block struct {
		key float64
		g   int
		vs  []int
	}
	var blocks []*block
	byGroup := map[int]*block{}
	for _, v := range l.layers[r] {
		g := l.group[v]
		if b := byGroup[g]; g >= 0 && b != nil {
			b.vs = append(b.vs, v)
			continue
		}
		b := &block{g: g, vs: []int{v}}
		blocks = append(blocks, b)
		if g >= 0 {
			byGroup[g] = b
		}
	}
	for _, b := range blocks {
		for _, v := range b.vs {
			b.key += w[v] / float64(len(b.vs))
		}
		sort.SliceStable(b.vs, func(i, j int) bool { return w[b.vs[i]] < w[b.vs[j]] })
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].key < blocks[j].key })
	var slots []int
	var gs []*block
	for i, b := range blocks {
		if b.g >= 0 {
			slots, gs = append(slots, i), append(gs, b)
		}
	}
	sort.SliceStable(gs, func(i, j int) bool { return rank[gs[i].g] < rank[gs[j].g] })
	for i, s := range slots {
		blocks[s] = gs[i]
	}
	ly := l.layers[r][:0]
	for _, b := range blocks {
		ly = append(ly, b.vs...)
	}
	l.index(r)
}

// groupRank orders the groups by the mean relative position of their members.
func (l *layered) groupRank() []int {
	score := make([]float64, l.g.groups)
	count := make([]float64, l.g.groups)
	for v, g := range l.group {
		if g >= 0 {
			score[g] += float64(l.pos[v]) / float64(len(l.layers[l.layer[v]]))
			count[g]++
		}
	}
	gs := make([]int, l.g.groups)
	for i := range gs {
		gs[i] = i
	}
	mean := func(g int) float64 {
		if count[g] == 0 {
			return 0
		}
		return score[g] / count[g]
	}
	sort.SliceStable(gs, func(i, j int) bool { return mean(gs[i]) < mean(gs[j]) })
	rank := make([]int, len(gs))
	for r, g := range gs {
		rank[g] = r
	}
	return rank
}

func (l *layered) positions() []float64 {
	w := make([]float64, len(l.pos))
	for v, p := range l.pos {
		w[v] = float64(p)
	}
	return w
}

func clone(layers [][]int) [][]int {
	out := make([][]int, len(layers))
	for i, ly := range layers {
		out[i] = slices.Clone(ly)
	}
	return out
}

func (l *layered) setLayers(layers [][]int) {
	l.layers = layers
	for r := range l.layers {
		l.index(r)
	}
}

func (l *layered) order() {
	fix := func() {
		w := l.positions()
		l.groupOrder = l.groupRank()
		for r := range l.layers {
			l.arrange(r, w, l.groupOrder)
		}
	}
	fix()
	best, bestOrder, bestC := clone(l.layers), l.groupOrder, l.total()
	for it := 0; it < 24 && bestC > 0; it++ {
		rank := l.groupRank()
		downward := it%2 == 0
		for k := range l.layers {
			r := k
			if !downward {
				r = len(l.layers) - 1 - k
			}
			w := l.positions()
			for _, v := range l.layers[r] {
				nb := l.up[v]
				if !downward {
					nb = l.down[v]
				}
				if len(nb) > 0 {
					var ps []float64
					for _, u := range nb {
						ps = append(ps, float64(l.pos[u]))
					}
					w[v] = median(ps)
				}
			}
			l.arrange(r, w, rank)
		}
		l.transpose()
		fix()
		if c := l.total(); c < bestC {
			best, bestOrder, bestC = clone(l.layers), l.groupOrder, c
		}
	}
	l.setLayers(best)
	l.groupOrder = bestOrder
	l.permute()
}

// transpose swaps neighbours of the same group while that removes crossings.
func (l *layered) transpose() {
	for improved := true; improved; {
		improved = false
		for r, ly := range l.layers {
			for i := 0; i+1 < len(ly); i++ {
				v, w := ly[i], ly[i+1]
				if l.group[v] != l.group[w] {
					continue
				}
				before := l.crossings(r-1, r+1)
				ly[i], ly[i+1] = w, v
				l.pos[v], l.pos[w] = i+1, i
				if l.crossings(r-1, r+1) < before {
					improved = true
				} else {
					ly[i], ly[i+1] = v, w
					l.pos[v], l.pos[w] = i, i+1
				}
			}
		}
	}
}

// orders counts the orders of layers of the given sizes searched together, up
// to one more than maxOrders.
func orders(sizes ...int) int {
	n := 1
	for _, size := range sizes {
		for i := 2; i <= size; i++ {
			if n *= i; n > maxOrders {
				return maxOrders + 1
			}
		}
	}
	return n
}

// permute tries every order of each pair of adjacent layers, or of a single
// layer when the pair has too many orders, that keeps groups contiguous and in
// groupOrder, keeping the one with the fewest crossings, until
// nothing improves. At this size it escapes the local minima that median
// sweeps and transpose leave, such as two sources sharing two targets.
func (l *layered) permute() {
	for improved := true; improved && l.total() > 0; {
		improved = false
		for r := range l.layers {
			window := []int{r}
			switch {
			case r+1 < len(l.layers) && orders(len(l.layers[r]), len(l.layers[r+1])) <= maxOrders:
				window = append(window, r+1)
			case orders(len(l.layers[r])) > maxOrders:
				continue
			}
			from, to := r-1, window[len(window)-1]+1
			bestC := l.crossings(from, to)
			var best [][]int
			var try func(k int)
			try = func(k int) {
				if k == len(window) {
					if c := l.crossings(from, to); c < bestC {
						bestC = c
						best = [][]int{slices.Clone(l.layers[window[0]]), slices.Clone(l.layers[window[len(window)-1]])}
					}
					return
				}
				w := window[k]
				ly := l.layers[w]
				perms(slices.Clone(ly), 0, func(p []int) {
					if l.respectsGroups(p, l.groupOrder) {
						copy(ly, p)
						l.index(w)
						try(k + 1)
					}
				})
			}
			saved := [][]int{slices.Clone(l.layers[window[0]]), slices.Clone(l.layers[window[len(window)-1]])}
			try(0)
			if best != nil {
				saved, improved = best, true
			}
			copy(l.layers[window[0]], saved[0])
			copy(l.layers[window[len(window)-1]], saved[1])
			for _, w := range window {
				l.index(w)
			}
		}
	}
}

func perms(p []int, k int, f func([]int)) {
	if k == len(p) {
		f(p)
		return
	}
	for i := k; i < len(p); i++ {
		p[k], p[i] = p[i], p[k]
		perms(p, k+1, f)
		p[k], p[i] = p[i], p[k]
	}
}

func (l *layered) respectsGroups(ly []int, rank []int) bool {
	done := map[int]bool{}
	last := -1
	for i, v := range ly {
		g := l.group[v]
		if g < 0 || (i > 0 && l.group[ly[i-1]] == g) {
			continue
		}
		if done[g] || rank[g] < last {
			return false
		}
		done[g], last = true, rank[g]
	}
	return true
}

// assignRows gives every vertex an integer grid row, strictly increasing within
// a layer. Each pass moves vertices to the weighted median row of their
// neighbours, then restores the order with isotonic regression on row minus
// index.
func (l *layered) assignRows() {
	nv := len(l.layer)
	l.row = make([]int, nv)
	for _, ly := range l.layers {
		for i, v := range ly {
			l.row[v] = i
		}
	}
	cost := func(rows []int) float64 {
		c := 0.0
		for v := range rows {
			for _, w := range l.down[v] {
				c += l.omega(v, w) * math.Abs(float64(rows[v]-rows[w]))
			}
		}
		// Prefer the shorter of equally straight drawings.
		return c + 0.5*float64(slices.Max(rows))
	}
	l.constrain()
	best, bestC := slices.Clone(l.row), cost(l.row)
	for it := range 12 {
		for k := range l.layers {
			r := k
			if it%3 == 1 {
				r = len(l.layers) - 1 - k
			}
			ly := l.layers[r]
			d := make([]float64, len(ly))
			wt := make([]float64, len(ly))
			for i, v := range ly {
				nb := l.up[v]
				switch it % 3 {
				case 1:
					nb = l.down[v]
				case 2:
					nb = append(slices.Clone(l.up[v]), l.down[v]...)
				}
				d[i], wt[i] = float64(l.row[v]), 1
				if len(nb) > 0 {
					var ps []float64
					for _, u := range nb {
						for range int(l.omega(u, v)) {
							ps = append(ps, float64(l.row[u]))
						}
						wt[i] = max(wt[i], l.omega(u, v))
					}
					d[i] = median(ps)
				}
			}
			for i, z := range isotonic(d, wt) {
				l.row[ly[i]] = int(math.Round(z)) + i
			}
		}
		l.constrain()
		if c := cost(l.row); c < bestC {
			best, bestC = slices.Clone(l.row), c
		}
	}
	l.row = best
}

// isotonic returns the weighted least-squares nondecreasing fit of d[i]-i, by
// pooling adjacent violators.
func isotonic(d, w []float64) []float64 {
	type pool struct {
		sum, wt float64
		n       int
	}
	var ps []pool
	for i := range d {
		ps = append(ps, pool{(d[i] - float64(i)) * w[i], w[i], 1})
		for len(ps) > 1 && ps[len(ps)-2].sum/ps[len(ps)-2].wt > ps[len(ps)-1].sum/ps[len(ps)-1].wt {
			a, b := ps[len(ps)-2], ps[len(ps)-1]
			ps = append(ps[:len(ps)-2], pool{a.sum + b.sum, a.wt + b.wt, a.n + b.n})
		}
	}
	var out []float64
	for _, p := range ps {
		for range p.n {
			out = append(out, p.sum/p.wt)
		}
	}
	return out
}

// constrain moves rows down, as little as the constraints need, until every
// group spans a band of rows that holds, in the layers the group spans, only
// its members and its own dummies, and groups whose layers overlap take
// disjoint bands in groupOrder. Group boxes then hold no other node and do not
// overlap.
//
// The constraints are differences between rows and the top and bottom row of
// each band, so the least solution above the current rows is a longest path,
// taken in topological order. Above and below a group's block follow the
// layer order; in a layer of its span where a group has no vertex, the split
// falls between the groups ranked before and after it, nearest the band's
// current middle. Within a layer the splits of the groups spanning it then
// follow groupOrder, and the constraints are acyclic: a path from a band
// limit through one layer to another band limit can only lead from the top
// to the bottom of one band or to a band ranked later, and a path through
// vertices alone runs down one layer.
func (l *layered) constrain() {
	nv, groups := len(l.layer), l.g.groups
	if groups == 0 {
		return
	}
	type limit struct{ from, to, gap int }
	var limits []limit
	top := func(g int) int { return nv + 2*g } // first row of the band of g
	bottom := func(g int) int { return nv + 2*g + 1 }
	for _, ly := range l.layers {
		for i := 0; i+1 < len(ly); i++ {
			limits = append(limits, limit{ly[i], ly[i+1], 1})
		}
	}
	span := make([][2]int, groups)
	middle := make([]float64, groups)
	for g := range span {
		lo, hi, r0, r1 := math.MaxInt, -1, math.MaxInt, math.MinInt
		for v, vg := range l.group[:l.n] {
			if vg == g {
				lo, hi = min(lo, l.layer[v]), max(hi, l.layer[v])
				r0, r1 = min(r0, l.row[v]), max(r1, l.row[v])
			}
		}
		span[g], middle[g] = [2]int{lo, hi}, float64(r0+r1)/2
	}
	byRank := make([]int, groups)
	for g, r := range l.groupOrder {
		byRank[r] = g
	}
	for r, ly := range l.layers {
		floor := 0 // the vertices before it are above every group placed so far
		for k, g := range byRank {
			if r < span[g][0] || r > span[g][1] {
				continue
			}
			start := slices.IndexFunc(ly, func(v int) bool { return l.group[v] == g })
			end := start
			if start >= 0 {
				for end < len(ly) && l.group[ly[end]] == g {
					end++
				}
			} else {
				// Split before the first block of a group ranked after g.
				limit := slices.IndexFunc(ly, func(v int) bool {
					return l.group[v] >= 0 && slices.Contains(byRank[k+1:], l.group[v])
				})
				if limit < 0 {
					limit = len(ly)
				}
				start = floor
				for start < limit && float64(l.row[ly[start]]) < middle[g] {
					start++
				}
				end = start
			}
			for _, v := range ly[:start] {
				limits = append(limits, limit{v, top(g), 1})
			}
			for _, v := range ly[start:end] {
				limits = append(limits, limit{top(g), v, 0}, limit{v, bottom(g), 0})
			}
			for _, v := range ly[end:] {
				limits = append(limits, limit{bottom(g), v, 1})
			}
			floor = end
		}
	}
	for i, g := range byRank {
		for _, h := range byRank[i+1:] {
			if span[g][0] <= span[h][1] && span[h][0] <= span[g][1] {
				limits = append(limits, limit{bottom(g), top(h), 1})
			}
		}
	}
	rows := append(slices.Clone(l.row), make([]int, 2*groups)...)
	for g := range groups {
		rows[top(g)], rows[bottom(g)] = math.MinInt/2, math.MinInt/2
	}
	out := make([][]limit, len(rows))
	indeg := make([]int, len(rows))
	for _, c := range limits {
		out[c.from] = append(out[c.from], c)
		indeg[c.to]++
	}
	var queue []int
	for v, d := range indeg {
		if d == 0 {
			queue = append(queue, v)
		}
	}
	for done := 0; done < len(rows); done++ {
		if len(queue) == 0 {
			panic("diagram: the group constraints of a flow form a cycle")
		}
		v := queue[0]
		queue = queue[1:]
		for _, c := range out[v] {
			rows[c.to] = max(rows[c.to], rows[v]+c.gap)
			if indeg[c.to]--; indeg[c.to] == 0 {
				queue = append(queue, c.to)
			}
		}
	}
	lo := slices.Min(rows[:nv])
	for v := range nv {
		l.row[v] = rows[v] - lo
	}
}

// Spacing of the flow geometry.
const (
	gapMin = 56.0
	// labelInset keeps an edge label clear of the columns and group borders
	// beside its gap.
	labelInset = 20.0
	emptyColW  = 24.0
	labelClear = 4.0 // between an edge label and its own line
	// widenBy is added to a gap whose labels find no free place, at most
	// widenTimes times.
	widenBy, widenTimes = 40.0, 3
	groupPadX           = 12.0
	groupPadY           = 10.0
)

// fit places the graph, widening the gaps whose labels find no free place.
func (l *layered) fit(rowGap float64, titles []float64) geometry {
	if l.widen == nil {
		l.widen = make([]float64, len(l.layers))
	}
	for try := 0; ; try++ {
		geo, crowded := l.place(rowGap, titles)
		if len(crowded) == 0 || try == widenTimes {
			return geo
		}
		for _, c := range crowded {
			l.widen[c] += widenBy
		}
	}
}

// place turns the layered graph into geometry and lists the gaps holding a
// label that found no free place. rowGap is the vertical space between the
// boxes of adjacent rows and titles the height of each group's title, which
// its box adds above the members; the left column starts at x = 0 and the top
// row at y = 0.
func (l *layered) place(rowGap float64, titles []float64) (geometry, []int) {
	g := l.g
	colW := make([]float64, len(l.layers))
	maxH := 0.0
	for v, size := range g.nodes {
		colW[l.layer[v]] = max(colW[l.layer[v]], size.x)
		maxH = max(maxH, size.y)
	}
	gap := make([]float64, len(l.layers))
	for k, c := range l.chains {
		for _, e := range l.origs[k] {
			gap[l.layer[c[0]]] = max(gap[l.layer[c[0]]], g.labels[e].x+2*labelInset)
		}
	}
	colX := make([]float64, len(l.layers))
	x := 0.0
	for c := range l.layers {
		if colW[c] == 0 {
			colW[c] = emptyColW
		}
		colX[c] = x + colW[c]/2
		x += colW[c] + max(gapMin, gap[c]) + l.widen[c]
	}
	rowH := maxH + rowGap
	rowY := func(r int) float64 { return float64(r)*rowH + maxH/2 }

	var geo geometry
	for v, size := range g.nodes {
		cx, cy := colX[l.layer[v]], rowY(l.row[v])
		geo.nodes = append(geo.nodes, rect{cx - size.x/2, cy - size.y/2, cx + size.x/2, cy + size.y/2})
	}
	geo.groups = make([]rect, g.groups)
	for i := range geo.groups {
		geo.groups[i] = rect{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	}
	for v, gr := range g.group {
		if gr >= 0 {
			r, b := geo.groups[gr], geo.nodes[v]
			geo.groups[gr] = rect{min(r.x0, b.x0), min(r.y0, b.y0), max(r.x1, b.x1), max(r.y1, b.y1)}
		}
	}
	for i, r := range geo.groups {
		title := 0.0
		if titles != nil {
			title = titles[i]
		}
		geo.groups[i] = rect{r.x0 - groupPadX, r.y0 - title, r.x1 + groupPadX, r.y1 + groupPadY}
	}

	port := l.ports(geo.nodes)
	geo.paths = make([][]point, len(g.edges))
	type gapSegment struct {
		p, q  point
		lower bool // the lower of parallel edges
		gap   int  // the layer the gap follows
	}
	segments := make([]gapSegment, len(g.edges))
	for k, c := range l.chains {
		for j, e := range l.origs[k] {
			off := (float64(j) - float64(len(l.origs[k])-1)/2) * 2 * pairOff
			y := func(v int) float64 {
				switch v {
				case c[0]:
					return rowY(l.row[v]) + port[[2]int{e, 0}]
				case c[len(c)-1]:
					return rowY(l.row[v]) + port[[2]int{e, 1}]
				}
				return rowY(l.row[v]) + off
			}
			// The tail leaves 2 px from its box and the arrow tip stops 3 px
			// short of its own.
			a, b := c[0], c[len(c)-1]
			ga, gb := 2.0, 3.0
			if l.reversed[e] {
				ga, gb = 3, 2
			}
			start, end := geo.nodes[a].x1+ga, geo.nodes[b].x0-gb
			p := []point{{start, y(a)}}
			for i := 0; i+1 < len(c); i++ {
				u, v := c[i], c[i+1]
				right := max(start, colX[l.layer[u]]+colW[l.layer[u]]/2)
				left := min(end, colX[l.layer[v]]-colW[l.layer[v]]/2)
				p = append(p, point{right, y(u)}, point{left, y(v)})
			}
			p = append(p, point{end, y(b)})
			segments[e] = gapSegment{p[1], p[2], off > 0, l.layer[c[0]]}
			if l.reversed[e] {
				slices.Reverse(p)
			}
			geo.paths[e] = slices.Compact(p)
		}
	}

	// Each label takes the first of its candidate places that keeps clear of
	// nodes, group titles and borders, other edges and the labels placed
	// before it, or the first candidate when none does.
	var titleRects []rect
	for i, r := range geo.groups {
		if titles != nil {
			titleRects = append(titleRects, rect{r.x0, r.y0, r.x1, r.y0 + titles[i]})
		}
	}
	geo.labels = make([]rect, len(g.edges))
	var crowded []int
	free := func(e int, c rect) bool {
		for _, r := range slices.Concat(geo.nodes, titleRects, geo.labels) {
			if overlaps(c, r) {
				return false
			}
		}
		for _, r := range geo.groups {
			if overlaps(c, r) && !(r.x0 <= c.x0 && c.x1 <= r.x1 && r.y0 <= c.y0 && c.y1 <= r.y1) {
				return false
			}
		}
		for other, p := range geo.paths {
			for i := 0; other != e && i+1 < len(p); i++ {
				if segmentHits(p[i], p[i+1], c) {
					return false
				}
			}
		}
		return true
	}
	for e, size := range g.labels {
		if size.x == 0 {
			continue
		}
		s := segments[e]
		var first rect
		for k, place := range labelPlaces(s.p, s.q, size, s.lower) {
			if k == 0 {
				first = place
			}
			if free(e, place) {
				geo.labels[e] = place
				break
			}
		}
		if geo.labels[e] == (rect{}) {
			geo.labels[e] = first
			crowded = append(crowded, s.gap)
		}
	}
	return geo, crowded
}

// labelPlaces lists the places for a label of size along the segment from p
// to q across a gap, best first: above the segment, or below it for the lower
// of two parallel edges, then the other side, each at the middle and then
// nearer either end.
func labelPlaces(p, q, size point, lower bool) []rect {
	var out []rect
	for _, below := range []bool{lower, !lower} {
		for _, t := range []float64{0.5, 0.3, 0.7, 0.15, 0.85} {
			out = append(out, labelAt(p, q, size, below, t))
		}
	}
	return out
}

// labelAt places a label of size in the gap crossed by the segment from p to
// q, above or below the segment at the fraction t of its length, beside that
// point on the side the segment slopes away from.
func labelAt(p, q, size point, lower bool, t float64) rect {
	mid := point{p.x + t*(q.x-p.x), p.y + t*(q.y-p.y)}
	x0 := mid.x - size.x/2
	if slope := q.y - p.y; math.Abs(slope) >= 12 {
		if (slope > 0) != lower {
			x0 = mid.x + labelClear
		} else {
			x0 = mid.x - labelClear - size.x
		}
	}
	x0 = max(p.x+labelInset, min(x0, q.x-labelInset-size.x))
	lineY := func(x float64) float64 {
		x = max(p.x, min(x, q.x))
		if q.x == p.x {
			return p.y
		}
		return p.y + (x-p.x)*(q.y-p.y)/(q.x-p.x)
	}
	if lower {
		y0 := max(lineY(x0), lineY(x0+size.x)) + labelClear
		return rect{x0, y0, x0 + size.x, y0 + size.y}
	}
	y1 := min(lineY(x0), lineY(x0+size.x)) - labelClear
	return rect{x0, y1 - size.y, x0 + size.x, y1}
}

// ports spreads the edges leaving one side of a node along that side, ordered
// by the row of the next vertex, so edges sharing a node do not cross there.
// The key is the edge and 0 for its left end or 1 for its right end.
func (l *layered) ports(boxes []rect) map[[2]int]float64 {
	type end struct {
		key    [2]int
		row, j int
		v      int
	}
	sides := map[[2]int][]end{}
	for k, c := range l.chains {
		for j, e := range l.origs[k] {
			a, b := c[0], c[len(c)-1]
			sides[[2]int{a, 1}] = append(sides[[2]int{a, 1}], end{[2]int{e, 0}, l.row[c[1]], j, a})
			sides[[2]int{b, 0}] = append(sides[[2]int{b, 0}], end{[2]int{e, 1}, l.row[c[len(c)-2]], j, b})
		}
	}
	out := map[[2]int]float64{}
	for _, es := range sides {
		sort.SliceStable(es, func(i, j int) bool {
			if es[i].row != es[j].row {
				return es[i].row < es[j].row
			}
			return es[i].j < es[j].j
		})
		step := 2.0 * pairOff
		if len(es) > 1 {
			step = min(step, (boxes[es[0].v].h()-16)/float64(len(es)-1))
		}
		for i, x := range es {
			out[x.key] = (float64(i) - float64(len(es)-1)/2) * step
		}
	}
	return out
}
