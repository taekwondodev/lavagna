package diagram

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// The examples of #17, for a question with options atomic, journal and none.
const (
	sequenceExample = `Sessione A | a.json.tmp [atomic] | a.log [journal] | a.json [-journal] | Avvio successivo
Sessione A -> a.json: apre con O_TRUNC [now none]
Sessione A -> a.json: scrive 4 di 9 KB [now none]
note Sessione A: crash !1 [now none]
Sessione A -> a.json.tmp: scrive 9 KB e fsync [atomic]
note Sessione A: crash qui, a.json intatto [atomic]
Sessione A -> a.json: rename sul posto [atomic]
Sessione A -> a.log: append record e checksum [journal]
note Sessione A: crash, ultimo record incompleto [journal]
Avvio successivo -> a.json: legge [-journal]
Avvio successivo -> a.log: rilegge il log [journal]
a.json --> Avvio successivo: JSON troncato, errore !2 [now]
a.json --> Avvio successivo: vecchia o nuova, mai a metà [atomic]
a.log --> Avvio successivo: scarta il record non valido [journal]
a.json --> Avvio successivo: errore, riparte da zero ?2 [none]`

	barsExample = `Scrittura: 4 ms !1 [now none]
Scrittura su file temporaneo: 4 ms [atomic]
fsync: 5 ms +1 [atomic journal]
Rename: 0.1 ms [atomic]
Append al log: 1 ms [journal]
Compattazione del log: 2 ms ?2 [journal]`
)

var options = []Option{{"atomic", "A"}, {"journal", "B"}, {"none", "C"}}

// body numbers the lines of src from line 2, as below an opening line 1.
func body(src string) []Line {
	var lines []Line
	for i, text := range strings.Split(src, "\n") {
		lines = append(lines, Line{i + 2, text})
	}
	return lines
}

func parse(t *testing.T, kind, src string) *Diagram {
	t.Helper()
	d, errs := Parse(kind, "Titolo", 1, body(src), options)
	if errs != nil {
		t.Fatal(errs)
	}
	return d
}

// toneOf returns the tone class of the group that draws text, or "" when the
// drawing lacks it.
func toneOf(svg, text string) string {
	i := strings.Index(svg, ">"+text+"<")
	if i < 0 {
		return ""
	}
	g := strings.LastIndex(svg[:i], `<g class="`)
	if g < 0 {
		return "none"
	}
	class, _, _ := strings.Cut(svg[g+len(`<g class="`):], `"`)
	return class
}

func TestSequenceExampleDrawsEachVariant(t *testing.T) {
	f := testFont(t)
	d := parse(t, "sequence", sequenceExample)
	if !d.Varies() || strings.Join(d.Variants(), " ") != "now atomic journal none" {
		t.Fatalf("variants %v, varies %v", d.Variants(), d.Varies())
	}
	cases := []struct {
		variant string
		tones   map[string]string // label: tone class, "" when absent
	}{
		{"now", map[string]string{
			"apre con O_TRUNC": "dg-neutral", "crash": "dg-problem", "JSON troncato, errore": "dg-problem",
			"a.json.tmp": "", "a.log": "", "rename sul posto": "", "legge": "dg-neutral",
		}},
		{"atomic", map[string]string{
			"a.json.tmp": "dg-change", "scrive 9 KB e fsync": "dg-change", "rename sul posto": "dg-change",
			"legge": "dg-neutral", "apre con O_TRUNC": "", "JSON troncato, errore": "",
		}},
		{"journal", map[string]string{
			"a.log": "dg-change", "a.json": "", "legge": "", "scarta il record non valido": "dg-change",
		}},
		{"none", map[string]string{
			"apre con O_TRUNC": "dg-neutral", "crash": "dg-problem", "errore, riparte da zero": "dg-risk", "JSON troncato, errore": "",
		}},
	}
	for _, tc := range cases {
		svg := d.SVG(f, tc.variant)
		for label, want := range tc.tones {
			if got := toneOf(svg, label); got != want {
				t.Errorf("%s: %q drawn %q, want %q", tc.variant, label, got, want)
			}
		}
	}
	now := d.SVG(f, "now")
	if n := strings.Count(now, `<circle class="dg-badge"`); n != 2 {
		t.Errorf("now draws %d badges, want ① and ②", n)
	}
	if !regexp.MustCompile(`<circle class="dg-badge"[^>]*/><text class="dg-badge-text"[^>]*>1</text>`).MatchString(now) {
		t.Errorf("badge 1 is not an SVG circle: %s", now)
	}
}

func TestBarsExampleSharesOneScale(t *testing.T) {
	f := testFont(t)
	d := parse(t, "bars", barsExample)
	widths := map[string]string{}
	bar := regexp.MustCompile(`<rect class="dg-bar" x="[^"]+" y="[^"]+" width="([^"]+)"`)
	for _, v := range d.Variants() {
		svg := d.SVG(f, v)
		for _, m := range bar.FindAllStringSubmatch(svg, -1) {
			widths[v] += m[1] + " "
		}
		if !strings.HasPrefix(svg, `<svg xmlns="http://www.w3.org/2000/svg" class="diagram-svg" viewBox="0 0 `) {
			t.Errorf("%s: %s", v, svg)
		}
	}
	// 5 ms is the longest bar of every variant: 280 px.
	want := map[string]string{"now": "224 ", "atomic": "224 280 5.6 ", "journal": "280 56 112 ", "none": "224 "}
	for v, w := range want {
		if widths[v] != w {
			t.Errorf("%s: bar widths %q, want %q", v, widths[v], w)
		}
	}
	if got := toneOf(d.SVG(f, "journal"), "2 ms"); got != "dg-risk" {
		t.Errorf("?2 drawn %q", got)
	}
	if got := toneOf(d.SVG(f, "atomic"), "5 ms"); got != "dg-change" {
		t.Errorf("+1 drawn %q", got)
	}
}

func TestLegendIsGeneratedFromTheTonesDrawn(t *testing.T) {
	f := testFont(t)
	d := parse(t, "sequence", sequenceExample)
	legend := regexp.MustCompile(`<text class="dg-legend"[^>]*>([^<]+)</text>`)
	labels := func(v string) string {
		var out []string
		for _, m := range legend.FindAllStringSubmatch(d.SVG(f, v), -1) {
			out = append(out, m[1])
		}
		return strings.Join(out, ", ")
	}
	for v, want := range map[string]string{
		"now":     "problema, attuale",
		"atomic":  "cambia con A, invariato",
		"journal": "cambia con B, invariato",
		"none":    "problema, rischio, invariato",
	} {
		if got := labels(v); got != want {
			t.Errorf("%s legend %q, want %q", v, got, want)
		}
	}
	plain := parse(t, "sequence", "A -> B: ciao")
	if plain.Varies() || strings.Contains(plain.SVG(f, "now"), "dg-legend") {
		t.Error("a diagram without markers or variants needs no legend")
	}
}

func TestLabelsWrapToTheMeasuredWidth(t *testing.T) {
	f := testFont(t)
	label := strings.Repeat("parola ", 10) + "fine"
	lines := f.wrap(label, messageWidth, textStyle)
	if len(lines) < 2 || strings.Join(lines, " ") != label {
		t.Fatalf("wrapped %q", lines)
	}
	for _, l := range lines {
		if w := f.Width(l, textStyle.size, textStyle.weight); w > messageWidth {
			t.Errorf("line %q is %.2f px, above %v", l, w, messageWidth)
		}
	}
	if long := f.wrap(strings.Repeat("x", 80), 100, textStyle); len(long) < 2 {
		t.Errorf("a word wider than the line is not broken: %q", long)
	}
	svg := parse(t, "sequence", "A -> B: "+label).SVG(f, "now")
	if n := strings.Count(svg, "<tspan"); n != len(lines) {
		t.Errorf("the message is drawn on %d lines, want %d: %s", n, len(lines), svg)
	}
}

func TestSequenceSelfMessageAndImplicitParticipants(t *testing.T) {
	f := testFont(t)
	d := parse(t, "sequence", "Client -> Server: richiesta\nServer -> Server: valida\nServer --> Client: risposta")
	if len(d.participants) != 2 || d.participants[0].label != "Client" {
		t.Fatalf("participants %v", d.participants)
	}
	svg := d.SVG(f, "now")
	for _, want := range []string{">valida<", `stroke-dasharray="5 4"`, ">Client<", ">Server<"} {
		if !strings.Contains(svg, want) {
			t.Errorf("drawing lacks %s: %s", want, svg)
		}
	}
}

func TestGrammarErrorsCarryTheirLine(t *testing.T) {
	many := func(n int, format string) string {
		var lines []string
		for i := range n {
			lines = append(lines, fmt.Sprintf(format, i, i))
		}
		return strings.Join(lines, "\n")
	}
	cases := []struct {
		name, kind, src string
		line            int
		want            string
	}{
		{"unknown variant", "sequence", "A -> B: x [now other]", 2, `unknown variant "other"`},
		{"unknown excluded variant", "bars", "A: 1 [-later]", 2, `unknown variant "later"`},
		{"mixed tag", "bars", "A: 1\nB: 2 [now -atomic]", 3, "mixes included and excluded"},
		{"empty tag", "bars", "A: 1 []", 2, "empty variant tag"},
		{"unknown participant", "sequence", "A | B\nA -> C: x", 3, `unknown participant "C"`},
		{"participant absent from a variant", "sequence", "A | B [atomic]\nA -> B: x", 3, `participant "B" is absent from variant now`},
		{"nine participants", "sequence", "P0 | P1 | P2 | P3 | P4 | P5 | P6 | P7 | P8\nP0 -> P1: x", 2, "at most 8 participants"},
		{"nine implicit participants", "sequence", many(8, "P%d -> Q%d: x"), 6, "at most 8 participants"},
		{"forty-one lines", "sequence", many(41, "A -> B: %d%d"), 42, "at most 40 lines"},
		{"thirteen bars", "bars", many(13, "B%d: %d"), 14, "at most 12 bars"},
		{"negative value", "bars", "A: 1\nB: -2", 3, "non-negative"},
		{"unit differs", "bars", "A: 1 ms\nB: 2 s", 3, `unit "s" differs from "ms" on line 2`},
		{"label too long", "bars", strings.Repeat("x", 81) + ": 1", 2, "longer than 80 characters"},
		{"bar without value", "bars", "A: molto", 2, "a bar is Label: value unit"},
		{"unknown line", "sequence", "A -> B: x\nA => B", 3, "a sequence line is"},
		{"no message", "sequence", "A | B", 1, "at least one message"},
		{"no bar", "bars", "", 1, "at least one bar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := Parse(tc.kind, "", 1, body(tc.src), options)
			for _, e := range errs {
				if e.Line == tc.line && strings.Contains(e.Msg, tc.want) {
					return
				}
			}
			t.Errorf("want line %d %q, got %v", tc.line, tc.want, errs)
		})
	}
	if _, errs := Parse("bars", strings.Repeat("t", 81), 7, body("A: 1"), options); len(errs) != 1 || errs[0].Line != 7 {
		t.Errorf("a long title: %v", errs)
	}
}

func TestEmptyVariantsAndZeroValuesStillDraw(t *testing.T) {
	f := testFont(t)
	cases := []struct{ kind, src string }{
		{"sequence", "A [atomic] | B [atomic]\nA -> B: x [atomic]"},
		{"sequence", "A | B\nA -> B: x [atomic]"},
		{"bars", "A: 0 ms\nB: 0 ms"},
		{"bars", "A: 1 [atomic]"},
	}
	for _, tc := range cases {
		d := parse(t, tc.kind, tc.src)
		for _, v := range d.Variants() {
			if svg := d.SVG(f, v); !strings.HasPrefix(svg, "<svg") || strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
				t.Errorf("%s %q in %s: %s", tc.kind, tc.src, v, svg)
			}
		}
	}
}
