package round

import (
	"fmt"
	"html"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Phase is one authored batch of questions. Question source is retained so
// callers can version and render each question independently.
type Phase struct {
	Questions []PhaseQuestion
	Budget    int `json:"-"`
}

type PhaseQuestion struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	After     []string      `json:"after,omitempty"`
	Planned   bool          `json:"planned,omitempty"`
	Source    string        `json:"-"`
	HTML      string        `json:"-"`
	Options   []PhaseOption `json:"options,omitempty"`
	Resources []File        `json:"-"`
	FrameKey  string        `json:"-"`
	Frame     string        `json:"frame,omitempty"`
	StartLine int           `json:"-"`
}

type PhaseOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Effect      string `json:"effect,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

var (
	phaseIDRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	phaseMetaRE   = regexp.MustCompile(`^(.*?)\s+\{id="([^"]+)"(?:\s+after="([^"]*)")?\}$`)
	phaseOptionRE = regexp.MustCompile(`^- \[([^\]]+)\]\s*(.*)$`)
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
	for i, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "# ") {
			m := phaseMetaRE.FindStringSubmatch(strings.TrimSpace(line[2:]))
			if m == nil {
				errs = append(errs, fmt.Sprintf("round.md:%d: title needs {id=\"…\"}", i+1))
				continue
			}
			after := strings.Fields(m[3])
			sections = append(sections, section{line: i + 1, title: strings.TrimSpace(m[1]), id: m[2], after: after})
			continue
		}
		if len(sections) == 0 {
			if strings.TrimSpace(line) != "" {
				errs = append(errs, fmt.Sprintf("round.md:%d: content before first question", i+1))
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
	for i, line := range strings.Split(q.Source, "\n") {
		ln := start + 1 + i
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
		}
	}
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

func RenderPhase(phase *Phase, excerpt Excerpter) []string {
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
		p := parser{out: &strings.Builder{}, questions: map[string]bool{}, anchors: map[string]bool{}, in: Input{Files: files, Budget: remaining, Excerpt: excerpt}}
		p.out.WriteString("<article data-question=\"" + html.EscapeString(q.ID) + "\"><h1>" + html.EscapeString(q.Title) + "</h1>")
		chapter := ""
		var body []line
		flush := func() {
			if chapter == "" {
				for _, text := range body {
					if strings.TrimSpace(text.text) != "" {
						fmt.Fprintf(p.out, "<p>%s</p>", inline(strings.TrimSpace(text.text)))
					}
				}
			}
			if chapter != "" && (chapter == "Capire" || chapter == "Confrontare") {
				fmt.Fprintf(p.out, "<section class=\"chapter\"><h2>%s</h2><div class=\"chapter-body\">", html.EscapeString(chapter))
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
		p.out.WriteString("</article>")
		q.HTML = p.out.String()
		errs = append(errs, p.errs...)
		remaining -= p.used
	}
	return errs
}
