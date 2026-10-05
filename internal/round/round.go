package round

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

const MaxBytes = 4 << 20

type Round struct {
	HTML      string     `json:"html"`
	Chapters  []Chapter  `json:"chapters"`
	Questions []Question `json:"questions"`
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

var chapters = []struct {
	Chapter
	purpose string
}{
	{Chapter{"capire", "01", "Capire", "role-information"}, "Spiegazione · il problema, prima delle alternative"},
	{Chapter{"confrontare", "02", "Confrontare", "role-analysis"}, "Analisi · differenze, proposte e limiti"},
	{Chapter{"decidere", "03", "Decidere", "role-action"}, "La tua scelta · facoltativa, mai implicita"},
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	questionSuffix = regexp.MustCompile(`\s*\{id="([^"]*)"\}$`)
	optionLine     = regexp.MustCompile(`^- \[([^\]]*)\]\s*(.*)$`)
	orderedItem    = regexp.MustCompile(`^\d+\. `)
	rawHTML        = regexp.MustCompile(`^</?[A-Za-z!]`)
	thematicBreak  = regexp.MustCompile(`^(?:(?:\*\s*){3,}|(?:-\s*){3,}|(?:_\s*){3,})$`)
	setextLine     = regexp.MustCompile(`^=+$`)
	tableDelimiter = regexp.MustCompile(`^\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?$`)
	atxHeading     = regexp.MustCompile(`^#+(?:\s|$)`)
	parenItem      = regexp.MustCompile(`^\d+\) `)
	listMarker     = regexp.MustCompile(`^(?:[-*+] |\d+[.)] )`)
)

type line struct {
	n    int
	text string
}

type parser struct {
	errs      []string
	out       strings.Builder
	round     Round
	questions map[string]bool
}

func (p *parser) fail(n int, format string, args ...any) {
	p.errs = append(p.errs, fmt.Sprintf("round.md:%d: %s", n, fmt.Sprintf(format, args...)))
}

func Parse(src []byte) (Round, []string) {
	if len(src) > MaxBytes {
		return Round{}, []string{fmt.Sprintf("round.md: %d bytes exceeds the %d byte bound", len(src), MaxBytes)}
	}
	var lines []line
	for i, t := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		lines = append(lines, line{i + 1, strings.TrimRight(t, " \t")})
	}

	p := &parser{questions: map[string]bool{}, round: Round{Chapters: []Chapter{}, Questions: []Question{}}}
	next := 0
	current := -1
	var body []line
	flush := func() {
		if current >= 0 {
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
	p.round.HTML = p.out.String()
	return p.round, nil
}

func (p *parser) chapter(i int, body []line) {
	c := chapters[i]
	p.round.Chapters = append(p.round.Chapters, c.Chapter)
	fmt.Fprintf(&p.out, `<section class="chapter %s" id="%s" aria-labelledby="%s-name">`, c.Role, c.ID, c.ID)
	fmt.Fprintf(&p.out, `<header class="chapter-header"><span class="chapter-index" aria-hidden="true">%s</span><div><p class="chapter-name" id="%s-name">%s</p><p class="chapter-purpose">%s</p></div></header>`, c.Index, c.ID, c.Name, c.purpose)
	p.out.WriteString(`<div class="chapter-body">`)
	if c.ID == "decidere" {
		p.decide(body)
	} else {
		p.blocks(body, false, false)
	}
	p.out.WriteString(`</div></section>`)
}

func (p *parser) blocks(ls []line, nested, deciding bool) {
	for i := 0; i < len(ls); {
		l := ls[i]
		t := l.text
		switch {
		case t == "":
			i++
		case strings.HasPrefix(t, ":::"):
			i = p.info(ls, i, nested)
		case strings.HasPrefix(t, "### "):
			p.plain(l)
			fmt.Fprintf(&p.out, "<h3>%s</h3>", inline(strings.TrimSpace(t[4:])))
			i++
		case strings.HasPrefix(t, "## "):
			if nested {
				p.fail(l.n, "## heading inside a ::: block (use ###)")
			}
			p.plain(l)
			fmt.Fprintf(&p.out, "<h2>%s</h2>", inline(strings.TrimSpace(t[3:])))
			i++
		case p.construct(l):
			i++
		case strings.HasPrefix(t, "- ") || orderedItem.MatchString(t):
			i = p.list(ls, i, deciding)
		default:
			i = p.paragraph(ls, i)
		}
	}
}

func (p *parser) info(ls []line, i int, nested bool) int {
	l := ls[i]
	kind, label, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(l.text, ":::")), " ")
	switch {
	case kind == "":
		p.fail(l.n, "::: closes no open block")
		return i + 1
	case nested:
		p.fail(l.n, "nested ::: block")
		return i + 1
	case kind != "info":
		p.fail(l.n, "unknown block ::: %s", kind)
	}
	end := i + 1
	for end < len(ls) && ls[end].text != ":::" {
		end++
	}
	if end == len(ls) {
		p.fail(l.n, "::: %s is not closed", kind)
		return end
	}
	if kind == "info" {
		label = strings.TrimSpace(label)
		if label == "" {
			label = "Informazione"
		}
		p.plain(line{l.n, label})
		fmt.Fprintf(&p.out, `<div class="problem"><span class="content-label role-information">%s</span>`, inline(label))
		p.blocks(ls[i+1:end], true, false)
		p.out.WriteString(`</div>`)
	}
	return end + 1
}

func (p *parser) paragraph(ls []line, i int) int {
	var text []string
	end := i
	for end < len(ls) && ls[end].text != "" && (end == i || !startsBlock(ls[end].text) && unknown(ls[end].text) == "") {
		p.plain(ls[end])
		text = append(text, strings.TrimSpace(ls[end].text))
		end++
	}
	fmt.Fprintf(&p.out, "<p>%s</p>", inline(strings.Join(text, " ")))
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
	case strings.HasPrefix(t, "|"), tableDelimiter.MatchString(t):
		return "table"
	case strings.HasPrefix(t, "```"), strings.HasPrefix(t, "~~~"):
		return "code fence"
	case rawHTML.MatchString(t):
		return "raw HTML"
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

func (p *parser) list(ls []line, i int, deciding bool) int {
	ordered := !strings.HasPrefix(ls[i].text, "- ")
	tag := "ul"
	if ordered {
		tag = "ol"
	}
	p.out.WriteString("<" + tag + ">")
	for i < len(ls) {
		t := ls[i].text
		var item string
		if ordered && orderedItem.MatchString(t) {
			item = orderedItem.ReplaceAllString(t, "")
		} else if !ordered && strings.HasPrefix(t, "- ") {
			item = t[2:]
		} else {
			break
		}
		if deciding && optionLine.MatchString(t) {
			p.fail(ls[i].n, `an option needs a question: ## Title {id="..."}`)
		}
		p.plain(ls[i])
		parts := []string{strings.TrimSpace(item)}
		i++
		for i < len(ls) && strings.HasPrefix(ls[i].text, "  ") {
			p.continuation(ls[i])
			parts = append(parts, strings.TrimSpace(ls[i].text))
			i++
		}
		fmt.Fprintf(&p.out, "<li>%s</li>", inline(strings.Join(parts, " ")))
	}
	p.out.WriteString("</" + tag + ">")
	return i
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
	p.blocks(ls[:start], false, true)
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
	fmt.Fprintf(&p.out, `<fieldset class="question"><legend id="q-%s">%s</legend>`, id, inline(title))
	if len(intro) > 0 {
		fmt.Fprintf(&p.out, `<p class="small muted">%s</p>`, inline(strings.Join(intro, " ")))
	}
	fmt.Fprintf(&p.out, `<div class="options" role="radiogroup" aria-labelledby="q-%s">%s</div></fieldset>`, id, options.String())
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
