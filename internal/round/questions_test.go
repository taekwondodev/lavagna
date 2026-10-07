package round

import (
	"strings"
	"testing"
)

func TestParsePhaseQuestionsAndPlannedTitle(t *testing.T) {
	phase, errs := ParsePhase([]byte(`# Save safely {id="storage"}
## Capire
The file can be truncated.
## Decidere
- [atomic] Atomic write {recommended}
  => The old file stays intact until rename.
- [journal] Append-only journal

# Follow up {id="follow" after="storage"}
`))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if len(phase.Questions) != 2 {
		t.Fatalf("questions: %#v", phase.Questions)
	}
	q := phase.Questions[0]
	if q.ID != "storage" || len(q.Options) != 2 || !q.Options[0].Recommended || q.Options[0].Effect == "" {
		t.Fatalf("question: %#v", q)
	}
	if !phase.Questions[1].Planned || phase.Questions[1].ID != "follow" {
		t.Fatalf("planned: %#v", phase.Questions[1])
	}
}

func TestQuestionLimitsAndRetiredSyntaxAreLineNumbered(t *testing.T) {
	base := func(options string) []byte { return []byte("# Q {id=\"q\"}\n## Decidere\n" + options) }
	cases := []struct {
		name   string
		source []byte
		want   string
	}{
		{"id pattern", []byte("# Q {id=\"Upper\"}\n"), "id \"Upper\""},
		{"title length", []byte("# " + strings.Repeat("x", 121) + " {id=\"q\"}\n"), "at most 120 characters"},
		{"duplicate question id", []byte("# First {id=\"q\"}\n# Second {id=\"q\"}\n"), "duplicate question id"},
		{"one option", base("- [a] A\n"), "2 to 6 options"},
		{"seven options", base("- [a] A\n- [b] B\n- [c] C\n- [d] D\n- [e] E\n- [f] F\n- [g] G\n"), "2 to 6 options"},
		{"multiple recommendations", base("- [a] A {recommended}\n- [b] B {recommended}\n"), "at most one option may be recommended"},
		{"reserved option", base("- [now] A\n- [b] B\n"), "reserved option id"},
		{"multiple effects", base("- [a] A\n  => first\n  => second\n- [b] B\n"), "at most one => effect"},
		{"unknown dependency", []byte("# Q {id=\"q\" after=\"missing\"}\n"), "after names unknown question"},
		{"legacy chapter", []byte("# Capire\n"), "title needs"},
		{"legacy reference", []byte("# Q {id=\"q\"}\n## Capire\nText {ref=\"old\"}\n## Decidere\n- [a] A\n- [b] B\n"), "anchors are not supported"},
		{"legacy data-ref", []byte("# Q {id=\"q\"}\n## Capire\n<div data-ref=\"old\"></div>\n## Decidere\n- [a] A\n- [b] B\n"), "anchors are not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := ParsePhase(tc.source)
			joined := strings.Join(errs, "\n")
			if !strings.Contains(joined, tc.want) || !strings.Contains(joined, "round.md:") {
				t.Fatalf("expected line-numbered %q, got %v", tc.want, errs)
			}
		})
	}
}

func TestParsePhaseAcceptsExactlyTheInputByteLimit(t *testing.T) {
	prefix := "# Q {id=\"q\"}\n## Capire\n"
	suffix := "\n## Decidere\n- [a] A\n- [b] B\n"
	source := prefix + strings.Repeat("x", MaxBytes-len(prefix)-len(suffix)) + suffix
	phase, errs := ParsePhase([]byte(source))
	if errs != nil || len(phase.Questions) != 1 {
		t.Fatalf("exactly %d bytes: %v", MaxBytes, errs)
	}
	if _, errs := ParsePhase([]byte(source + "x")); len(errs) == 0 || !strings.Contains(errs[0], "input exceeds") {
		t.Fatalf("accepted over-limit input: %v", errs)
	}
}

func TestParsePhaseReportsInvalidLines(t *testing.T) {
	input := `# Invalid {id="bad id"}
## Decidere
- [now] First
- [now] Second {recommended} {recommended}
- [third] Third => one => two
`
	_, errs := ParsePhase([]byte(input))
	if len(errs) < 4 {
		t.Fatalf("expected diagnostics, got %v", errs)
	}
	for _, err := range errs {
		if !strings.Contains(err, "round.md:") {
			t.Errorf("diagnostic lacks line: %q", err)
		}
	}
}

func TestQuestionExampleRendersExcerptMarksAndQuestionResources(t *testing.T) {
	source := []byte(`# Crash recovery {id="crash" after="storage"}
A partial write can corrupt state.
## Capire
::: excerpt main.go:1-2 1!1
:::
<img src="crash/screen.png" alt="screen">
## Decidere
- [atomic] Write atomically {recommended}
  => Keep the old copy until rename.
- [journal] Append records
# Storage {id="storage"}
`)
	phase, errs := ParsePhase(source)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	files := []File{{Name: "crash/screen.png", Body: []byte("png")}, {Name: "crash/view.js", Body: []byte("script")}}
	if errs := AttachPhaseFiles(&phase, files); len(errs) > 0 {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, func(_ string, from, to int) (string, error) { return "first\\nsecond", nil }, nil); len(errs) > 0 {
		t.Fatal(errs)
	}
	html := phase.Questions[0].HTML
	if !strings.Contains(html, `class="line problem" data-line="1"`) || !strings.Contains(html, `class="problem-badge">1`) || !strings.Contains(html, `src="crash/screen.png"`) {
		t.Fatalf("rendered question: %s", html)
	}
	if len(phase.Questions[0].Resources) != 2 || len(phase.Questions[1].Resources) != 0 {
		t.Fatalf("resources escaped question: %#v", phase.Questions)
	}
}

func TestQuestionIdsCannotDependOnThemselvesAndResourcesCannotTraverse(t *testing.T) {
	_, errs := ParsePhase([]byte("# Self {id=\"self\" after=\"self\"}\n## Decidere\n- [a] A\n- [b] B\n"))
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "round.md:1: dependency cycle") {
		t.Fatalf("self-cycle not rejected with line number: %v", errs)
	}
	phase, errs := ParsePhase([]byte("# Q {id=\"q\"}\n"))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, name := range []string{"q/../escape.js", "q/a/../../escape.js", "q/a\\\\escape.js"} {
		if errs := AttachPhaseFiles(&phase, []File{{Name: name}}); len(errs) == 0 {
			t.Errorf("accepted traversal path %q", name)
		}
	}
}

func TestPhaseResourcesAreScopedToQuestion(t *testing.T) {
	phase, errs := ParsePhase([]byte("# One {id=\"one\"}\n## Decidere\n- [a] A\n- [b] B\n# Two {id=\"two\"}\n"))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	resource := File{Name: "one/screen.js", Body: []byte("one")}
	if errs := AttachPhaseFiles(&phase, []File{resource}); len(errs) != 0 {
		t.Fatal(errs)
	}
	if len(phase.Questions[0].Resources) != 1 || len(phase.Questions[1].Resources) != 0 {
		t.Fatalf("resource escaped its question: %#v", phase.Questions)
	}
	if errs := AttachPhaseFiles(&phase, []File{{Name: "shared.js"}}); len(errs) == 0 {
		t.Fatal("accepted root-shared resource")
	}
}

func TestParsePhaseAllowsDependenciesOnLedgerQuestionsAndDetectsCrossCallCycles(t *testing.T) {
	valid := []byte("# New {id=\"new\" after=\"old\"}\n## Decidere\n- [a] A\n- [b] B\n")
	if _, errs := ParsePhaseKnown(valid, map[string][]string{"old": nil}); len(errs) != 0 {
		t.Fatal(errs)
	}
	_, errs := ParsePhaseKnown(valid, map[string][]string{"old": {"new"}})
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "round.md:1: dependency cycle") {
		t.Fatalf("cross-call cycle lacks line diagnostic: %v", errs)
	}
}

func TestParsePhaseRejectsCycleAndLegacyAnchors(t *testing.T) {
	_, errs := ParsePhase([]byte(`# First {id="first" after="second"}
## Decidere
- [a] A
- [b] B
# Second {id="second" after="first"}
## Capire
See {ref="old"}
## Decidere
- [a] A
- [b] B
`))
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "cycle") || !strings.Contains(joined, "anchors") {
		t.Fatalf("missing expected errors: %v", errs)
	}
}

func TestParsePhaseCallElements(t *testing.T) {
	phase, errs := ParsePhase([]byte(`::: phase Archivio delle sessioni
:::
::: settled storage
Nessuna dipendenza nuova.
:::
::: settled retention week
:::
# Quanto teniamo le sessioni chiuse? {id="retention"}
## Decidere
- [day] 1 giorno
- [week] 7 giorni {recommended}

::: reply retention
Hai ragione sul weekend.
:::
::: reply
Grazie a tutti.
:::
`))
	if errs != nil {
		t.Fatal(errs)
	}
	if phase.Title != "Archivio delle sessioni" || phase.TitleLine != 1 {
		t.Fatalf("phase: %q %d", phase.Title, phase.TitleLine)
	}
	settled := []Element{{Line: 3, ID: "storage", Text: "Nessuna dipendenza nuova."}, {Line: 6, ID: "retention", Option: "week"}}
	if len(phase.Settled) != 2 || phase.Settled[0] != settled[0] || phase.Settled[1] != settled[1] {
		t.Fatalf("settled: %+v", phase.Settled)
	}
	replies := []Element{{Line: 13, ID: "retention", Text: "Hai ragione sul weekend."}, {Line: 16, Text: "Grazie a tutti."}}
	if len(phase.Replies) != 2 || phase.Replies[0] != replies[0] || phase.Replies[1] != replies[1] {
		t.Fatalf("replies: %+v", phase.Replies)
	}
	if len(phase.Questions) != 1 || strings.Contains(phase.Questions[0].Source, "reply") || len(phase.Questions[0].Options) != 2 {
		t.Fatalf("a call element ends the question before it: %+v", phase.Questions)
	}
	if empty, errs := ParsePhase(nil); errs != nil || len(empty.Questions)+len(empty.Replies)+len(empty.Settled) != 0 {
		t.Fatalf("an empty call is valid: %+v %v", empty, errs)
	}
}

func TestParsePhaseCallElementErrorsAreLineNumbered(t *testing.T) {
	cases := map[string]struct{ source, want string }{
		"unclosed":            {"::: reply q\ntext\n", "round.md:1: ::: reply is not closed"},
		"empty reply":         {"::: reply q\n:::\n", "round.md:1: ::: reply needs a message"},
		"reply id":            {"::: reply Bad\nx\n:::\n", `round.md:1: ::: reply names invalid question id "Bad"`},
		"oversized reply":     {"::: reply\n" + strings.Repeat("x", MaxMessageBytes+1) + "\n:::\n", "round.md:1: ::: reply message exceeds 32768 bytes"},
		"settled arguments":   {"::: settled\n:::\n", "round.md:1: ::: settled needs a question id"},
		"settled twice":       {"::: settled q\n:::\n::: settled q a\n:::\n", "round.md:3: question q is settled twice"},
		"phase body":          {"::: phase T\nbody\n:::\n", "round.md:1: ::: phase takes its title on the opening line"},
		"phase twice":         {"::: phase A\n:::\n::: phase B\n:::\n", "round.md:3: only one ::: phase per call"},
		"content after reply": {"::: reply\nx\n:::\nstray\n", "round.md:4: content outside a question"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, errs := ParsePhase([]byte(c.source))
			if !strings.Contains(strings.Join(errs, "\n"), c.want) {
				t.Fatalf("want %q, got %v", c.want, errs)
			}
		})
	}
}

func TestRecapRendersTheDecisionsTable(t *testing.T) {
	phase, errs := ParsePhase([]byte("# Confermi? {id=\"confirm\"}\n## Capire\n::: recap\n:::\n## Decidere\n- [yes] Sì\n- [fix] Correggo\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	rows := []RecapRow{
		{Question: "Dove salviamo?", Decision: "Database", Round: "r1", Why: "Serve SQL.", Rejected: []string{"File"}, Struck: true},
		{Question: "Dove salviamo?", Decision: "Un `file`", Round: "r2", Why: "Più semplice.", Rejected: []string{"Database", "<script>"}},
	}
	if errs := RenderPhase(&phase, nil, rows); errs != nil {
		t.Fatal(errs)
	}
	html := phase.Questions[0].HTML
	for _, want := range []string{
		`<th scope="col">Domanda</th><th scope="col">Decisione</th><th scope="col">Round</th><th scope="col">Perché</th><th scope="col">Scartate</th>`,
		`<tr class="struck"><th scope="row"><s>Dove salviamo?</s></th><td data-label="Decisione"><s>Database</s></td>`,
		`<td data-label="Decisione">Un <code>file</code></td><td data-label="Round">r2</td>`,
		`<td data-label="Scartate">Database, &lt;script&gt;</td>`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("recap lacks %s:\n%s", want, html)
		}
	}
	empty, _ := ParsePhase([]byte("# Confermi? {id=\"confirm\"}\n## Capire\n::: recap\n:::\n## Decidere\n- [yes] Sì\n- [fix] Correggo\n"))
	if errs := RenderPhase(&empty, nil, nil); errs != nil || !strings.Contains(empty.Questions[0].HTML, "Nessuna decisione ancora.") {
		t.Fatalf("empty recap: %v %s", errs, empty.Questions[0].HTML)
	}
	body, _ := ParsePhase([]byte("# Confermi? {id=\"confirm\"}\n## Capire\n::: recap\n| a |\n:::\n## Decidere\n- [yes] Sì\n- [fix] Correggo\n"))
	if errs := RenderPhase(&body, nil, nil); !strings.Contains(strings.Join(errs, "\n"), "round.md:3: ::: recap takes no label or body") {
		t.Fatalf("authored recap body: %v", errs)
	}
}
