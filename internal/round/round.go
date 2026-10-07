package round

import (
	"fmt"
	"html"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxBytes = 4 << 20

type Round struct {
	Content   string     `json:"-"`
	Decide    string     `json:"decide"`
	Chapters  []Chapter  `json:"chapters"`
	Questions []Question `json:"questions"`
	Anchors   []string   `json:"anchors"`
}

type Chapter struct {
	ID    string `json:"id"`
	Index string `json:"index"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type Question struct {
	ID      string   `json:"id"`
	Options []string `json:"options"`
}

type Excerpter func(path string, from, to int) (string, error)

type Input struct {
	Source  []byte
	Files   map[string]bool
	Budget  int
	Excerpt Excerpter
}

var chapters = []struct {
	Chapter
	purpose string
}{
	{Chapter{"capire", "01", "Capire", "role-information"}, "Spiegazione · il problema, prima delle alternative"},
	{Chapter{"confrontare", "02", "Confrontare", "role-analysis"}, "Analisi · differenze, proposte e limiti"},
	{Chapter{"decidere", "03", "Decidere", "role-action"}, "La tua scelta · facoltativa, mai implicita"},
}

type component struct {
	class, role, label string
}

var components = map[string]component{
	"info":     {"problem", "role-information", "Informazione"},
	"proposal": {"recommendation", "role-analysis", "Proposta · non approvata"},
	"why":      {"representation", "role-neutral", "Perché questa forma"},
	"boundary": {"boundary", "role-neutral", "Confine"},
	"sequence": {"diagram sequence", "role-information", "Sequenza"},
	"flow":     {"diagram flow", "role-information", "Flusso"},
	"bars":     {"diagram bars", "role-information", "Barre"},
}

var evidenceKinds = map[string]component{
	"observed":   {"", "role-information", "Osservato"},
	"unverified": {"", "role-analysis", "Da verificare"},
	"outside":    {"", "role-neutral", "Fuori da questa scelta"},
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	questionSuffix = regexp.MustCompile(`\s*\{id="([^"]*)"\}$`)
	refSuffix      = regexp.MustCompile(`\s*\{ref="([^"]*)"\}$`)
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

type scope struct {
	nested, frame bool
}

type parser struct {
	errs      []string
	out       *strings.Builder
	round     Round
	questions map[string]bool
	anchors   map[string]bool
	in        Input
	used      int
	recap     []RecapRow
	phase     bool
}

func (p *parser) fail(n int, format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf("round.md:%d: %s", n, fmt.Sprintf(format, args...)))
}

func Parse(in Input) (Round, []string) {
	if len(in.Source) > MaxBytes {
		return Round{}, []string{fmt.Sprintf("round.md: %d bytes exceeds the %d byte bound", len(in.Source), MaxBytes)}
	}
	var lines []line
	for i, t := range strings.Split(strings.ReplaceAll(string(in.Source), "\r\n", "\n"), "\n") {
		lines = append(lines, line{i + 1, strings.TrimRight(t, " \t")})
	}

	p := &parser{
		questions: map[string]bool{},
		anchors:   map[string]bool{},
		in:        in,
		round:     Round{Chapters: []Chapter{}, Questions: []Question{}, Anchors: []string{}},
	}
	var content, decide strings.Builder
	next := 0
	current := -1
	var body []line
	flush := func() {
		if current >= 0 {
			p.out = &content
			if chapters[current].ID == "decidere" {
				p.out = &decide
			}
			p.chapter(current, body)
		}
		body = nil
	}
	for _, l := range lines {
		if name, ok := strings.CutPrefix(l.text, "# "); ok {
			name = strings.TrimSpace(name)
			found := -1
			for i, c := range chapters {
				if c.Name == name {
					found = i
				}
			}
			switch {
			case found == -1:
				p.fail(l.n, "unknown chapter %q (use # Capire, # Confrontare or # Decidere)", name)
			case found < next:
				p.fail(l.n, "chapter # %s is out of order or repeated", chapters[found].Name)
			default:
				flush()
				current = found
				next = found + 1
			}
			continue
		}
		if current == -1 {
			if l.text != "" {
				p.fail(l.n, "content before the first chapter")
			}
			continue
		}
		body = append(body, l)
	}
	flush()
	if current == -1 && len(p.errs) == 0 {
		p.fail(1, "no chapter: a round needs # Capire, # Confrontare or # Decidere")
	}
	if len(p.errs) > 0 {
		return Round{}, p.errs
	}
	p.round.Content = content.String()
	p.round.Decide = decide.String()
	return p.round, nil
}

func (p *parser) chapter(i int, body []line) {
	c := chapters[i]
	p.round.Chapters = append(p.round.Chapters, c.Chapter)
	fmt.Fprintf(p.out, `<section class="chapter %s" id="%s" aria-labelledby="%s-name">`, c.Role, c.ID, c.ID)
	fmt.Fprintf(p.out, `<header class="chapter-header"><span class="chapter-index" aria-hidden="true">%s</span><div><p class="chapter-name" id="%s-name">%s</p><p class="chapter-purpose">%s</p></div></header>`, c.Index, c.ID, c.Name, c.purpose)
	p.out.WriteString(`<div class="chapter-body">`)
	if c.ID == "decidere" {
		p.decide(body)
	} else {
		p.blocks(body, scope{frame: true})
	}
	p.out.WriteString(`</div></section>`)
}

func (p *parser) blocks(ls []line, sc scope) {
	for i := 0; i < len(ls); {
		l := ls[i]
		t := l.text
		switch {
		case t == "":
			i++
		case strings.HasPrefix(t, ":::"):
			i = p.component(ls, i, sc)
		case strings.HasPrefix(t, "### "):
			p.heading(l, "h3", t[4:], sc)
			i++
		case strings.HasPrefix(t, "## "):
			if sc.nested {
				p.fail(l.n, "## heading inside a ::: block (use ###)")
			}
			p.heading(l, "h2", t[3:], sc)
			i++
		case strings.HasPrefix(t, "|"):
			i = p.table(ls, i, sc)
		case rawHTML.MatchString(t):
			i = p.raw(ls, i, sc)
		case p.construct(l):
			i++
		case strings.HasPrefix(t, "- ") || orderedItem.MatchString(t):
			i = p.list(ls, i, sc)
		default:
			i = p.paragraph(ls, i, sc)
		}
	}
}

func (p *parser) heading(l line, tag, text string, sc scope) {
	text, attr := p.anchor(l.n, strings.TrimSpace(text), sc)
	p.plain(line{l.n, text})
	fmt.Fprintf(p.out, "<%s%s>%s</%s>", tag, attr, inline(text), tag)
}

func (p *parser) anchor(n int, text string, sc scope) (string, string) {
	m := refSuffix.FindStringSubmatch(text)
	if m == nil {
		return text, ""
	}
	rest := text[:len(text)-len(m[0])]
	if !sc.frame {
		p.fail(n, `{ref="..."} anchors belong in # Capire or # Confrontare`)
		return rest, ""
	}
	if !p.ref(n, m[1]) {
		return rest, ""
	}
	return rest, ` data-ref="` + html.EscapeString(m[1]) + `"`
}

func (p *parser) ref(n int, ref string) bool {
	switch {
	case strings.TrimSpace(ref) == "":
		p.fail(n, "empty anchor")
	case utf8.RuneCountInString(ref) > 80:
		p.fail(n, "anchor %q is longer than 80 characters", ref)
	case strings.IndexFunc(ref, unicode.IsControl) >= 0:
		p.fail(n, "anchor %q contains a control character", ref)
	case p.anchors[ref]:
		p.fail(n, "anchor %q is repeated", ref)
	default:
		p.anchors[ref] = true
		p.round.Anchors = append(p.round.Anchors, ref)
		return true
	}
	return false
}

func (p *parser) component(ls []line, i int, sc scope) int {
	l := ls[i]
	head, attr := p.anchor(l.n, strings.TrimSpace(strings.TrimPrefix(l.text, ":::")), sc)
	kind, label, _ := strings.Cut(head, " ")
	label = strings.TrimSpace(label)
	switch {
	case kind == "":
		p.fail(l.n, "::: closes no open block")
		return i + 1
	case sc.nested:
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
	inner := scope{nested: true, frame: sc.frame}
	if c, ok := components[kind]; ok {
		if label == "" {
			label = c.label
		}
		p.plain(line{l.n, label})
		fmt.Fprintf(p.out, `<div class="%s"%s><span class="content-label %s">%s</span>`, c.class, attr, c.role, inline(label))
		p.blocks(body, inner)
		p.out.WriteString(`</div>`)
		return end + 1
	}
	switch kind {
	case "evidence":
		if label != "" {
			p.fail(l.n, "::: evidence takes no label (label each item)")
		}
		p.evidence(body, attr, inner)
	case "steps":
		if label == "" {
			label = "Passaggi"
		}
		p.plain(line{l.n, label})
		p.steps(body, label, attr, inner)
	case "excerpt":
		p.excerpt(l.n, label, body, attr, inner)
	case "recap":
		if !p.phase {
			p.fail(l.n, "unknown block ::: %s", kind)
			break
		}
		if label != "" || strings.TrimSpace(joined(body)) != "" {
			p.fail(l.n, "::: recap takes no label or body; lavagna renders the decisions table")
		}
		p.recapTable(attr)
	default:
		p.fail(l.n, "unknown block ::: %s", kind)
	}
	return end + 1
}

func (p *parser) recapTable(attr string) {
	if len(p.recap) == 0 {
		fmt.Fprintf(p.out, `<p class="recap"%s>Nessuna decisione ancora.</p>`, attr)
		return
	}
	fmt.Fprintf(p.out, `<div class="table recap"%s><table><thead><tr><th scope="col">Domanda</th><th scope="col">Decisione</th><th scope="col">Round</th><th scope="col">Perché</th><th scope="col">Scartate</th></tr></thead><tbody>`, attr)
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

func (p *parser) items(ls []line, sc scope, each func(first line, rest []line, attr string)) {
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
		item := append([]line(nil), ls[i:end]...)
		last := &item[len(item)-1]
		var attr string
		last.text, attr = p.anchor(last.n, last.text, sc)
		for _, c := range item[1:] {
			p.continuation(c)
		}
		each(item[0], item[1:], attr)
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

func (p *parser) evidence(ls []line, attr string, sc scope) {
	fmt.Fprintf(p.out, `<div class="evidence"%s>`, attr)
	p.items(ls, sc, func(first line, rest []line, itemAttr string) {
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
		fmt.Fprintf(p.out, `<div class="evidence-item"%s><span class="content-label %s">%s</span><p>%s</p></div>`, itemAttr, k.role, inline(label), inline(text))
	})
	p.out.WriteString(`</div>`)
}

func (p *parser) steps(ls []line, label, attr string, sc scope) {
	fmt.Fprintf(p.out, `<ol class="simple-flow" aria-label="%s"%s>`, html.EscapeString(label), attr)
	p.items(ls, sc, func(first line, rest []line, itemAttr string) {
		title := strings.TrimSpace(listMarker.ReplaceAllString(first.text, ""))
		p.plain(line{first.n, title})
		fmt.Fprintf(p.out, `<li%s><strong>%s</strong>`, itemAttr, inline(title))
		if len(rest) > 0 {
			fmt.Fprintf(p.out, `<p>%s</p>`, inline(joined(rest)))
		}
		p.out.WriteString(`</li>`)
	})
	p.out.WriteString(`</ol>`)
}

func (p *parser) excerpt(n int, input string, body []line, attr string, sc scope) {
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
	if p.in.Excerpt == nil {
		p.fail(n, "excerpt rendering is unavailable")
		return
	}
	text, err := p.in.Excerpt(path, from, to)
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
	for i, l := range strings.Split(text, "\n") {
		line := from + i
		class, badge := "line", marks[line]
		if badge != "" {
			class = "line problem"
		}
		fmt.Fprintf(&rendered, `<span class="%s" data-line="%d">`, class, line)
		if badge != "" {
			fmt.Fprintf(&rendered, `<span class="problem-badge">%s</span>`, html.EscapeString(badge))
		}
		rendered.WriteString(html.EscapeString(l))
		rendered.WriteString(`</span>`)
		if p.used+rendered.Len() > p.in.Budget {
			p.fail(n, "excerpt %s: the round exceeds the %d byte bound", spec, MaxBytes)
			return
		}
	}
	p.used += rendered.Len()
	fmt.Fprintf(p.out, `<figure class="excerpt"%s><figcaption><code>%s</code></figcaption><pre><code>%s</code></pre>`, attr, html.EscapeString(spec), rendered.String())
	p.blocks(body, sc)
	p.out.WriteString(`</figure>`)
}

func (p *parser) table(ls []line, i int, sc scope) int {
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
		text, attr := p.cell(rows[0].n, cell, sc)
		labels[c] = html.EscapeString(text)
		fmt.Fprintf(p.out, `<th scope="col"%s%s>%s</th>`, align[c], attr, inline(text))
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
			text, attr := p.cell(row.n, cell, sc)
			if c == 0 {
				fmt.Fprintf(p.out, `<th scope="row"%s%s>%s</th>`, align[c], attr, inline(text))
				continue
			}
			fmt.Fprintf(p.out, `<td data-label="%s"%s%s>%s</td>`, labels[c], align[c], attr, inline(text))
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

func (p *parser) cell(n int, cell string, sc scope) (string, string) {
	text, attr := p.anchor(n, strings.TrimSpace(cell), sc)
	p.plain(line{n, text})
	return text, attr
}

func (p *parser) raw(ls []line, i int, sc scope) int {
	end := i
	for end < len(ls) && ls[end].text != "" {
		end++
	}
	if !sc.frame {
		p.fail(ls[i].n, "raw HTML belongs in # Capire or # Confrontare")
		return end
	}
	// The frame helper fits a raw block wider than the column inside its stage.
	p.out.WriteString(`<div class="raw"><div class="raw-stage">` + "\n")
	for _, l := range ls[i:end] {
		for _, m := range dataRef.FindAllStringSubmatch(l.text, -1) {
			p.ref(l.n, html.UnescapeString(m[1]+m[2]+m[3]))
		}
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
	case p.in.Files[file]:
	default:
		p.fail(n, "%s=%q is not a file of the round directory or a data: image", attr, ref)
	}
}

func (p *parser) paragraph(ls []line, i int, sc scope) int {
	end := i
	for end < len(ls) && ls[end].text != "" && (end == i || !startsBlock(ls[end].text) && unknown(ls[end].text) == "") {
		end++
	}
	para := append([]line(nil), ls[i:end]...)
	last := &para[len(para)-1]
	var attr string
	last.text, attr = p.anchor(last.n, last.text, sc)
	for _, l := range para {
		p.plain(l)
	}
	fmt.Fprintf(p.out, "<p%s>%s</p>", attr, inline(joined(para)))
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
	if strings.Contains(l.text, `{ref="`) || strings.Contains(l.text, `{id="`) {
		p.fail(l.n, "unknown construct: attribute")
	}
}

func (p *parser) list(ls []line, i int, sc scope) int {
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
	p.items(ls[i:end], sc, func(first line, rest []line, attr string) {
		if !sc.frame && !sc.nested && optionLine.MatchString(first.text) {
			p.fail(first.n, `an option needs a question: ## Title {id="..."}`)
		}
		item := line{first.n, listMarker.ReplaceAllString(first.text, "")}
		p.plain(item)
		fmt.Fprintf(p.out, "<li%s>%s</li>", attr, inline(joined(append([]line{item}, rest...))))
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

func (p *parser) decide(ls []line) {
	start := 0
	for start < len(ls) && !strings.HasPrefix(ls[start].text, "## ") {
		start++
	}
	p.blocks(ls[:start], scope{})
	for i := start; i < len(ls); {
		end := i + 1
		for end < len(ls) && !strings.HasPrefix(ls[end].text, "## ") {
			end++
		}
		p.question(ls[i], ls[i+1:end])
		i = end
	}
	if start < len(ls) {
		p.out.WriteString(`<p class="decision-help">La scelta entra direttamente nel prossimo invio. Non devi aggiungerla a una seconda lista.</p>`)
	}
}

func (p *parser) question(head line, ls []line) {
	title := strings.TrimSpace(strings.TrimPrefix(head.text, "## "))
	m := questionSuffix.FindStringSubmatch(title)
	if m == nil {
		p.fail(head.n, `a question under # Decidere needs {id="..."}`)
		return
	}
	id := m[1]
	title = strings.TrimSpace(title[:len(title)-len(m[0])])
	switch {
	case !idPattern.MatchString(id):
		p.fail(head.n, "question id %q must match %s", id, idPattern)
	case p.questions[id]:
		p.fail(head.n, "question id %q is repeated", id)
	}
	p.questions[id] = true
	p.plain(line{head.n, title})

	q := Question{ID: id, Options: []string{}}
	var intro []string
	var options strings.Builder
	seen := map[string]bool{}
	for i := 0; i < len(ls); {
		l := ls[i]
		switch {
		case l.text == "":
			i++
		case strings.HasPrefix(l.text, "- "):
			i = p.option(ls, i, &q, seen, &options)
		case len(q.Options) > 0:
			p.fail(l.n, "text after the options of question %q", id)
			i++
		case orderedItem.MatchString(l.text):
			p.fail(l.n, "a list inside question %q (put it before the first question)", id)
			i++
		default:
			if !p.construct(l) {
				p.plain(l)
				intro = append(intro, strings.TrimSpace(l.text))
			}
			i++
		}
	}
	if len(q.Options) < 2 {
		p.fail(head.n, "question %q needs at least two options", id)
	}
	p.round.Questions = append(p.round.Questions, q)
	fmt.Fprintf(p.out, `<fieldset class="question"><legend id="q-%s">%s</legend>`, id, inline(title))
	if len(intro) > 0 {
		fmt.Fprintf(p.out, `<p class="small muted">%s</p>`, inline(strings.Join(intro, " ")))
	}
	fmt.Fprintf(p.out, `<div class="options" role="radiogroup" aria-labelledby="q-%s">%s</div></fieldset>`, id, options.String())
}

func (p *parser) option(ls []line, i int, q *Question, seen map[string]bool, out *strings.Builder) int {
	l := ls[i]
	m := optionLine.FindStringSubmatch(l.text)
	if m == nil {
		p.fail(l.n, `an option needs an id: "- [id] label"`)
		return i + 1
	}
	id, label := m[1], strings.TrimSpace(m[2])
	switch {
	case !idPattern.MatchString(id):
		p.fail(l.n, "option id %q must match %s", id, idPattern)
	case seen[id]:
		p.fail(l.n, "option id %q is repeated in question %q", id, q.ID)
	case label == "":
		p.fail(l.n, "option %q has no label", id)
	}
	seen[id] = true
	p.plain(line{l.n, label})
	q.Options = append(q.Options, id)
	var detail []string
	i++
	for i < len(ls) && strings.HasPrefix(ls[i].text, "  ") {
		p.continuation(ls[i])
		detail = append(detail, strings.TrimSpace(ls[i].text))
		i++
	}
	fmt.Fprintf(out, `<label class="option"><input type="radio" name="q-%s" value="%s" data-question="%s"><span><strong>%s</strong>`, q.ID, id, q.ID, inline(label))
	if len(detail) > 0 {
		fmt.Fprintf(out, `<small>%s</small>`, inline(strings.Join(detail, " ")))
	}
	out.WriteString(`</span></label>`)
	return i
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
