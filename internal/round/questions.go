package round

import (
	"fmt"
	"html"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxMessageBytes bounds one agent message, like the user's text per batch.
const MaxMessageBytes = 32 << 10

// Phase is one call: its questions and the call elements phase, reply and
// settled. Question source is retained so callers can version and render each
// question independently.
type Phase struct {
	Title     string `json:"title,omitempty"`
	TitleLine int    `json:"-"`
	Questions []PhaseQuestion
	Replies   []Element `json:"-"`
	Settled   []Element `json:"-"`
	Budget    int       `json:"-"`
}

// Element is one ::: reply [ID] or ::: settled ID [OPTION] block; Text is its body.
type Element struct {
	Line   int
	ID     string
	Option string
	Text   string
}

// RecapRow is one row of the decisions table rendered by ::: recap.
type RecapRow struct {
	Question, Decision, Round, Why string
	Rejected                       []string
	Struck                         bool
}

// PhaseQuestion is one question as authored. Lead, option labels, details and
// Reason are plain text for the shell; HTML holds only 01 Capire and
// 02 Confrontare, rendered for the content frame.
type PhaseQuestion struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	After     []string      `json:"after,omitempty"`
	Planned   bool          `json:"planned,omitempty"`
	Source    string        `json:"-"`
	HTML      string        `json:"-"`
	Lead      string        `json:"lead,omitempty"`
	Options   []PhaseOption `json:"options,omitempty"`
	Reason    string        `json:"reason,omitempty"`
	Resources []File        `json:"-"`
	FrameKey  string        `json:"-"`
	Frame     string        `json:"frame,omitempty"`
	StartLine int           `json:"-"`
}

// Size is the rendered size of the question: its frame document, the text the
// shell shows and its resources.
func (q PhaseQuestion) Size() int {
	n := len(q.HTML) + len(q.Title) + len(q.Lead) + len(q.Reason)
	for _, o := range q.Options {
		n += len(o.Label) + len(o.Detail) + len(o.Effect)
	}
	for _, r := range q.Resources {
		n += len(r.Body)
	}
	return n
}

type PhaseOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Detail      string `json:"detail,omitempty"`
	Effect      string `json:"effect,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

var (
	phaseIDRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	phaseMetaRE   = regexp.MustCompile(`^(.*?)\s+\{id="([^"]+)"(?:\s+after="([^"]*)")?\}$`)
	phaseOptionRE = regexp.MustCompile(`^- \[([^\]]+)\]\s*(.*)$`)
	callElementRE = regexp.MustCompile(`^::: (phase|reply|settled)(?:\s+(.*))?$`)
)

// ParsePhase validates and parses the per-question authoring format.
func ParsePhase(src []byte) (Phase, []string) { return ParsePhaseKnown(src, nil) }

// ParsePhaseKnown additionally accepts question ids already retained in the
// conversation ledger as valid dependencies.
func ParsePhaseKnown(src []byte, prior map[string][]string) (Phase, []string) {
	phase := Phase{Budget: MaxBytes - len(src)}
	if len(src) > MaxBytes {
		return phase, []string{fmt.Sprintf("round.md:1: input exceeds %d bytes", MaxBytes)}
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	type section struct {
		line      int
		title, id string
		after     []string
		body      []string
	}
	var sections []section
	var errs []string
	// inQuestion holds lines for the last section; skip drops the body of a
	// rejected title, already reported once.
	inQuestion, skip := false, false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		if m := callElementRE.FindStringSubmatch(line); m != nil {
			end := i + 1
			for end < len(lines) && strings.TrimRight(lines[end], " \t") != ":::" {
				end++
			}
			if end == len(lines) {
				errs = append(errs, fmt.Sprintf("round.md:%d: ::: %s is not closed", i+1, m[1]))
				break
			}
			phase.element(i+1, m[1], strings.TrimSpace(m[2]), strings.TrimSpace(strings.Join(lines[i+1:end], "\n")), &errs)
			i, inQuestion, skip = end, false, false
			continue
		}
		if strings.HasPrefix(line, "# ") {
			m := phaseMetaRE.FindStringSubmatch(strings.TrimSpace(line[2:]))
			inQuestion, skip = m != nil, m == nil
			if m == nil {
				errs = append(errs, fmt.Sprintf("round.md:%d: title needs {id=\"…\"}", i+1))
				continue
			}
			after := strings.Fields(m[3])
			sections = append(sections, section{line: i + 1, title: strings.TrimSpace(m[1]), id: m[2], after: after})
			continue
		}
		if !inQuestion {
			if !skip && strings.TrimSpace(line) != "" {
				errs = append(errs, fmt.Sprintf("round.md:%d: content outside a question", i+1))
			}
			continue
		}
		sections[len(sections)-1].body = append(sections[len(sections)-1].body, line)
	}
	known := map[string]bool{}
	for id := range prior {
		known[id] = true
	}
	for _, s := range sections {
		known[s.id] = true
	}
	seen := map[string]bool{}
	for _, s := range sections {
		if !phaseIDRE.MatchString(s.id) {
			errs = append(errs, fmt.Sprintf("round.md:%d: question id %q must match [a-z0-9][a-z0-9_-]{0,39}", s.line, s.id))
		}
		if seen[s.id] {
			errs = append(errs, fmt.Sprintf("round.md:%d: duplicate question id %q", s.line, s.id))
		}
		seen[s.id] = true
		if s.id == "now" {
			errs = append(errs, fmt.Sprintf("round.md:%d: question id now is reserved", s.line))
		}
		if s.title == "" || utf8.RuneCountInString(s.title) > 120 || strings.ContainsAny(s.title, "\r\n") {
			errs = append(errs, fmt.Sprintf("round.md:%d: title must be one line of at most 120 characters", s.line))
		}
		for _, id := range s.after {
			if !known[id] {
				errs = append(errs, fmt.Sprintf("round.md:%d: after names unknown question %q", s.line, id))
			}
		}
		for i, line := range s.body {
			ln := s.line + 1 + i
			if strings.Contains(line, "{ref=") || dataRef.MatchString(line) {
				errs = append(errs, fmt.Sprintf("round.md:%d: anchors are not supported", ln))
			}
			if strings.HasPrefix(line, "# ") {
				errs = append(errs, fmt.Sprintf("round.md:%d: level-1 headings start questions", ln))
			}
		}
		q := PhaseQuestion{ID: s.id, Title: s.title, After: s.after, Source: strings.Join(s.body, "\n"), StartLine: s.line}
		q.Planned = strings.TrimSpace(q.Source) == ""
		if !q.Planned {
			parseQuestionBody(&q, s.line, &errs)
		}
		phase.Questions = append(phase.Questions, q)
	}
	// Dependencies are directed from a question to prerequisites. Detect cycles
	// over the complete batch, independent of authoring order.
	graph := map[string][]string{}
	lineByID := map[string]int{}
	currentID := map[string]bool{}
	for id, after := range prior {
		graph[id] = after
		lineByID[id] = 1
	}
	for i, q := range phase.Questions {
		graph[q.ID] = q.After
		lineByID[q.ID] = sections[i].line
		currentID[q.ID] = true
	}
	state := map[string]uint8{}
	var stack []string
	var visit func(string)
	visit = func(id string) {
		if state[id] == 1 {
			line := lineByID[id]
			for i, active := range stack {
				if active == id {
					for _, candidate := range stack[i:] {
						if currentID[candidate] && (line == 1 || lineByID[candidate] < line) {
							line = lineByID[candidate]
						}
					}
					break
				}
			}
			errs = append(errs, fmt.Sprintf("round.md:%d: dependency cycle involving question %s", line, id))
			return
		}
		if state[id] == 2 {
			return
		}
		state[id] = 1
		stack = append(stack, id)
		for _, dep := range graph[id] {
			visit(dep)
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	ids := make([]string, 0, len(graph))
	for id := range graph {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		visit(id)
	}
	if len(errs) > 0 {
		return Phase{}, errs
	}
	return phase, nil
}

func (phase *Phase) element(line int, kind, args, body string, errs *[]string) {
	fail := func(format string, a ...any) {
		*errs = append(*errs, fmt.Sprintf("round.md:%d: ", line)+fmt.Sprintf(format, a...))
	}
	fields := strings.Fields(args)
	switch kind {
	case "phase":
		switch {
		case phase.TitleLine != 0:
			fail("only one ::: phase per call")
		case args == "" || utf8.RuneCountInString(args) > 120:
			fail("::: phase needs a title of at most 120 characters on its opening line")
		case body != "":
			fail("::: phase takes its title on the opening line and no body")
		}
		phase.Title, phase.TitleLine = args, line
	case "reply":
		e := Element{Line: line, Text: body}
		switch {
		case len(fields) > 1:
			fail("::: reply takes at most one question id")
		case len(fields) == 1 && !phaseIDRE.MatchString(fields[0]):
			fail("::: reply names invalid question id %q", fields[0])
		case body == "":
			fail("::: reply needs a message")
		case len(body) > MaxMessageBytes:
			fail("::: reply message exceeds %d bytes", MaxMessageBytes)
		}
		if len(fields) == 1 {
			e.ID = fields[0]
		}
		phase.Replies = append(phase.Replies, e)
	case "settled":
		if len(fields) < 1 || len(fields) > 2 {
			fail("::: settled needs a question id and at most one option id")
			return
		}
		e := Element{Line: line, ID: fields[0], Text: body}
		if len(fields) == 2 {
			e.Option = fields[1]
		}
		if !phaseIDRE.MatchString(e.ID) || e.Option != "" && !phaseIDRE.MatchString(e.Option) {
			fail("::: settled names an invalid question or option id")
		}
		for _, prior := range phase.Settled {
			if prior.ID == e.ID {
				fail("question %s is settled twice in one call", e.ID)
			}
		}
		phase.Settled = append(phase.Settled, e)
	}
}

// AttachPhaseFiles binds every resource to exactly one question. Root-level
// resources and resources for unknown questions are rejected rather than shared.
func AttachPhaseFiles(phase *Phase, files []File) []string {
	known := make(map[string]*PhaseQuestion, len(phase.Questions))
	for i := range phase.Questions {
		known[phase.Questions[i].ID] = &phase.Questions[i]
	}
	var errs []string
	if len(files)+1 > maxFiles {
		errs = append(errs, fmt.Sprintf("round: more than %d files", maxFiles))
		return errs
	}
	for _, file := range files {
		phase.Budget -= len(file.Body)
		if phase.Budget < 0 {
			errs = append(errs, fmt.Sprintf("round: input exceeds %d bytes", MaxBytes))
			return errs
		}
		owner, name, ok := strings.Cut(file.Name, "/")
		q := known[owner]
		if !ok || q == nil || name == "" || !fs.ValidPath(name) || name == "." || strings.Contains(name, `\`) || path.Clean(name) != name || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") {
			errs = append(errs, fmt.Sprintf("%s: resource must be under DIR/<question-id>/", file.Name))
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".js" && ext != ".css" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" && ext != ".svg" {
			errs = append(errs, fmt.Sprintf("%s: unsupported question resource", file.Name))
			continue
		}
		q.Resources = append(q.Resources, File{Name: name, Body: file.Body})
	}
	return errs
}

func parseQuestionBody(q *PhaseQuestion, start int, errs *[]string) {
	chapter := ""
	chapterOrder := map[string]int{"Capire": 1, "Confrontare": 2, "Decidere": 3}
	lastChapter := 0
	seenChapter := map[string]bool{}
	optionIDs := map[string]bool{}
	effects := map[int]bool{}
	recommended := 0
	current := -1
	var lead, reason []string
	for i, line := range strings.Split(q.Source, "\n") {
		ln := start + 1 + i
		if chapter == "" && !strings.HasPrefix(line, "## ") && strings.TrimSpace(line) != "" {
			lead = append(lead, strings.TrimSpace(line))
		}
		if strings.HasPrefix(line, "## ") {
			name := strings.TrimSpace(line[3:])
			if name != "Capire" && name != "Confrontare" && name != "Decidere" {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: unknown chapter %q", ln, name))
				continue
			}
			if seenChapter[name] || chapterOrder[name] < lastChapter {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: chapters must be unique and ordered Capire, Confrontare, Decidere", ln))
			}
			seenChapter[name] = true
			lastChapter = chapterOrder[name]
			chapter = name
			continue
		}
		if strings.HasPrefix(line, "### ") && chapter == "" {
			*errs = append(*errs, fmt.Sprintf("round.md:%d: content heading must be inside a chapter", ln))
		}
		if chapter != "Decidere" {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			m := phaseOptionRE.FindStringSubmatch(line)
			if m == nil {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: option needs [id]", ln))
				continue
			}
			id, label := m[1], strings.TrimSpace(m[2])
			if strings.Contains(label, "=>") {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: => belongs on an indented effect line", ln))
			}
			markerCount := strings.Count(label, "{recommended}")
			if markerCount > 1 {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: at most one {recommended} marker per option", ln))
			}
			rec := strings.HasSuffix(label, "{recommended}")
			if markerCount > 0 && !rec {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: {recommended} must end the option line", ln))
			}
			if rec {
				label = strings.TrimSpace(strings.TrimSuffix(label, "{recommended}"))
				recommended++
			}
			if !phaseIDRE.MatchString(id) || id == "now" {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: invalid or reserved option id %q", ln, id))
			}
			if optionIDs[id] {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: duplicate option id %q", ln, id))
			}
			optionIDs[id] = true
			if label == "" {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: option label is empty", ln))
			}
			q.Options = append(q.Options, PhaseOption{ID: id, Label: label, Recommended: rec})
			current = len(q.Options) - 1
		} else if strings.Contains(line, "=>") {
			if !strings.HasPrefix(line, "  ") {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: => effect lines must be indented", ln))
			}
			if current < 0 {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: effect line must follow an option", ln))
				continue
			}
			if strings.Count(line, "=>") > 1 || effects[current] {
				*errs = append(*errs, fmt.Sprintf("round.md:%d: at most one => effect per option", ln))
				continue
			}
			effects[current] = true
			q.Options[current].Effect = strings.TrimSpace(strings.SplitN(line, "=>", 2)[1])
		} else if strings.HasPrefix(line, "  ") && current >= 0 {
			q.Options[current].Detail = strings.TrimSpace(q.Options[current].Detail + " " + strings.TrimSpace(line))
		} else if strings.TrimSpace(line) != "" {
			reason = append(reason, strings.TrimSpace(line))
		}
	}
	q.Lead = strings.Join(lead, "\n")
	q.Reason = strings.Join(reason, "\n")
	if len(q.Options) < 2 || len(q.Options) > 6 {
		*errs = append(*errs, fmt.Sprintf("round.md:%d: question needs 2 to 6 options", start))
	}
	if recommended > 1 {
		*errs = append(*errs, fmt.Sprintf("round.md:%d: at most one option may be recommended", start))
	}
	if len(q.Options) == 0 {
		*errs = append(*errs, fmt.Sprintf("round.md:%d: non-planned question needs ## Decidere options", start))
	}
}

// frameChapters are the chapters rendered in the content frame. Title, lead
// and 03 Decidere stay in the shell.
var frameChapters = map[string]struct{ index, role, purpose string }{
	"Capire":      {"01", "role-information", "dov’è il problema"},
	"Confrontare": {"02", "role-analysis", "come cambia con la scelta"},
}

// optionKey is the letter the page shows for the option at index i.
func optionKey(i int) string { return string(rune('A' + i)) }

// preview renders the head of 02: one Anteprima chip per option and the
// effect line of each option. The frame helper shows the variant in use.
func preview(q *PhaseQuestion) string {
	var b strings.Builder
	b.WriteString(`<div class="preview" role="group" aria-label="Anteprima"><span class="preview-label">Anteprima</span>`)
	for i, o := range q.Options {
		star, recommended := "", ""
		if o.Recommended {
			star, recommended = " ★", ` data-recommended=""`
		}
		fmt.Fprintf(&b, `<button type="button" class="chip" data-variant="%s"%s title="%s" aria-pressed="false">%s%s</button>`,
			html.EscapeString(o.ID), recommended, html.EscapeString(o.Label), optionKey(i), star)
	}
	b.WriteString(`<span class="preview-note"></span></div>`)
	for i, o := range q.Options {
		if o.Effect != "" {
			fmt.Fprintf(&b, `<p class="effect" data-variant="%s" hidden><strong>Con %s:</strong> %s</p>`, html.EscapeString(o.ID), optionKey(i), inline(o.Effect))
		}
	}
	return b.String()
}

// RenderPhase renders every complete question of the call. recap holds the
// decisions table after the call's settled elements.
func RenderPhase(phase *Phase, excerpt Excerpter, recap []RecapRow) []string {
	var errs []string
	remaining := phase.Budget
	for i := range phase.Questions {
		q := &phase.Questions[i]
		if q.Planned {
			continue
		}
		files := map[string]bool{}
		for _, file := range q.Resources {
			files[q.ID+"/"+file.Name] = true
		}
		p := parser{out: &strings.Builder{}, questions: map[string]bool{}, anchors: map[string]bool{}, in: Input{Files: files, Budget: remaining, Excerpt: excerpt}, recap: recap, phase: true}
		chapter := ""
		var body []line
		// flush renders a frame chapter; one with no content is hidden.
		flush := func() {
			c, framed := frameChapters[chapter]
			empty := !slices.ContainsFunc(body, func(l line) bool { return strings.TrimSpace(l.text) != "" })
			if framed && !empty {
				id := strings.ToLower(chapter)
				fmt.Fprintf(p.out, `<section class="chapter %s" id="%s"><header class="chapter-header"><span class="chapter-index" aria-hidden="true">%s</span><span class="chapter-name">%s</span><span class="chapter-purpose">%s</span></header><div class="chapter-body">`,
					c.role, id, c.index, chapter, c.purpose)
				if chapter == "Confrontare" {
					p.out.WriteString(preview(q))
				}
				p.blocks(body, scope{frame: true})
				p.out.WriteString("</div></section>")
			}
			body = nil
		}
		for j, text := range strings.Split(q.Source, "\n") {
			if strings.HasPrefix(text, "## ") {
				flush()
				chapter = strings.TrimSpace(strings.TrimPrefix(text, "## "))
				continue
			}
			body = append(body, line{n: q.StartLine + 1 + j, text: text})
		}
		flush()
		q.HTML = p.out.String()
		errs = append(errs, p.errs...)
		remaining -= p.used
	}
	return errs
}
