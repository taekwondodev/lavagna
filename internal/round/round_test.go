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

func parse(src string) (Round, []string) {
	return Parse(Input{Source: []byte(src), Files: map[string]bool{"diagramma.svg": true, "a b.png": true}, Budget: MaxBytes, Excerpt: fakeRepository})
}

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
	r, errs := parse(src)
	if errs != nil {
		t.Fatal(errs)
	}
	html := r.Content + r.Decide
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
		if !strings.Contains(html, want) {
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
		{"table without delimiter", "# Capire\n| a | b |", []string{"round.md:2: a table needs a header row and a | --- | delimiter row"}},
		{"table after paragraph", "# Capire\ntesto\n| a | b |", []string{"round.md:3: a table needs a header row and a | --- | delimiter row"}},
		{"table without outer pipes", "# Capire\na | b\n--- | ---", []string{"round.md:3: unknown construct: table row without outer |"}},
		{"table row without closing pipe", "# Capire\n| a | b |\n| --- | --- |\n| x | y", []string{"round.md:4: a table row starts and ends with |"}},
		{"table row width", "# Capire\n| a | b |\n| --- | --- |\n| x |", []string{"round.md:4: the row has 1 cells, the header has 2"}},
		{"table delimiter width", "# Capire\n| a | b |\n| --- |", []string{"round.md:3: the delimiter row has 1 cells, the header has 2"}},
		{"code fence", "# Capire\n```go", []string{"round.md:2: unknown construct: code fence (use ::: excerpt)"}},
		{"indented code", "# Capire\n    codice", []string{"round.md:2: unknown construct: indented code"}},
		{"tab-indented list", "# Capire\n\t- tab", []string{"round.md:2: unknown construct: indented code"}},
		{"raw HTML under Decidere", "# Decidere\n<svg></svg>", []string{"round.md:2: raw HTML belongs in # Capire or # Confrontare"}},
		{"raw HTML outside the round", "# Capire\n<img src=\"../segreto.png\">\n<a href='https://example.com'>x</a>\n<img src=nascosto.png>", []string{
			`round.md:2: src="../segreto.png" is not a file of the round directory or a data: image`,
			`round.md:3: href="https://example.com" is not a file of the round directory or a data: image`,
			`round.md:4: src="nascosto.png" is not a file of the round directory or a data: image`,
		}},
		{"quote", "# Capire\n> citazione", []string{"round.md:2: unknown construct: quote"}},
		{"star bullet", "# Capire\n* stella", []string{"round.md:2: unknown construct: list marker (use -)"}},
		{"plus bullet", "# Capire\n+ piu", []string{"round.md:2: unknown construct: list marker (use -)"}},
		{"thematic break", "# Capire\n---", []string{"round.md:2: unknown construct: thematic break"}},
		{"setext heading", "# Capire\nTitolo\n=====", []string{"round.md:3: unknown construct: setext heading"}},
		{"parenthesis list", "# Capire\n1) uno", []string{"round.md:2: unknown construct: list marker (use 1.)"}},
		{"space-indented list", "# Capire\n  - indentato", []string{"round.md:2: unknown construct: indented list"}},
		{"list inside a question", "# Decidere\n## Q {id=\"q\"}\n1. passo\n- [a] A\n- [b] B", []string{`round.md:3: a list inside question "q" (put it before the first question)`}},
		{"anchor under Decidere", "# Decidere\nPunto {ref=\"a\"}", []string{`round.md:2: {ref="..."} anchors belong in # Capire or # Confrontare`}},
		{"anchor inside text", "# Capire\nPunto {ref=\"a\"} e altro", []string{"round.md:2: unknown construct: attribute"}},
		{"repeated anchor", "# Capire\nUno {ref=\"a\"}\n\n<div data-ref=\"a\"></div>", []string{`round.md:4: anchor "a" is repeated`}},
		{"empty anchor", "# Capire\n## Titolo {ref=\" \"}", []string{"round.md:2: empty anchor"}},
		{"long anchor", "# Capire\nUno {ref=\"" + strings.Repeat("à", 81) + "\"}", []string{`round.md:2: anchor "` + strings.Repeat("à", 81) + `" is longer than 80 characters`}},
		{"evidence without kind", "# Capire\n::: evidence\n- testo\n:::", []string{"round.md:3: an evidence item needs a kind: - [observed|unverified|outside] text"}},
		{"evidence kind", "# Capire\n::: evidence\n- [certo] testo\n:::", []string{`round.md:3: unknown evidence kind "certo" (use observed, unverified or outside)`}},
		{"evidence label", "# Capire\n::: evidence Prove\n- [observed] testo\n:::", []string{"round.md:2: ::: evidence takes no label (label each item)"}},
		{"steps paragraph", "# Capire\n::: steps\nprima\n:::", []string{"round.md:3: only list items belong in this block"}},
		{"excerpt spec", "# Capire\n::: excerpt main.go\n:::", []string{"round.md:2: ::: excerpt needs path:start-end"}},
		{"excerpt outside the root", "# Capire\n::: excerpt ../x.go:1-2\n:::", []string{"round.md:2: excerpt ../x.go:1-2: the path must be relative to the git root, without .."}},
		{"excerpt range", "# Capire\n::: excerpt main.go:5-2\n:::", []string{"round.md:2: excerpt main.go:5-2: the range starts at line 1 or later and ends at or after its start"}},
		{"excerpt unreadable", "# Capire\n::: excerpt manca.go:1-2\n:::", []string{"round.md:2: excerpt manca.go:1-2: no such file"}},
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
			if _, errs := parse(c.src); !reflect.DeepEqual(errs, c.want) {
				t.Fatalf("errors\n%q\nwant\n%q", errs, c.want)
			}
		})
	}
}

func TestParseRefusesOversizedRound(t *testing.T) {
	_, errs := parse("# Capire\n" + strings.Repeat("a", MaxBytes))
	if len(errs) != 1 || !strings.Contains(errs[0], "exceeds") {
		t.Fatalf("got %q, want one bound error", errs)
	}
}

func TestParseAcceptsEveryRichConstruct(t *testing.T) {
	src := `# Capire
## Il problema {ref="Problema"}
Due sessioni scrivono
lo stesso file. {ref="Due scritture"}
- primo punto {ref="Punto uno"}
::: info Esempio {ref="Esempio"}
Dentro un blocco.
:::
::: steps Come procediamo
1. Prima capisci
  Leggi la spiegazione. {ref="Passo uno"}
2. Poi scegli
:::
::: boundary Un solo riferimento
La spec resta l'unico documento durevole.
:::
::: excerpt main.go:3-4 {ref="Codice"}
La scrittura senza lock.
:::
<svg viewBox="0 0 10 10" data-ref="Diagramma &amp; flusso"><use href="#punto"/></svg>
<img src="diagramma.svg" alt="">
<img src='./a b.png' data-ref='Immagine'>
<svg><use href="diagramma.svg#icona"/></svg>
<img src="data:image/png;base64,AAAA">

# Confrontare
::: why Modulo scelto: confronto
Le alternative sugli stessi criteri.
:::
| Aspetto | File | Database |
| --- | :-: | --: |
| Contesa | nessuna {ref="File · contesa"} | a\|b |
::: proposal
**Un file per sessione.** Nessuna dipendenza.
:::
::: evidence {ref="Prove"}
- [observed] Oggi un solo file.
- [unverified Da misurare] La pulizia. {ref="Pulizia"}
- [outside] Codemode.
:::

# Decidere
::: proposal Consiglio
Scegli il file.
:::
| Opzione | Costo |
| --- | --- |
| file | basso |
## Dove salviamo lo stato? {id="storage"}
- [file] Un file per sessione
- [db] Un database locale
`
	r, errs := parse(src)
	if errs != nil {
		t.Fatal(errs)
	}
	wantAnchors := []string{"Problema", "Due scritture", "Punto uno", "Esempio", "Passo uno", "Codice", "Diagramma & flusso", "Immagine", "File · contesa", "Prove", "Pulizia"}
	if !reflect.DeepEqual(r.Anchors, wantAnchors) {
		t.Errorf("anchors %q, want %q", r.Anchors, wantAnchors)
	}
	if !reflect.DeepEqual(r.Questions, []Question{{"storage", []string{"file", "db"}}}) {
		t.Errorf("questions %v", r.Questions)
	}
	for _, want := range []string{
		`<section class="chapter role-information" id="capire"`,
		`<h2 data-ref="Problema">Il problema</h2>`,
		`<p data-ref="Due scritture">Due sessioni scrivono lo stesso file.</p>`,
		`<li data-ref="Punto uno">primo punto</li>`,
		`<div class="problem" data-ref="Esempio"><span class="content-label role-information">Esempio</span><p>Dentro un blocco.</p></div>`,
		`<ol class="simple-flow" aria-label="Come procediamo"><li data-ref="Passo uno"><strong>Prima capisci</strong><p>Leggi la spiegazione.</p></li><li><strong>Poi scegli</strong></li></ol>`,
		`<div class="boundary"><span class="content-label role-neutral">Un solo riferimento</span><p>La spec resta l&#39;unico documento durevole.</p></div>`,
		`<figure class="excerpt" data-ref="Codice"><figcaption><code>main.go:3-4</code></figcaption><pre><code><span class="line" data-line="3">riga 3 &lt;3&gt;</span><span class="line" data-line="4">riga 4 &lt;4&gt;</span></code></pre><p>La scrittura senza lock.</p></figure>`,
		`<svg viewBox="0 0 10 10" data-ref="Diagramma &amp; flusso"><use href="#punto"/></svg>` + "\n" + `<img src="diagramma.svg" alt="">`,
		`<div class="representation"><span class="content-label role-neutral">Modulo scelto: confronto</span><p>Le alternative sugli stessi criteri.</p></div>`,
		`<thead><tr><th scope="col">Aspetto</th><th scope="col" class="align-center">File</th><th scope="col" class="align-right">Database</th></tr></thead>`,
		`<tr><th scope="row">Contesa</th><td data-label="File" class="align-center" data-ref="File · contesa">nessuna</td><td data-label="Database" class="align-right">a|b</td></tr>`,
		`<div class="recommendation"><span class="content-label role-analysis">Proposta · non approvata</span><p><strong>Un file per sessione.</strong> Nessuna dipendenza.</p></div>`,
		`<div class="evidence" data-ref="Prove"><div class="evidence-item"><span class="content-label role-information">Osservato</span><p>Oggi un solo file.</p></div><div class="evidence-item" data-ref="Pulizia"><span class="content-label role-analysis">Da misurare</span><p>La pulizia.</p></div><div class="evidence-item"><span class="content-label role-neutral">Fuori da questa scelta</span><p>Codemode.</p></div></div>`,
	} {
		if !strings.Contains(r.Content, want) {
			t.Errorf("content lacks %s", want)
		}
	}
	for _, want := range []string{
		`<section class="chapter role-action" id="decidere"`,
		`<div class="recommendation"><span class="content-label role-analysis">Consiglio</span><p>Scegli il file.</p></div>`,
		`<tr><th scope="row">file</th><td data-label="Costo">basso</td></tr>`,
		`value="db" data-question="storage"`,
	} {
		if !strings.Contains(r.Decide, want) {
			t.Errorf("decide lacks %s", want)
		}
	}
	if strings.Contains(r.Content, "decidere") || strings.Contains(r.Decide, "capire") {
		t.Error("Decidere renders in the page and the other chapters in the frame")
	}
}

func TestParseBoundsExcerptsByTheRoundBudget(t *testing.T) {
	src := "# Capire\n::: excerpt main.go:1-3\n:::\n::: excerpt main.go:1-3\n:::"
	one, errs := Parse(Input{Source: []byte("# Capire\n::: excerpt main.go:1-3\n:::"), Budget: MaxBytes, Excerpt: fakeRepository})
	if errs != nil {
		t.Fatal(errs)
	}
	rendered := len(one.Content[strings.Index(one.Content, `<span class="line"`):strings.Index(one.Content, "</code></pre>")])
	_, errs = Parse(Input{Source: []byte(src), Budget: 2*rendered - 1, Excerpt: fakeRepository})
	want := []string{fmt.Sprintf("round.md:4: excerpt main.go:1-3: the round exceeds the %d byte bound", MaxBytes)}
	if !reflect.DeepEqual(errs, want) {
		t.Fatalf("errors %q, want %q", errs, want)
	}
}
