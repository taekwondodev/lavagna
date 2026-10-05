package round

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseAcceptsTextOnlyRound(t *testing.T) {
	src := `# Capire
## Il problema
**usa ` + "`make`" + ` ora** e 2 ** 3 e 4 ** 5.
#104 resta testo: a<b, m[i][j], *parola*, _parola_, [link](url), <https://x.it>, AT&T; e a\*b.
Lo stato vive in **un solo** file ` + "`state_*.json`" + `,
scritto da due sessioni: 2 * 3, a < b, R&D e snake_case restano testo.

::: info Esempio
### Due scritture
- prima
  continua
1. uno
:::

# Confrontare
::: info
Senza etichetta.
:::

# Decidere
Scegli se vuoi.

## Dove salviamo lo stato? {id="storage"}
Pensa alla pulizia.
- [file] Un file per sessione
  Nessuna contesa.
- [db] Un database locale

## Procediamo? {id="go"}
- [yes] Sì
- [no] No
`
	r, errs := Parse([]byte(src))
	if errs != nil {
		t.Fatal(errs)
	}
	var ids []string
	for _, c := range r.Chapters {
		ids = append(ids, c.ID+" "+c.Index+" "+c.Role)
	}
	if want := []string{"capire 01 role-information", "confrontare 02 role-analysis", "decidere 03 role-action"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("chapters %v, want %v", ids, want)
	}
	wantQ := []Question{{"storage", []string{"file", "db"}}, {"go", []string{"yes", "no"}}}
	if !reflect.DeepEqual(r.Questions, wantQ) {
		t.Errorf("questions %v, want %v", r.Questions, wantQ)
	}
	for _, want := range []string{
		`<h2>Il problema</h2>`,
		`<strong>un solo</strong>`,
		`<code>state_*.json</code>`,
		`2 * 3, a &lt; b, R&amp;D e snake_case restano testo.`,
		`<span class="content-label role-information">Esempio</span>`,
		`<h3>Due scritture</h3>`,
		`<li>prima continua</li>`,
		`<ol><li>uno</li></ol>`,
		`<span class="content-label role-information">Informazione</span>`,
		`Dove salviamo lo stato?`,
		`Pensa alla pulizia.`,
		`value="file" data-question="storage"`,
		`Nessuna contesa.`,
		`<strong>usa <code>make</code> ora</strong>`,
		`2 ** 3 e 4 ** 5`,
		`#104 resta testo: a&lt;b, m[i][j], *parola*, _parola_, [link](url), &lt;https://x.it&gt;, AT&amp;T; e a\*b.`,
	} {
		if !strings.Contains(r.HTML, want) {
			t.Errorf("HTML lacks %s", want)
		}
	}
}

func TestParseRejectsUnknownConstructs(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"unknown block", "# Capire\ntesto\n::: card\nx\n:::", []string{"round.md:3: unknown block ::: card"}},
		{"unknown chapter", "# Capire\n# Riepilogo", []string{`round.md:2: unknown chapter "Riepilogo" (use # Capire, # Confrontare or # Decidere)`}},
		{"chapter order", "# Decidere\n# Capire", []string{"round.md:2: chapter # Capire is out of order or repeated"}},
		{"content before chapters", "ciao\n# Capire", []string{"round.md:1: content before the first chapter"}},
		{"no chapter", "", []string{"round.md:1: no chapter: a round needs # Capire, # Confrontare or # Decidere"}},
		{"table", "# Capire\n| a | b |", []string{"round.md:2: unknown construct: table"}},
		{"table after paragraph", "# Capire\ntesto\n| a | b |", []string{"round.md:3: unknown construct: table"}},
		{"table without outer pipes", "# Capire\na | b\n--- | ---", []string{"round.md:3: unknown construct: table"}},
		{"code fence", "# Capire\n```go", []string{"round.md:2: unknown construct: code fence"}},
		{"indented code", "# Capire\n    codice", []string{"round.md:2: unknown construct: indented code"}},
		{"tab-indented list", "# Capire\n\t- tab", []string{"round.md:2: unknown construct: indented code"}},
		{"raw HTML", "# Capire\n<svg></svg>", []string{"round.md:2: unknown construct: raw HTML"}},
		{"quote", "# Capire\n> citazione", []string{"round.md:2: unknown construct: quote"}},
		{"star bullet", "# Capire\n* stella", []string{"round.md:2: unknown construct: list marker (use -)"}},
		{"plus bullet", "# Capire\n+ piu", []string{"round.md:2: unknown construct: list marker (use -)"}},
		{"thematic break", "# Capire\n---", []string{"round.md:2: unknown construct: thematic break"}},
		{"setext heading", "# Capire\nTitolo\n=====", []string{"round.md:3: unknown construct: setext heading"}},
		{"parenthesis list", "# Capire\n1) uno", []string{"round.md:2: unknown construct: list marker (use 1.)"}},
		{"space-indented list", "# Capire\n  - indentato", []string{"round.md:2: unknown construct: indented list"}},
		{"list inside a question", "# Decidere\n## Q {id=\"q\"}\n1. passo\n- [a] A\n- [b] B", []string{`round.md:3: a list inside question "q" (put it before the first question)`}},
		{"anchor", "# Capire\nPunto {ref=\"a\"}", []string{"round.md:2: unknown construct: attribute"}},
		{"deep heading", "# Capire\n#### x", []string{"round.md:2: unknown construct: heading level"}},
		{"nested list", "# Capire\n- a\n  - b", []string{"round.md:3: unknown construct: nested list"}},
		{"nested parenthesis list", "# Capire\n- a\n  1) b", []string{"round.md:3: unknown construct: nested list"}},
		{"nested block", "# Capire\n::: info\n::: info\n:::", []string{"round.md:3: nested ::: block"}},
		{"unclosed block", "# Capire\n::: info\ntesto", []string{"round.md:2: ::: info is not closed"}},
		{"option outside a question", "# Decidere\n- [a] A\n- [b] B", []string{`round.md:2: an option needs a question: ## Title {id="..."}`, `round.md:3: an option needs a question: ## Title {id="..."}`}},
		{"question without id", "# Decidere\n## Scegli\n- [a] A\n- [b] B", []string{`round.md:2: a question under # Decidere needs {id="..."}`}},
		{"option without id", "# Decidere\n## Scegli {id=\"q\"}\n- A\n- [b] B", []string{`round.md:3: an option needs an id: "- [id] label"`, `round.md:2: question "q" needs at least two options`}},
		{"repeated ids", "# Decidere\n## A {id=\"q\"}\n- [a] A\n- [a] B\n## B {id=\"q\"}\n- [x] X\n- [y] Y", []string{`round.md:4: option id "a" is repeated in question "q"`, `round.md:5: question id "q" is repeated`}},
		{"text after options", "# Decidere\n## A {id=\"q\"}\n- [a] A\n- [b] B\n\ndopo", []string{`round.md:6: text after the options of question "q"`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, errs := Parse([]byte(c.src)); !reflect.DeepEqual(errs, c.want) {
				t.Fatalf("errors\n%q\nwant\n%q", errs, c.want)
			}
		})
	}
}

func TestParseRefusesOversizedRound(t *testing.T) {
	_, errs := Parse([]byte("# Capire\n" + strings.Repeat("a", MaxBytes)))
	if len(errs) != 1 || !strings.Contains(errs[0], "exceeds") {
		t.Fatalf("got %q, want one bound error", errs)
	}
}
