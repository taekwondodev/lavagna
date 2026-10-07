// Package diagram parses the sequence, flow and bars blocks of a question and
// draws each as one static SVG per variant: now, the present state, and every option
// of the question. Labels are measured with the embedded font, so the author
// writes no coordinates, sizes, line breaks or colours.
package diagram

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Now is the variant that draws the present state.
const Now = "now"

const maxLabel = 80

// Option is an option of the question, a variant a diagram can draw. Key is
// the letter the page shows for it.
type Option struct{ ID, Key string }

// Line is one source line of a block body.
type Line struct {
	N    int
	Text string
}

// Error is a grammar error on a source line.
type Error struct {
	Line int
	Msg  string
}

// Kind reports whether name opens a diagram block.
func Kind(name string) bool { return name == "sequence" || name == "flow" || name == "bars" }

// tone is how an element is drawn. A marker sets it; otherwise an element
// drawn in an option variant and absent from now is a change.
type tone uint8

const (
	neutral tone = iota
	change
	risk
	problem
)

var toneClass = [...]string{"dg-neutral", "dg-change", "dg-risk", "dg-problem"}

// element is what every diagram line shares: a label, a marker and the
// variants that draw it.
type element struct {
	line  int
	label string
	tone  tone
	badge int
	// only lists the variants of a [now a] tag, or with except those of [-a];
	// nil draws the element in every variant.
	only   []string
	except bool
}

func (e element) in(variant string) bool {
	return e.only == nil || slices.Contains(e.only, variant) != e.except
}

func (e element) toneIn(variant string) tone {
	if e.tone == neutral && variant != Now && !e.in(Now) {
		return change
	}
	return e.tone
}

// Diagram is a parsed block.
type Diagram struct {
	kind, title string
	options     []Option
	varies      bool

	participants []element
	steps        []step

	nodes  []element
	edges  []edge
	groups []group

	bars []bar
	unit string
}

// Varies reports whether the diagram declares variants, so 02 draws it for the
// variant shown.
func (d *Diagram) Varies() bool { return d.varies }

// Variants lists now and the option ids, in the order the page shows them.
func (d *Diagram) Variants() []string {
	ids := []string{Now}
	for _, o := range d.options {
		ids = append(ids, o.ID)
	}
	return ids
}

// SVG draws the diagram for variant.
func (d *Diagram) SVG(f *Font, variant string) string {
	switch d.kind {
	case "bars":
		return d.drawBars(f, variant)
	case "flow":
		return d.drawFlow(f, variant)
	}
	return d.drawSequence(f, variant)
}

var (
	tagRE    = regexp.MustCompile(`\s*\[([^\[\]]*)\]$`)
	markerRE = regexp.MustCompile(`(?:^|\s+)([!?+])([1-9][0-9]?)?$`)
)

type parser struct {
	d    *Diagram
	errs []Error
}

func (p *parser) fail(n int, format string, args ...any) {
	p.errs = append(p.errs, Error{n, fmt.Sprintf(format, args...)})
}

// Parse reads a block opened on line n with title and the given body, for a
// question with options.
func Parse(kind, title string, n int, body []Line, options []Option) (*Diagram, []Error) {
	p := &parser{d: &Diagram{kind: kind, title: title, options: options}}
	if title != "" {
		p.label(n, "title", title)
	}
	var lines []Line
	for _, l := range body {
		if strings.TrimSpace(l.Text) != "" {
			lines = append(lines, Line{l.N, strings.TrimSpace(l.Text)})
		}
	}
	switch kind {
	case "sequence":
		p.sequence(n, lines)
	case "flow":
		p.flow(n, lines)
	case "bars":
		p.barList(n, lines)
	default:
		p.fail(n, "unknown diagram ::: %s", kind)
	}
	if len(p.errs) > 0 {
		return nil, p.errs
	}
	return p.d, nil
}

// element splits the variant tag and the marker off the end of text and
// returns the rest.
func (p *parser) element(n int, text string) (element, string) {
	e := element{line: n}
	if m := tagRE.FindStringSubmatchIndex(text); m != nil {
		p.d.varies = true
		ids := strings.Fields(text[m[2]:m[3]])
		text = text[:m[0]]
		if len(ids) == 0 {
			p.fail(n, "empty variant tag []")
		}
		e.except = len(ids) > 0 && strings.HasPrefix(ids[0], "-")
		e.only = []string{}
		for _, id := range ids {
			if strings.HasPrefix(id, "-") != e.except {
				p.fail(n, "variant tag [%s] mixes included and excluded variants", strings.Join(ids, " "))
				break
			}
			id = strings.TrimPrefix(id, "-")
			if !slices.Contains(p.d.Variants(), id) {
				p.fail(n, "unknown variant %q (use now or an option id of the question: %s)", id, strings.Join(p.d.Variants()[1:], ", "))
			}
			e.only = append(e.only, id)
		}
	}
	if m := markerRE.FindStringSubmatchIndex(text); m != nil {
		e.tone = map[string]tone{"!": problem, "?": risk, "+": change}[text[m[2]:m[3]]]
		if m[4] >= 0 {
			e.badge, _ = strconv.Atoi(text[m[4]:m[5]])
		}
		text = text[:m[0]]
	}
	return e, strings.TrimSpace(text)
}

// label checks that text is a plain label of at most 80 characters.
func (p *parser) label(n int, what, text string) {
	switch {
	case text == "":
		p.fail(n, "%s is empty", what)
	case utf8.RuneCountInString(text) > maxLabel:
		p.fail(n, "%s %q is longer than %d characters", what, text, maxLabel)
	case strings.IndexFunc(text, unicode.IsControl) >= 0:
		p.fail(n, "%s %q contains a control character", what, text)
	}
}

// presentIn reports, for the line n drawing e, an element of of it names that
// is absent from a variant drawing e.
func (p *parser) presentIn(n int, e element, what string, of []element, names ...int) {
	for _, v := range p.d.Variants() {
		if !e.in(v) {
			continue
		}
		for _, i := range names {
			if part := of[i]; !part.in(v) {
				p.fail(n, "%s %q is absent from variant %s", what, part.label, v)
				return
			}
		}
	}
}
