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
	if errs := RenderPhase(&phase, func(_ string, from, to int) (string, error) { return "first\\nsecond", nil }); len(errs) > 0 {
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
