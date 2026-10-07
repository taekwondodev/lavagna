package diagram

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
)

// missingEm is the width of a rune the font lacks. The browser draws it in a
// host fallback font whose width is not portable, so it gets 0.1 em of margin
// over the 1 em measured on one host.
const missingEm = 1.1

var be = binary.BigEndian

// Font measures labels set in a variable TrueType font with one wght axis: the
// hmtx advance, its HVAR delta at the avar-normalized weight, and the GPOS
// kern XAdvance with its GDEF variation delta. This matches what Chrome
// renders for SVG text with default kerning, to its 1/64 px quantization. GSUB
// substitutions and mark positioning are not applied.
type Font struct {
	unitsPerEm float64
	cmap       map[rune]uint16
	advances   []uint16
	axis       [3]float64   // wght minimum, default and maximum
	avar       [][2]float64 // normalized segment map of wght
	hvar       *varStore
	hvarMap    []uint32 // glyph to outer<<16|inner; nil maps the glyph id directly
	gdef       *varStore
	kern       [][]pairSubtable // per lookup, in LookupList order
}

// ParseFont reads the tables that label widths need from the font file bytes.
func ParseFont(b []byte) (*Font, error) {
	tables, err := directory(b)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"head", "cmap", "hhea", "hmtx", "maxp"} {
		if tables[name] == nil {
			return nil, fmt.Errorf("font: no %s table", name)
		}
	}
	f := &Font{unitsPerEm: float64(be.Uint16(tables["head"][18:]))}
	if err := f.parseCmap(tables["cmap"]); err != nil {
		return nil, err
	}
	metrics := int(be.Uint16(tables["hhea"][34:]))
	glyphs := int(be.Uint16(tables["maxp"][4:]))
	f.advances = make([]uint16, glyphs)
	for g := range glyphs {
		f.advances[g] = be.Uint16(tables["hmtx"][4*min(g, metrics-1):])
	}
	if fv := tables["fvar"]; fv != nil {
		off, count, size := int(be.Uint16(fv[4:])), int(be.Uint16(fv[8:])), int(be.Uint16(fv[10:]))
		for i := range count {
			a := fv[off+i*size:]
			if string(a[:4]) == "wght" {
				f.axis = [3]float64{fixed(a[4:]), fixed(a[8:]), fixed(a[12:])}
			}
		}
	}
	if av := tables["avar"]; av != nil {
		// A single-axis font: the first segment map belongs to wght.
		for i := range int(be.Uint16(av[8:])) {
			p := av[10+4*i:]
			f.avar = append(f.avar, [2]float64{f2dot14(p), f2dot14(p[2:])})
		}
	}
	if hv := tables["HVAR"]; hv != nil {
		if f.hvar, err = parseVarStore(hv, int(be.Uint32(hv[4:]))); err != nil {
			return nil, fmt.Errorf("font: HVAR: %w", err)
		}
		if off := int(be.Uint32(hv[8:])); off != 0 {
			f.hvarMap = parseDeltaSetIndexMap(hv[off:])
		}
	}
	if gd := tables["GDEF"]; gd != nil && be.Uint16(gd[2:]) >= 3 {
		if off := int(be.Uint32(gd[14:])); off != 0 {
			if f.gdef, err = parseVarStore(gd, off); err != nil {
				return nil, fmt.Errorf("font: GDEF: %w", err)
			}
		}
	}
	if gp := tables["GPOS"]; gp != nil {
		f.kern = parseKern(gp)
	}
	return f, nil
}

// Width is the advance width in px of label set at size px and weight. Kerning
// stops at a rune missing from the font.
func (f *Font) Width(label string, size, weight float64) float64 {
	n := f.normalize(weight)
	units, prev, kerned := 0.0, uint16(0), false
	for _, r := range label {
		g, ok := f.cmap[r]
		if !ok {
			units += missingEm * f.unitsPerEm
			kerned = false
			continue
		}
		units += f.advance(g, n)
		if kerned {
			units += f.kerning(prev, g, n)
		}
		prev, kerned = g, true
	}
	return units * size / f.unitsPerEm
}

func directory(b []byte) (map[string][]byte, error) {
	if len(b) < 12 {
		return nil, errors.New("font: short file")
	}
	n := int(be.Uint16(b[4:]))
	if len(b) < 12+16*n {
		return nil, errors.New("font: short table directory")
	}
	t := map[string][]byte{}
	for i := range n {
		r := b[12+16*i:]
		off, ln := be.Uint32(r[8:]), be.Uint32(r[12:])
		if uint64(off)+uint64(ln) > uint64(len(b)) {
			return nil, fmt.Errorf("font: table %q exceeds the file", r[:4])
		}
		t[string(r[:4])] = b[off : off+ln]
	}
	return t, nil
}

func fixed(b []byte) float64   { return float64(int32(be.Uint32(b))) / 65536 }
func f2dot14(b []byte) float64 { return float64(int16(be.Uint16(b))) / 16384 }

// parseCmap reads a format 12 subtable, else format 4, from the Unicode or
// Windows Unicode encodings.
func (f *Font) parseCmap(c []byte) error {
	var best []byte
	format := 0
	for i := range int(be.Uint16(c[2:])) {
		r := c[4+8*i:]
		pid, eid := be.Uint16(r), be.Uint16(r[2:])
		if pid != 0 && (pid != 3 || eid != 1 && eid != 10) {
			continue
		}
		st := c[be.Uint32(r[4:]):]
		if v := int(be.Uint16(st)); (v == 12 || v == 4) && v > format {
			best, format = st, v
		}
	}
	f.cmap = map[rune]uint16{}
	switch format {
	case 12:
		for i := range int(be.Uint32(best[12:])) {
			g := best[16+12*i:]
			start, end, gid := be.Uint32(g), be.Uint32(g[4:]), be.Uint32(g[8:])
			for cp := start; cp <= end; cp++ {
				f.cmap[rune(cp)] = uint16(gid + cp - start)
			}
		}
	case 4:
		segX2 := int(be.Uint16(best[6:]))
		for s := 0; s < segX2/2; s++ {
			end, start := int(be.Uint16(best[14+2*s:])), int(be.Uint16(best[16+segX2+2*s:]))
			delta := be.Uint16(best[16+2*segX2+2*s:])
			ro := int(be.Uint16(best[16+3*segX2+2*s:]))
			for cp := start; cp <= end && cp != 0xFFFF; cp++ {
				gid := uint16(cp) + delta
				if ro != 0 {
					if gid = be.Uint16(best[16+3*segX2+2*s+ro+2*(cp-start):]); gid != 0 {
						gid += delta
					}
				}
				if gid != 0 {
					f.cmap[rune(cp)] = gid
				}
			}
		}
	default:
		return errors.New("font: no Unicode cmap subtable of format 4 or 12")
	}
	return nil
}

// normalize maps a wght value to the normalized coordinate through avar,
// quantized to F2DOT14.
func (f *Font) normalize(wght float64) float64 {
	lo, def, hi := f.axis[0], f.axis[1], f.axis[2]
	v := 0.0
	switch {
	case wght < def:
		v = -(def - max(wght, lo)) / (def - lo)
	case wght > def:
		v = (min(wght, hi) - def) / (hi - def)
	}
	for i := 1; i < len(f.avar); i++ {
		a, b := f.avar[i-1], f.avar[i]
		if v <= b[0] {
			v = a[1] + (b[1]-a[1])*(v-a[0])/(b[0]-a[0])
			break
		}
	}
	return math.Round(v*16384) / 16384
}

func (f *Font) advance(g uint16, n float64) float64 {
	adv := float64(f.advances[g])
	if f.hvar == nil {
		return adv
	}
	idx := uint32(g)
	if f.hvarMap != nil {
		idx = f.hvarMap[min(int(g), len(f.hvarMap)-1)]
	}
	return adv + f.hvar.delta(idx, n)
}

// kerning sums the pair's XAdvance over every kern lookup; within a lookup the
// first matching subtable wins.
func (f *Font) kerning(left, right uint16, n float64) float64 {
	sum := 0.0
	for _, lookup := range f.kern {
		for _, st := range lookup {
			if v, ok := st.lookup(left, right, f.gdef, n); ok {
				sum += v
				break
			}
		}
	}
	return sum
}

// varStore is an item variation store over the single wght axis.
type varStore struct {
	regions [][3]float64 // start, peak, end
	data    []varData
}

type varData struct {
	regions []int
	deltas  [][]float64
}

func parseVarStore(table []byte, off int) (*varStore, error) {
	s := table[off:]
	if be.Uint16(s) != 1 {
		return nil, errors.New("unknown ItemVariationStore format")
	}
	vs := &varStore{}
	rl := s[be.Uint32(s[2:]):]
	if axes := int(be.Uint16(rl)); axes != 1 {
		return nil, fmt.Errorf("expected one axis, got %d", axes)
	}
	for i := range int(be.Uint16(rl[2:])) {
		r := rl[4+6*i:]
		vs.regions = append(vs.regions, [3]float64{f2dot14(r), f2dot14(r[2:]), f2dot14(r[4:])})
	}
	for i := range int(be.Uint16(s[6:])) {
		d := s[be.Uint32(s[8+4*i:]):]
		items, wc, ri := int(be.Uint16(d)), int(be.Uint16(d[2:])), int(be.Uint16(d[4:]))
		long, words := wc&0x8000 != 0, wc&0x7FFF
		vd := varData{}
		for j := range ri {
			vd.regions = append(vd.regions, int(be.Uint16(d[6+2*j:])))
		}
		p := 6 + 2*ri
		for range items {
			row := make([]float64, ri)
			for k := range ri {
				switch {
				case long && k < words:
					row[k] = float64(int32(be.Uint32(d[p:])))
					p += 4
				case long || k < words:
					row[k] = float64(int16(be.Uint16(d[p:])))
					p += 2
				default:
					row[k] = float64(int8(d[p]))
					p++
				}
			}
			vd.deltas = append(vd.deltas, row)
		}
		vs.data = append(vs.data, vd)
	}
	return vs, nil
}

func (vs *varStore) delta(idx uint32, n float64) float64 {
	outer, inner := int(idx>>16), int(idx&0xFFFF)
	if outer >= len(vs.data) || inner >= len(vs.data[outer].deltas) {
		return 0
	}
	vd := vs.data[outer]
	sum := 0.0
	for k, r := range vd.regions {
		sum += regionScalar(vs.regions[r], n) * vd.deltas[inner][k]
	}
	return sum
}

func regionScalar(r [3]float64, n float64) float64 {
	start, peak, end := r[0], r[1], r[2]
	switch {
	case start > peak || peak > end, start < 0 && end > 0 && peak != 0, peak == 0:
		return 1
	case n < start || n > end:
		return 0
	case n == peak:
		return 1
	case n < peak:
		return (n - start) / (peak - start)
	default:
		return (end - n) / (end - peak)
	}
}

func parseDeltaSetIndexMap(m []byte) []uint32 {
	format, entry := m[0], m[1]
	count, p := int(be.Uint16(m[2:])), 4
	if format != 0 {
		count, p = int(be.Uint32(m[2:])), 6
	}
	size := int(entry&0x30>>4) + 1
	innerBits := uint(entry&0x0F) + 1
	out := make([]uint32, count)
	for i := range count {
		var e uint32
		for k := range size {
			e = e<<8 | uint32(m[p+i*size+k])
		}
		out[i] = e>>innerBits<<16 | e&(1<<innerBits-1)
	}
	return out
}

// pairSubtable is a GPOS PairPos subtable of format 1 or 2.
type pairSubtable struct {
	t        []byte
	format   int
	vf1, vf2 uint16
	cov      map[uint16]int
}

func parseKern(gp []byte) [][]pairSubtable {
	features := gp[be.Uint16(gp[6:]):]
	lookupList := gp[be.Uint16(gp[8:]):]
	var lookups []int
	for i := range int(be.Uint16(features)) {
		r := features[2+6*i:]
		if string(r[:4]) != "kern" {
			continue
		}
		feature := features[be.Uint16(r[4:]):]
		for j := range int(be.Uint16(feature[2:])) {
			if li := int(be.Uint16(feature[4+2*j:])); !slices.Contains(lookups, li) {
				lookups = append(lookups, li)
			}
		}
	}
	// Lookups apply in LookupList order, not feature order.
	slices.Sort(lookups)
	var out [][]pairSubtable
	for _, li := range lookups {
		l := lookupList[be.Uint16(lookupList[2+2*li:]):]
		typ := be.Uint16(l)
		var subs []pairSubtable
		for k := range int(be.Uint16(l[4:])) {
			st := l[be.Uint16(l[6+2*k:]):]
			if typ == 9 { // Extension
				if be.Uint16(st[2:]) != 2 {
					continue
				}
				st = st[be.Uint32(st[4:]):]
			} else if typ != 2 {
				continue
			}
			subs = append(subs, pairSubtable{t: st, format: int(be.Uint16(st)), vf1: be.Uint16(st[4:]), vf2: be.Uint16(st[6:]), cov: coverage(st[be.Uint16(st[2:]):])})
		}
		out = append(out, subs)
	}
	return out
}

func coverage(c []byte) map[uint16]int {
	m := map[uint16]int{}
	n := int(be.Uint16(c[2:]))
	if be.Uint16(c) == 1 {
		for i := range n {
			m[be.Uint16(c[4+2*i:])] = i
		}
		return m
	}
	for i := range n {
		r := c[4+6*i:]
		start, end, idx := int(be.Uint16(r)), int(be.Uint16(r[2:])), int(be.Uint16(r[4:]))
		for g := start; g <= end; g++ {
			m[uint16(g)] = idx + g - start
		}
	}
	return m
}

func classOf(c []byte, g uint16) int {
	if be.Uint16(c) == 1 {
		start, n := be.Uint16(c[2:]), be.Uint16(c[4:])
		if g >= start && g < start+n {
			return int(be.Uint16(c[6+2*int(g-start):]))
		}
		return 0
	}
	for i := range int(be.Uint16(c[2:])) {
		r := c[4+6*i:]
		if g >= be.Uint16(r) && g <= be.Uint16(r[2:]) {
			return int(be.Uint16(r[4:]))
		}
	}
	return 0
}

// valueSize is the byte size of a ValueRecord with format vf.
func valueSize(vf uint16) int {
	n := 0
	for b := vf; b != 0; b >>= 1 {
		n += int(b & 1)
	}
	return 2 * n
}

// xAdvance reads XAdvance and its variation delta from the ValueRecord v, whose
// device offsets are relative to base.
func xAdvance(base, v []byte, vf uint16, store *varStore, n float64) float64 {
	if vf&0x0004 == 0 {
		return 0
	}
	p := 2*int(vf&0x1) + 2*int(vf>>1&0x1)
	x := float64(int16(be.Uint16(v[p:])))
	if vf&0x0040 == 0 || store == nil {
		return x
	}
	// Skip XAdvance, YAdvance, XPlacement device and YPlacement device.
	q := p + 2 + 2*int(vf>>3&0x1) + 2*int(vf>>4&0x1) + 2*int(vf>>5&0x1)
	if off := be.Uint16(v[q:]); off != 0 {
		if d := base[off:]; be.Uint16(d[4:]) == 0x8000 { // VariationIndex table
			x += store.delta(uint32(be.Uint16(d))<<16|uint32(be.Uint16(d[2:])), n)
		}
	}
	return x
}

func (ps pairSubtable) lookup(l, r uint16, store *varStore, n float64) (float64, bool) {
	ci, ok := ps.cov[l]
	if !ok {
		return 0, false
	}
	s1, s2 := valueSize(ps.vf1), valueSize(ps.vf2)
	st := ps.t
	if ps.format == 1 {
		set := st[be.Uint16(st[10+2*ci:]):]
		rec := 2 + s1 + s2
		for i := range int(be.Uint16(set)) {
			if pr := set[2+rec*i:]; be.Uint16(pr) == r {
				return xAdvance(set, pr[2:], ps.vf1, store, n), true
			}
		}
		return 0, false
	}
	c1 := classOf(st[be.Uint16(st[8:]):], l)
	c2 := classOf(st[be.Uint16(st[10:]):], r)
	classes2 := int(be.Uint16(st[14:]))
	// Format 2 matches every pair whose first glyph is covered.
	return xAdvance(st, st[16+(c1*classes2+c2)*(s1+s2):], ps.vf1, store, n), true
}
