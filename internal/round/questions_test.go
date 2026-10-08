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
	if errs := AttachPhaseFiles(&phase, []File{{Name: "shared.js"}}); len(errs) == 0 {
		t.Fatal("accepted root-shared resource")
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

func TestQuestionPartsSplitBetweenShellAndFrame(t *testing.T) {
	phase, errs := ParsePhase([]byte(`# Cosa succede se crasha? {id="crash"}
Con un file per sessione resta un rischio.
Il file può restare a metà.
## Capire
Il salvataggio tronca il file.
## Confrontare
<div class="screen"></div>
## Decidere
- [atomic] Scrittura atomica {recommended}
  Il file è sempre vecchio o nuovo;
  un fsync per salvataggio.
  => Il crash avviene sul ` + "`.tmp`" + `.
- [journal] Journal con checksum
  => Si perde solo l'ultimo record.
- [none] Nessuna protezione

Risolve il caso con **poche righe**.
`))
	if errs != nil {
		t.Fatal(errs)
	}
	q := phase.Questions[0]
	if q.Lead != "Con un file per sessione resta un rischio.\nIl file può restare a metà." {
		t.Errorf("lead %q", q.Lead)
	}
	if q.Options[0].Detail != "Il file è sempre vecchio o nuovo; un fsync per salvataggio." || q.Options[1].Detail != "" {
		t.Errorf("details %q, %q", q.Options[0].Detail, q.Options[1].Detail)
	}
	if q.Reason != "Risolve il caso con **poche righe**." {
		t.Errorf("reason %q", q.Reason)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	html := phase.Questions[0].HTML
	for _, absent := range []string{"Cosa succede", "resta un rischio", "Decidere", "Scrittura atomica</", "poche righe"} {
		if strings.Contains(html, absent) {
			t.Errorf("frame document carries shell content %q: %s", absent, html)
		}
	}
	for _, want := range []string{
		`<section class="chapter role-information" id="capire">`,
		`<span class="chapter-index" aria-hidden="true">02</span><span class="chapter-name">Confrontare</span>`,
		`<button type="button" class="chip" data-variant="atomic" data-recommended="" title="Scrittura atomica" aria-pressed="false">A ★</button>`,
		`<button type="button" class="chip" data-variant="none" title="Nessuna protezione" aria-pressed="false">C</button>`,
		`<p class="effect" data-variant="atomic" hidden><strong>Con A:</strong> Il crash avviene sul <code>.tmp</code>.</p>`,
		`<div class="raw"><div class="raw-stage">
<div class="screen"></div>
</div></div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("frame document lacks %s: %s", want, html)
		}
	}
	if strings.Contains(html, `data-variant="none" hidden`) {
		t.Errorf("an option without => has an effect line: %s", html)
	}
}

func TestEmptyChaptersAreHiddenFromTheFrame(t *testing.T) {
	phase, errs := ParsePhase([]byte("# Solo capire {id=\"a\"}\n## Capire\nIl problema.\n## Confrontare\n\n## Decidere\n- [x] X\n  => effetto\n- [y] Y\n# Solo decidere {id=\"b\"}\nUna premessa.\n## Decidere\n- [x] X\n- [y] Y\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	if a := phase.Questions[0].HTML; !strings.Contains(a, `id="capire"`) || strings.Contains(a, `id="confrontare"`) || strings.Contains(a, "Anteprima") {
		t.Errorf("empty 02 rendered: %s", a)
	}
	if b := phase.Questions[1].HTML; b != "" {
		t.Errorf("a question with only lead and 03 needs no frame document: %q", b)
	}
}

func TestDiagramsDrawNowIn01AndTheVariantShownIn02(t *testing.T) {
	phase, errs := ParsePhase([]byte(`# Cosa succede se crasha? {id="crash"}
## Capire
::: sequence Salvataggio
Sessione | a.json.tmp [atomic] | a.json
Sessione -> a.json: apre con O_TRUNC !1 [now]
Sessione -> a.json.tmp: scrive [atomic]
:::

::: bars Senza varianti
Scrittura: 4 ms
:::
## Decidere
- [atomic] Scrittura atomica {recommended}
- [journal] Journal
`))
	if errs != nil {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	html := phase.Questions[0].HTML
	capire, confrontare, ok := strings.Cut(html, `id="confrontare"`)
	if !ok {
		t.Fatalf("a diagram with variants needs 02 even without ## Confrontare: %s", html)
	}
	if strings.Count(capire, "<svg") != 2 || strings.Contains(capire, "diagram-variant") || strings.Contains(capire, ">scrive<") || !strings.Contains(capire, ">apre con O_TRUNC<") {
		t.Errorf("01 must draw only now: %s", capire)
	}
	if strings.Contains(confrontare, "Senza varianti") {
		t.Errorf("02 repeats a diagram without variants: %s", confrontare)
	}
	for _, want := range []string{
		`<figcaption class="content-label role-information">Salvataggio</figcaption><div class="diagram-variant" data-variant="now"><svg`,
		`<div class="diagram-variant" data-variant="atomic" hidden><svg`,
		`<div class="diagram-variant" data-variant="journal" hidden><svg`,
	} {
		if !strings.Contains(confrontare, want) {
			t.Errorf("02 lacks %s: %s", want, confrontare)
		}
	}
	if strings.Index(confrontare, `class="preview"`) > strings.Index(confrontare, "diagram-variant") {
		t.Errorf("the repeated diagrams follow the Anteprima chips: %s", confrontare)
	}
}

func TestDiagramsAuthoredIn02DrawEveryVariant(t *testing.T) {
	phase, errs := ParsePhase([]byte("# Q {id=\"q\"}\n## Confrontare\nTesto.\n\n::: bars\nA: 1 [a]\nB: 2\n:::\n## Decidere\n- [a] A\n- [b] B\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	html := phase.Questions[0].HTML
	if strings.Contains(html, `id="capire"`) || strings.Count(html, `class="diagram-variant"`) != 3 || !strings.Contains(html, `<figcaption class="content-label role-information">Barre</figcaption>`) {
		t.Errorf("02 diagram: %s", html)
	}
}

func TestDiagramErrorsCarryTheirRoundLine(t *testing.T) {
	phase, errs := ParsePhase([]byte("# Q {id=\"q\"}\n## Capire\n::: sequence\nA -> B: x [later]\n:::\n## Decidere\n- [a] A\n- [b] B\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	errs = RenderPhase(&phase, nil, nil)
	if len(errs) != 1 || !strings.HasPrefix(errs[0], `round.md:4: unknown variant "later"`) {
		t.Errorf("errors %v", errs)
	}
}

// TestGrammarDiagramExampleRenders keeps the example of --help grammar valid.
func TestGrammarDiagramExampleRenders(t *testing.T) {
	_, rest, _ := strings.Cut(Grammar, "## Diagrams")
	_, example, _ := strings.Cut(rest, "```text\n")
	example, _, _ = strings.Cut(example, "```")
	phase, errs := ParsePhase([]byte("# Q {id=\"q\"}\n## Capire\n" + example + "## Decidere\n- [atomic] Atomic\n- [journal] Journal\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	if html := phase.Questions[0].HTML; strings.Count(html, `class="diagram-variant"`) != 9 {
		t.Errorf("the three diagrams vary, so 02 draws 3 variants of each: %s", html)
	}
}

// TestFlowExampleOf17Renders draws the flow example of #17 in a question with
// options send and inside.
func TestFlowExampleOf17Renders(t *testing.T) {
	phase, errs := ParsePhase([]byte(`# Chi conosce la scelta prima del Send? {id="send"}
## Capire
::: flow
cli = lavagna CLI
frame = Frame sandbox
Agente -> cli: domande
cli -> Shell: pagina e token
cli -> frame: 01 e 02
Utente -> Shell: sceglie in 03
Shell -> cli: Send
Shell -> frame: id opzione ?1 [send]
Utente -> frame: sceglie di nuovo in 02 !1 [inside]
group Browser: Shell, frame
:::
## Decidere
- [send] Al Send
- [inside] Dentro il frame
`))
	if errs != nil {
		t.Fatal(errs)
	}
	if errs := RenderPhase(&phase, nil, nil); errs != nil {
		t.Fatal(errs)
	}
	capire, confrontare, ok := strings.Cut(phase.Questions[0].HTML, `id="confrontare"`)
	if !ok {
		t.Fatal("a flow with variants needs 02")
	}
	if !strings.Contains(capire, `<figure class="diagram flow"><figcaption class="content-label role-information">Flusso</figcaption><svg`) ||
		!strings.Contains(capire, ">lavagna CLI<") || strings.Contains(capire, ">id opzione<") || strings.Contains(capire, "dg-badge") {
		t.Errorf("01 draws now: %s", capire)
	}
	for _, v := range []string{"now", "send", "inside"} {
		if !strings.Contains(confrontare, `<div class="diagram-variant" data-variant="`+v+`"`) {
			t.Errorf("02 lacks variant %s: %s", v, confrontare)
		}
	}
	_, send, _ := strings.Cut(confrontare, `<div class="diagram-variant" data-variant="send"`)
	send, _, _ = strings.Cut(send, `<div class="diagram-variant" data-variant="inside"`)
	if !strings.Contains(send, `<g class="dg-risk">`) || !strings.Contains(send, ">id opzione<") || !strings.Contains(send, `class="dg-badge"`) {
		t.Errorf("send draws the risk ①: %s", send)
	}
}
