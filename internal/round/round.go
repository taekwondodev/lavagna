package round

import (
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/taekwondodev/lavagna/internal/diagram"
	"github.com/taekwondodev/lavagna/internal/page"
)

const MaxBytes = 4 << 20

type Excerpter func(path string, from, to int) (string, error)

type component struct {
	class, role, label string
}

var components = map[string]component{
	"info":     {"problem", "role-information", "Informazione"},
	"proposal": {"recommendation", "role-analysis", "Proposta · non approvata"},
	"why":      {"representation", "role-neutral", "Perché questa forma"},
	"boundary": {"boundary", "role-neutral", "Confine"},
	"flow":     {"diagram flow", "role-information", "Flusso"},
}

var evidenceKinds = map[string]component{
	"observed":   {"", "role-information", "Osservato"},
	"unverified": {"", "role-analysis", "Da verificare"},
	"outside":    {"", "role-neutral", "Fuori da questa scelta"},
}

var (
	optionLine     = regexp.MustCompile(`^- \[([^\]]*)\]\s*(.*)$`)
	orderedItem    = regexp.MustCompile(`^\d+\. `)
	rawHTML        = regexp.MustCompile(`^</?[A-Za-z!]`)
	thematicBreak  = regexp.MustCompile(`^(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$`)
	setextLine     = regexp.MustCompile(`^=+$`)
	tableDelimiter = regexp.MustCompile(`^\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?$`)
	delimiterCell  = regexp.MustCompile(`^(:?)-+(:?)$`)
	atxHeading     = regexp.MustCompile(`^#+(?:\s|$)`)
	parenItem      = regexp.MustCompile(`^\d+\) `)
	listMarker     = regexp.MustCompile(`^(?:[-*+] |\d+[.)] )`)
	excerptSpec    = regexp.MustCompile(`^(\S+):(\d+)-(\d+)$`)
	excerptMark    = regexp.MustCompile(`^(\d+)!([1-9][0-9]*)$`)
	dataRef        = regexp.MustCompile(`(?i)(?:^|[\s"'])data-ref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	resourceAttr   = regexp.MustCompile(`(?i)(?:^|[\s"'])((?:xlink:)?href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
)

type line struct {
	n    int
	text string
}

type parser struct {
	errs       []string
	out        *strings.Builder
	files      map[string]bool
	budget     int
	repository Excerpter
	used       int
	recap      []RecapRow

	options []diagram.Option
	current string
	repeat  []string
}

func (p *parser) fail(n int, format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf("round.md:%d: %s", n, fmt.Sprintf(format, args...)))
}

func (p *parser) blocks(ls []line, nested bool) {
	for i := 0; i < len(ls); {
		l := ls[i]
		t := l.text
		switch {
		case t == "":
			i++
		case strings.HasPrefix(t, ":::"):
			i = p.component(ls, i, nested)
		case strings.HasPrefix(t, "### "):
			p.heading(l, t[4:])
			i++
		case strings.HasPrefix(t, "|"):
			i = p.table(ls, i)
		case rawHTML.MatchString(t):
			i = p.raw(ls, i)
		case p.construct(l):
			i++
		case strings.HasPrefix(t, "- ") || orderedItem.MatchString(t):
			i = p.list(ls, i)
		default:
			i = p.paragraph(ls, i)
		}
	}
}

func (p *parser) heading(l line, text string) {
	text = strings.TrimSpace(text)
	p.plain(line{l.n, text})
	fmt.Fprintf(p.out, "<h3>%s</h3>", inline(text))
}

func (p *parser) component(ls []line, i int, nested bool) int {
	l := ls[i]
	head := strings.TrimSpace(strings.TrimPrefix(l.text, ":::"))
	kind, label, _ := strings.Cut(head, " ")
	label = strings.TrimSpace(label)
	switch {
	case kind == "":
		p.fail(l.n, "::: closes no open block")
		return i + 1
	case nested:
		p.fail(l.n, "nested ::: block")
		return i + 1
	}
	end := i + 1
	for end < len(ls) && ls[end].text != ":::" {
		end++
	}
	if end == len(ls) {
		p.fail(l.n, "::: %s is not closed", kind)
		return end
	}
	body := ls[i+1 : end]
	if diagram.Kind(kind) {
		p.diagram(l.n, kind, label, body)
		return end + 1
	}
	if c, ok := components[kind]; ok {
		if label == "" {
			label = c.label
		}
		p.plain(line{l.n, label})
		fmt.Fprintf(p.out, `<div class="%s"><span class="content-label %s">%s</span>`, c.class, c.role, inline(label))
		p.blocks(body, true)
		p.out.WriteString(`</div>`)
		return end + 1
	}
	switch kind {
	case "evidence":
		if label != "" {
			p.fail(l.n, "::: evidence takes no label (label each item)")
		}
		p.evidence(body)
	case "steps":
		if label == "" {
			label = "Passaggi"
		}
		p.plain(line{l.n, label})
		p.steps(body, label)
	case "excerpt":
		p.excerpt(l.n, label, body)
	case "recap":
		if label != "" || strings.TrimSpace(joined(body)) != "" {
			p.fail(l.n, "::: recap takes no label or body; lavagna renders the decisions table")
		}
		p.recapTable()
	default:
		p.fail(l.n, "unknown block ::: %s", kind)
	}
	return end + 1
}

var diagramFont = sync.OnceValues(func() (*diagram.Font, error) {
	b, err := fs.ReadFile(page.Assets, page.Font)
	if err != nil {
		return nil, err
	}
	return diagram.ParseFont(b)
})

var diagramNames = map[string]string{"sequence": "Sequenza", "flow": "Flusso", "bars": "Barre"}

func (p *parser) diagram(n int, kind, title string, body []line) {
	lines := make([]diagram.Line, len(body))
	for i, l := range body {
		lines[i] = diagram.Line{N: l.n, Text: l.text}
	}
	d, errs := diagram.Parse(kind, title, n, lines, p.options)
	for _, e := range errs {
		p.fail(e.Line, "%s", e.Msg)
	}
	if d == nil {
		return
	}
	font, err := diagramFont()
	if err != nil {
		p.fail(n, "diagram font: %v", err)
		return
	}
	if title == "" {
		title = diagramNames[kind]
	}
	figure := func(variants []string) string {
		var b strings.Builder
		fmt.Fprintf(&b, `<figure class="diagram %s"><figcaption class="content-label role-information">%s</figcaption>`, kind, html.EscapeString(title))
		if len(variants) == 1 {
			b.WriteString(d.SVG(font, variants[0]))
		} else {
			for _, v := range variants {
				hidden := " hidden"
				if v == diagram.Now {
					hidden = ""
				}
				fmt.Fprintf(&b, `<div class="diagram-variant" data-variant="%s"%s>%s</div>`, html.EscapeString(v), hidden, d.SVG(font, v))
			}
		}
		b.WriteString(`</figure>`)
		return b.String()
	}
	out, repeat := figure([]string{diagram.Now}), ""
	switch {
	case d.Varies() && p.current == "Capire":
		repeat = figure(d.Variants())
	case d.Varies():
		out = figure(d.Variants())
	}
	if p.used+len(out)+len(repeat) > p.budget {
		p.fail(n, "::: %s: the round exceeds the %d byte bound", kind, MaxBytes)
		return
	}
	p.used += len(out) + len(repeat)
	p.out.WriteString(out)
	if repeat != "" {
		p.repeat = append(p.repeat, repeat)
	}
}

func (p *parser) recapTable() {
	if len(p.recap) == 0 {
		p.out.WriteString(`<p class="recap">Nessuna decisione ancora.</p>`)
		return
	}
	p.out.WriteString(`<div class="table recap"><table><thead><tr><th scope="col">Domanda</th><th scope="col">Decisione</th><th scope="col">Round</th><th scope="col">Perché</th><th scope="col">Scartate</th></tr></thead><tbody>`)
	for _, row := range p.recap {
		cell := func(s string) string {
			if row.Struck && s != "" {
				return "<s>" + inline(s) + "</s>"
			}
			return inline(s)
		}
		class := ""
		if row.Struck {
			class = ` class="struck"`
		}
		fmt.Fprintf(p.out, `<tr%s><th scope="row">%s</th><td data-label="Decisione">%s</td><td data-label="Round">%s</td><td data-label="Perché">%s</td><td data-label="Scartate">%s</td></tr>`,
			class, cell(row.Question), cell(row.Decision), cell(row.Round), cell(row.Why), cell(strings.Join(row.Rejected, ", ")))
	}
	p.out.WriteString(`</tbody></table></div>`)
}

func (p *parser) items(ls []line, each func(first line, rest []line)) {
	for i := 0; i < len(ls); {
		l := ls[i]
		if l.text == "" {
			i++
			continue
		}
		if !strings.HasPrefix(l.text, "- ") && !orderedItem.MatchString(l.text) {
			p.fail(l.n, "only list items belong in this block")
			i++
			continue
		}
		end := i + 1
		for end < len(ls) && strings.HasPrefix(ls[end].text, "  ") {
			end++
		}
		for _, c := range ls[i+1 : end] {
			p.continuation(c)
		}
		each(ls[i], ls[i+1:end])
		i = end
	}
}

func joined(ls []line) string {
	var parts []string
	for _, l := range ls {
		parts = append(parts, strings.TrimSpace(l.text))
	}
	return strings.Join(parts, " ")
}

func (p *parser) evidence(ls []line) {
	p.out.WriteString(`<div class="evidence">`)
	p.items(ls, func(first line, rest []line) {
		m := optionLine.FindStringSubmatch(first.text)
		if m == nil {
			p.fail(first.n, "an evidence item needs a kind: - [observed|unverified|outside] text")
			return
		}
		kind, label, _ := strings.Cut(m[1], " ")
		k, ok := evidenceKinds[kind]
		if !ok {
			p.fail(first.n, "unknown evidence kind %q (use observed, unverified or outside)", kind)
			return
		}
		if label = strings.TrimSpace(label); label == "" {
			label = k.label
		}
		p.plain(line{first.n, label})
		p.plain(line{first.n, m[2]})
		text := joined(append([]line{{first.n, m[2]}}, rest...))
		fmt.Fprintf(p.out, `<div class="evidence-item"><span class="content-label %s">%s</span><p>%s</p></div>`, k.role, inline(label), inline(text))
	})
	p.out.WriteString(`</div>`)
}

func (p *parser) steps(ls []line, label string) {
	fmt.Fprintf(p.out, `<ol class="simple-flow" aria-label="%s">`, html.EscapeString(label))
	p.items(ls, func(first line, rest []line) {
		title := strings.TrimSpace(listMarker.ReplaceAllString(first.text, ""))
		p.plain(line{first.n, title})
		fmt.Fprintf(p.out, `<li><strong>%s</strong>`, inline(title))
		if len(rest) > 0 {
			fmt.Fprintf(p.out, `<p>%s</p>`, inline(joined(rest)))
		}
		p.out.WriteString(`</li>`)
	})
	p.out.WriteString(`</ol>`)
}

func (p *parser) excerpt(n int, input string, body []line) {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		p.fail(n, "::: excerpt needs path:start-end")
		return
	}
	spec := parts[0]
	m := excerptSpec.FindStringSubmatch(spec)
	if m == nil {
		p.fail(n, "::: excerpt needs path:start-end")
		return
	}
	path := m[1]
	from, _ := strconv.Atoi(m[2])
	to, _ := strconv.Atoi(m[3])
	switch {
	case !fs.ValidPath(path) || path == ".":
		p.fail(n, "excerpt %s: the path must be relative to the git root, without ..", spec)
		return
	case from < 1 || to < from:
		p.fail(n, "excerpt %s: the range starts at line 1 or later and ends at or after its start", spec)
		return
	}
	if p.repository == nil {
		p.fail(n, "excerpt rendering is unavailable")
		return
	}
	text, err := p.repository(path, from, to)
	if err != nil {
		p.fail(n, "excerpt %s: %v", spec, err)
		return
	}
	marks := map[int]string{}
	for _, mark := range parts[1:] {
		m := excerptMark.FindStringSubmatch(mark)
		if m == nil {
			p.fail(n, "excerpt mark %q must be line!badge", mark)
			continue
		}
		line, _ := strconv.Atoi(m[1])
		badge := m[2]
		if line < from || line > to {
			p.fail(n, "excerpt mark %q is outside the excerpt", mark)
			continue
		}
		if _, exists := marks[line]; exists {
			p.fail(n, "excerpt line %d has more than one problem mark", line)
			continue
		}
		marks[line] = badge
	}
	var rendered strings.Builder
	for i, l := range highlight(path, text) {
		line := from + i
		class, badge := "line", marks[line]
		if badge != "" {
			class = "line problem"
		}
		fmt.Fprintf(&rendered, `<span class="%s" data-line="%d">`, class, line)
		if badge != "" {
			fmt.Fprintf(&rendered, `<span class="problem-badge">%s</span>`, html.EscapeString(badge))
		}
		rendered.WriteString(l)
		rendered.WriteString(`</span>`)
		if p.used+rendered.Len() > p.budget {
			p.fail(n, "excerpt %s: the round exceeds the %d byte bound", spec, MaxBytes)
			return
		}
	}
	p.used += rendered.Len()
	fmt.Fprintf(p.out, `<figure class="excerpt"><figcaption><code>%s</code></figcaption><pre><code>%s</code></pre>`, html.EscapeString(spec), rendered.String())
	p.blocks(body, true)
	p.out.WriteString(`</figure>`)
}

func (p *parser) table(ls []line, i int) int {
	end := i
	for end < len(ls) && strings.HasPrefix(ls[end].text, "|") {
		end++
	}
	rows := ls[i:end]
	if len(rows) < 2 {
		p.fail(rows[0].n, "a table needs a header row and a | --- | delimiter row")
		return end
	}
	header := p.cells(rows[0])
	delimiters := p.cells(rows[1])
	if header == nil || delimiters == nil {
		return end
	}
	if len(delimiters) != len(header) {
		p.fail(rows[1].n, "the delimiter row has %d cells, the header has %d", len(delimiters), len(header))
		return end
	}
	align := make([]string, len(header))
	for c, d := range delimiters {
		m := delimiterCell.FindStringSubmatch(strings.TrimSpace(d))
		switch {
		case m == nil:
			p.fail(rows[1].n, "a table needs a header row and a | --- | delimiter row")
			return end
		case m[1] != "" && m[2] != "":
			align[c] = ` class="align-center"`
		case m[2] != "":
			align[c] = ` class="align-right"`
		}
	}
	labels := make([]string, len(header))
	p.out.WriteString(`<div class="table"><table><thead><tr>`)
	for c, cell := range header {
		text := p.cell(rows[0].n, cell)
		labels[c] = html.EscapeString(text)
		fmt.Fprintf(p.out, `<th scope="col"%s>%s</th>`, align[c], inline(text))
	}
	p.out.WriteString(`</tr></thead><tbody>`)
	for _, row := range rows[2:] {
		cells := p.cells(row)
		if cells == nil {
			continue
		}
		if len(cells) != len(header) {
			p.fail(row.n, "the row has %d cells, the header has %d", len(cells), len(header))
			continue
		}
		p.out.WriteString(`<tr>`)
		for c, cell := range cells {
			text := p.cell(row.n, cell)
			if c == 0 {
				fmt.Fprintf(p.out, `<th scope="row"%s>%s</th>`, align[c], inline(text))
				continue
			}
			fmt.Fprintf(p.out, `<td data-label="%s"%s>%s</td>`, labels[c], align[c], inline(text))
		}
		p.out.WriteString(`</tr>`)
	}
	p.out.WriteString(`</tbody></table></div>`)
	return end
}

func (p *parser) cells(l line) []string {
	t := strings.TrimSpace(l.text)
	if len(t) < 2 || !strings.HasSuffix(t, "|") || strings.HasSuffix(t, `\|`) {
		p.fail(l.n, "a table row starts and ends with |")
		return nil
	}
	var cells []string
	var cell strings.Builder
	for i := 1; i < len(t)-1; i++ {
		switch {
		case t[i] == '\\' && t[i+1] == '|':
			cell.WriteByte('|')
			i++
		case t[i] == '|':
			cells = append(cells, cell.String())
			cell.Reset()
		default:
			cell.WriteByte(t[i])
		}
	}
	return append(cells, cell.String())
}

func (p *parser) cell(n int, cell string) string {
	text := strings.TrimSpace(cell)
	p.plain(line{n, text})
	return text
}

func (p *parser) raw(ls []line, i int) int {
	end := i
	for end < len(ls) && ls[end].text != "" {
		end++
	}
	p.out.WriteString(`<div class="raw"><div class="raw-stage">` + "\n")
	for _, l := range ls[i:end] {
		for _, m := range resourceAttr.FindAllStringSubmatch(l.text, -1) {
			p.resource(l.n, strings.ToLower(m[1]), html.UnescapeString(m[2]+m[3]+m[4]))
		}
		p.out.WriteString(l.text)
		p.out.WriteString("\n")
	}
	p.out.WriteString("</div></div>")
	return end
}

func (p *parser) resource(n int, attr, ref string) {
	file, _, _ := strings.Cut(strings.TrimPrefix(ref, "./"), "#")
	switch {
	case attr == "src" && strings.HasPrefix(ref, "data:"):
	case attr != "src" && strings.HasPrefix(ref, "#"):
	case p.files[file]:
	default:
		p.fail(n, "%s=%q is not a file of the round directory or a data: image", attr, ref)
	}
}

func (p *parser) paragraph(ls []line, i int) int {
	end := i
	for end < len(ls) && ls[end].text != "" && (end == i || !startsBlock(ls[end].text) && unknown(ls[end].text) == "") {
		end++
	}
	para := ls[i:end]
	for _, l := range para {
		p.plain(l)
	}
	fmt.Fprintf(p.out, "<p>%s</p>", inline(joined(para)))
	return end
}

func startsBlock(t string) bool {
	return atxHeading.MatchString(t) || strings.HasPrefix(t, ":::") || strings.HasPrefix(t, "- ") || orderedItem.MatchString(t)
}

func (p *parser) construct(l line) bool {
	what := unknown(l.text)
	if what != "" {
		p.fail(l.n, "unknown construct: %s", what)
	}
	return what != ""
}

func unknown(raw string) string {
	if strings.HasPrefix(raw, "    ") || strings.HasPrefix(raw, "\t") {
		return "indented code"
	}
	t := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, " ") && listMarker.MatchString(t):
		return "indented list"
	case atxHeading.MatchString(t):
		return "heading level"
	case strings.HasPrefix(t, ":::"):
		return "::: block here"
	case thematicBreak.MatchString(t):
		return "thematic break"
	case setextLine.MatchString(t):
		return "setext heading"
	case strings.HasPrefix(t, "* "), strings.HasPrefix(t, "+ "):
		return "list marker (use -)"
	case parenItem.MatchString(t):
		return "list marker (use 1.)"
	case strings.HasPrefix(t, "|"):
		return "table here"
	case tableDelimiter.MatchString(t):
		return "table row without outer |"
	case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
		return "code fence (use ::: excerpt)"
	case rawHTML.MatchString(t):
		return "raw HTML here"
	case strings.HasPrefix(t, ">"):
		return "quote"
	}
	return ""
}

func (p *parser) plain(l line) {
	if strings.Contains(l.text, `{id="`) {
		p.fail(l.n, "unknown construct: attribute")
	}
}

func (p *parser) list(ls []line, i int) int {
	ordered := !strings.HasPrefix(ls[i].text, "- ")
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	end := i
	for end < len(ls) {
		t := ls[end].text
		if ordered && !orderedItem.MatchString(t) || !ordered && !strings.HasPrefix(t, "- ") {
			break
		}
		end++
		for end < len(ls) && strings.HasPrefix(ls[end].text, "  ") {
			end++
		}
	}
	p.out.WriteString("<" + tag + ">")
	p.items(ls[i:end], func(first line, rest []line) {
		item := line{first.n, listMarker.ReplaceAllString(first.text, "")}
		p.plain(item)
		fmt.Fprintf(p.out, "<li>%s</li>", inline(joined(append([]line{item}, rest...))))
	})
	p.out.WriteString("</" + tag + ">")
	return end
}

func (p *parser) continuation(l line) {
	t := strings.TrimSpace(l.text)
	if listMarker.MatchString(t) {
		p.fail(l.n, "unknown construct: nested list")
		return
	}
	p.plain(l)
}

func inline(s string) string {
	ticks := strings.Count(s, "`")
	code := make([]bool, len(s))
	if ticks%2 == 0 {
		in := false
		for i := range len(s) {
			if s[i] == '`' {
				in = !in
				code[i] = true
				continue
			}
			code[i] = in
		}
	}
	strong := map[int]string{}
	open := -1
	for i := 0; i+1 < len(s); i++ {
		if code[i] || code[i+1] || s[i:i+2] != "**" {
			continue
		}
		canOpen := i+2 < len(s) && s[i+2] != ' '
		canClose := i > 0 && s[i-1] != ' '
		switch {
		case open >= 0 && canClose:
			strong[open], strong[i] = "<strong>", "</strong>"
			open = -1
		case canOpen:
			open = i
		}
		i++
	}
	var b strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		if tag, ok := strong[i]; ok {
			b.WriteString(tag)
			i++
			continue
		}
		if code[i] && s[i] == '`' {
			if in {
				b.WriteString("</code>")
			} else {
				b.WriteString("<code>")
			}
			in = !in
			continue
		}
		b.WriteString(html.EscapeString(s[i : i+1]))
	}
	return b.String()
}
