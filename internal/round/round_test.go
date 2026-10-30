package round

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fakeRepository(path string, from, to int) (string, error) {
	if path != "main.go" {
		return "", errors.New("no such file")
	}
	var lines []string
	for n := from; n <= to; n++ {
		lines = append(lines, fmt.Sprintf("riga %d <%d>", n, n))
	}
	return strings.Join(lines, "\n"), nil
}

func question(t *testing.T, body string) (Phase, []string) {
	t.Helper()
	phase, errs := ParsePhase([]byte("# Q {id=\"q\"}\n## Capire\n" + body + "\n## Decidere\n- [a] A\n- [b] B\n"))
	if errs != nil {
		return phase, errs
	}
	if errs := AttachPhaseFiles(&phase, []File{{Name: "q/diagramma.svg"}, {Name: "q/a b.png"}}); errs != nil {
		t.Fatal(errs)
	}
	return phase, nil
}

func render(t *testing.T, body string) (string, []string) {
	t.Helper()
	phase, errs := question(t, body)
	if errs != nil {
		return "", errs
	}
	if errs := RenderPhase(&phase, fakeRepository, nil); errs != nil {
		return "", errs
	}
	return phase.Questions[0].HTML, nil
}

func TestBlocksRenderInlineMarkupAndEscapeTheRest(t *testing.T) {
	html, errs := render(t, `**usa `+"`make`"+` ora** e 2 ** 3 e 4 ** 5.
#104 resta testo: a<b, m[i][j], *parola*, _parola_, [link](url), <https://x.it>, AT&T; e a\*b.
Lo stato vive in **un solo** file `+"`state_*.json`"+`,
scritto da due sessioni: 2 * 3, a < b, R&D e snake_case restano testo.

::: info Esempio
### Due scritture
- prima
  continua
1. uno
:::

::: info
Senza etichetta.
:::`)
	if errs != nil {
		t.Fatal(errs)
	}
	for _, want := range []string{
		`<strong>un solo</strong>`,
		`<code>state_*.json</code>`,
		`2 * 3, a &lt; b, R&amp;D e snake_case restano testo.`,
		`<span class="content-label role-information">Esempio</span>`,
		`<h3>Due scritture</h3>`,
		`<li>prima continua</li>`,
		`<ol><li>uno</li></ol>`,
		`<span class="content-label role-information">Informazione</span>`,
		`<strong>usa <code>make</code> ora</strong>`,
		`2 ** 3 e 4 ** 5`,
		`#104 resta testo: a&lt;b, m[i][j], *parola*, _parola_, [link](url), &lt;https://x.it&gt;, AT&amp;T; e a\*b.`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %s", want)
		}
	}
}

func TestBlocksRejectUnknownConstructs(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"unknown block", "testo\n::: card\nx\n:::", []string{"round.md:4: unknown block ::: card"}},
		{"table without delimiter", "| a | b |", []string{"round.md:3: a table needs a header row and a | --- | delimiter row"}},
		{"table after paragraph", "testo\n| a | b |", []string{"round.md:4: a table needs a header row and a | --- | delimiter row"}},
		{"table without outer pipes", "a | b\n--- | ---", []string{"round.md:4: unknown construct: table row without outer |"}},
		{"table row without closing pipe", "| a | b |\n| --- | --- |\n| x | y", []string{"round.md:5: a table row starts and ends with |"}},
		{"table row width", "| a | b |\n| --- | --- |\n| x |", []string{"round.md:5: the row has 1 cells, the header has 2"}},
		{"table delimiter width", "| a | b |\n| --- |", []string{"round.md:4: the delimiter row has 1 cells, the header has 2"}},
		{"code fence", "```go", []string{"round.md:3: unknown construct: code fence (use ::: excerpt)"}},
		{"indented code", "    codice", []string{"round.md:3: unknown construct: indented code"}},
		{"tab-indented list", "\t- tab", []string{"round.md:3: unknown construct: indented code"}},
		{"raw HTML outside the round", "<img src=\"../segreto.png\">\n<a href='https://example.com'>x</a>\n<img src=nascosto.png>", []string{
			`round.md:3: src="../segreto.png" is not a file of the round directory or a data: image`,
			`round.md:4: href="https://example.com" is not a file of the round directory or a data: image`,
			`round.md:5: src="nascosto.png" is not a file of the round directory or a data: image`,
		}},
		{"quote", "> citazione", []string{"round.md:3: unknown construct: quote"}},
		{"star bullet", "* stella", []string{"round.md:3: unknown construct: list marker (use -)"}},
		{"plus bullet", "+ piu", []string{"round.md:3: unknown construct: list marker (use -)"}},
		{"thematic break", "---", []string{"round.md:3: unknown construct: thematic break"}},
		{"setext heading", "Titolo\n=====", []string{"round.md:4: unknown construct: setext heading"}},
		{"parenthesis list", "1) uno", []string{"round.md:3: unknown construct: list marker (use 1.)"}},
		{"space-indented list", "  - indentato", []string{"round.md:3: unknown construct: indented list"}},
		{"evidence without kind", "::: evidence\n- testo\n:::", []string{"round.md:4: an evidence item needs a kind: - [observed|unverified|outside] text"}},
		{"evidence kind", "::: evidence\n- [certo] testo\n:::", []string{`round.md:4: unknown evidence kind "certo" (use observed, unverified or outside)`}},
		{"evidence label", "::: evidence Prove\n- [observed] testo\n:::", []string{"round.md:3: ::: evidence takes no label (label each item)"}},
		{"steps paragraph", "::: steps\nprima\n:::", []string{"round.md:4: only list items belong in this block"}},
		{"excerpt spec", "::: excerpt main.go\n:::", []string{"round.md:3: ::: excerpt needs path:start-end"}},
		{"excerpt outside the root", "::: excerpt ../x.go:1-2\n:::", []string{"round.md:3: excerpt ../x.go:1-2: the path must be relative to the git root, without .."}},
		{"excerpt range", "::: excerpt main.go:5-2\n:::", []string{"round.md:3: excerpt main.go:5-2: the range starts at line 1 or later and ends at or after its start"}},
		{"excerpt unreadable", "::: excerpt manca.go:1-2\n:::", []string{"round.md:3: excerpt manca.go:1-2: no such file"}},
		{"deep heading", "#### x", []string{"round.md:3: unknown construct: heading level"}},
		{"nested list", "- a\n  - b", []string{"round.md:4: unknown construct: nested list"}},
		{"nested parenthesis list", "- a\n  1) b", []string{"round.md:4: unknown construct: nested list"}},
		{"nested block", "::: info\n::: info\n:::", []string{"round.md:4: nested ::: block"}},
		{"unclosed block", "::: info\ntesto", []string{"round.md:3: ::: info is not closed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, errs := render(t, c.body); !reflect.DeepEqual(errs, c.want) {
				t.Fatalf("errors\n%q\nwant\n%q", errs, c.want)
			}
		})
	}
}

func TestBlocksRenderEveryRichConstruct(t *testing.T) {
	html, errs := render(t, `Due sessioni scrivono
lo stesso file.
- primo punto
::: info Esempio
Dentro un blocco.
:::
::: steps Come procediamo
1. Prima capisci
  Leggi la spiegazione.
2. Poi scegli
:::
::: boundary Un solo riferimento
La spec resta l'unico documento durevole.
:::
::: excerpt main.go:3-4
La scrittura senza lock.
:::
<svg viewBox="0 0 10 10"><use href="#punto"/></svg>
<img src="q/diagramma.svg" alt="">
<img src='./q/a b.png'>
<svg><use href="q/diagramma.svg#icona"/></svg>
<img src="data:image/png;base64,AAAA">

## Confrontare
::: why Modulo scelto: confronto
Le alternative sugli stessi criteri.
:::
| Aspetto | File | Database |
| --- | :-: | --: |
| Contesa | nessuna | a\|b |
::: proposal
**Un file per sessione.** Nessuna dipendenza.
:::
::: evidence
- [observed] Oggi un solo file.
- [unverified Da misurare] La pulizia.
- [outside] Codemode.
:::`)
	if errs != nil {
		t.Fatal(errs)
	}
	for _, want := range []string{
		`<p>Due sessioni scrivono lo stesso file.</p>`,
		`<li>primo punto</li>`,
		`<div class="problem"><span class="content-label role-information">Esempio</span><p>Dentro un blocco.</p></div>`,
		`<ol class="simple-flow" aria-label="Come procediamo"><li><strong>Prima capisci</strong><p>Leggi la spiegazione.</p></li><li><strong>Poi scegli</strong></li></ol>`,
		`<div class="boundary"><span class="content-label role-neutral">Un solo riferimento</span><p>La spec resta l&#39;unico documento durevole.</p></div>`,
		`<figure class="excerpt"><figcaption><code>main.go:3-4</code></figcaption><pre><code><span class="line" data-line="3">riga <span class="hl-n">3</span> &lt;<span class="hl-n">3</span>&gt;</span><span class="line" data-line="4">riga <span class="hl-n">4</span> &lt;<span class="hl-n">4</span>&gt;</span></code></pre><p>La scrittura senza lock.</p></figure>`,
		`<svg viewBox="0 0 10 10"><use href="#punto"/></svg>` + "\n" + `<img src="q/diagramma.svg" alt="">`,
		`<div class="representation"><span class="content-label role-neutral">Modulo scelto: confronto</span><p>Le alternative sugli stessi criteri.</p></div>`,
		`<thead><tr><th scope="col">Aspetto</th><th scope="col" class="align-center">File</th><th scope="col" class="align-right">Database</th></tr></thead>`,
		`<tr><th scope="row">Contesa</th><td data-label="File" class="align-center">nessuna</td><td data-label="Database" class="align-right">a|b</td></tr>`,
		`<div class="recommendation"><span class="content-label role-analysis">Proposta · non approvata</span><p><strong>Un file per sessione.</strong> Nessuna dipendenza.</p></div>`,
		`<div class="evidence"><div class="evidence-item"><span class="content-label role-information">Osservato</span><p>Oggi un solo file.</p></div><div class="evidence-item"><span class="content-label role-analysis">Da misurare</span><p>La pulizia.</p></div><div class="evidence-item"><span class="content-label role-neutral">Fuori da questa scelta</span><p>Codemode.</p></div></div>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML lacks %s", want)
		}
	}
}

func TestExcerptsShareTheRoundBudget(t *testing.T) {
	one, errs := render(t, "::: excerpt main.go:1-3\n:::")
	if errs != nil {
		t.Fatal(errs)
	}
	rendered := len(one[strings.Index(one, `<span class="line"`):strings.Index(one, "</code></pre>")])
	phase, errs := question(t, "::: excerpt main.go:1-3\n:::\n::: excerpt main.go:1-3\n:::")
	if errs != nil {
		t.Fatal(errs)
	}
	phase.Budget = 2*rendered - 1
	want := []string{fmt.Sprintf("round.md:5: excerpt main.go:1-3: the round exceeds the %d byte bound", MaxBytes)}
	if errs := RenderPhase(&phase, fakeRepository, nil); !reflect.DeepEqual(errs, want) {
		t.Fatalf("errors %q, want %q", errs, want)
	}
}
