package main

import (
	"strings"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
)

const (
	acceptedText = "Ricevuto da lavagna · non ancora consegnato all’agente"
	returnedText = "Consegnato al terminale"
	formShown    = `!document.querySelector('#feedback-form').hidden`
)

const richRound = `# Capire
## Il problema
Lo stato vive in un solo file.
::: info Esempio
Due sessioni scrivono lo stesso file.
:::

# Confrontare
- Un file per sessione: nessuna contesa.
- Un database: una dipendenza in più.

# Decidere
## Dove salviamo lo stato? {id="storage"}
- [file] Un file per sessione
  Nessuna contesa, più file da pulire.
- [db] Un database locale
`

func openRound(t *testing.T, src string, width int) (*call, *cdptest.Page) {
	t.Helper()
	c := startRound(t, env(t, "LAVAGNA_SESSION=page"), src)
	p := cdptest.Start(t).Open(c.url, width, 900)
	p.WaitFor(formShown)
	return c, p
}

func text(p *cdptest.Page, selector string) string {
	var s string
	p.MustEval(`document.querySelector('`+selector+`').textContent`, &s)
	return s
}

func feedbackTail(choice string, comments ...string) string {
	var parts []string
	for _, c := range comments {
		parts = append(parts, `{"anchor":null,"text":"`+c+`"}`)
	}
	return `","choices":{"storage":"` + choice + `"},"comments":[` + strings.Join(parts, ",") + `],"images":[]}`
}

func TestPageSendsOneBatchPerSend(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	p.Click(`input[value="db"]`)
	p.Type("#comment-text", "Primo commento")
	p.Click("#add-comment")
	p.Type("#comment-text", "Ancora in bozza")

	p.Hold("*/send")
	p.ClickTimes("#send-feedback", 3)
	time.Sleep(300 * time.Millisecond)
	held := p.Held()
	if len(held) != 1 {
		t.Fatalf("the page issued %d send requests, want 1", len(held))
	}
	p.Click(`input[value="file"]`)
	p.Type("#comment-text", "scritto durante l'invio")
	var frozen struct {
		File   bool
		Editor string
	}
	p.MustEval(`({File: document.querySelector('input[value="file"]').checked, Editor: document.querySelector('#comment-text').value})`, &frozen)
	if frozen.File || frozen.Editor != "Ancora in bozza" {
		t.Errorf("the draft changed while the batch was in flight: %+v", frozen)
	}
	p.Release(held[0])

	lines, code := c.finish()
	if code != 0 || len(lines) != 1 || !strings.HasPrefix(lines[0], `{"lavagna":"feedback","round":"r1","submission":"s-`) ||
		!strings.HasSuffix(lines[0], feedbackTail("db", "Primo commento", "Ancora in bozza")) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + returnedText + `'`)
	time.Sleep(200 * time.Millisecond)
	var after struct {
		Disabled bool
		Drafts   int
	}
	p.MustEval(`({Disabled: document.querySelector('#send-feedback').disabled, Drafts: Object.keys(localStorage).filter(k => k.startsWith('lavagna:draft:')).length})`, &after)
	if !after.Disabled || after.Drafts != 0 {
		t.Errorf("after the batch was returned: %+v, want send disabled and no stored draft", after)
	}
}

func TestPageKeepsTheDraftWithoutDuplicates(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	p.Type("#comment-text", "Da correggere")
	p.Click("#add-comment")
	p.Click(".note-actions button")
	p.Click("#comment-text")
	p.SelectAll()
	p.Insert("Corretto")
	p.Click("#add-comment")
	p.Type("#comment-text", "Non ancora aggiunto")
	p.Click(`input[value="file"]`)

	p.Reload()
	p.WaitFor(formShown)
	if got := text(p, "#comments"); !strings.Contains(got, "Corretto") || strings.Contains(got, "Da correggere") {
		t.Fatalf("restored comments %q", got)
	}
	var restored struct {
		Notes  int
		Editor string
		Choice bool
	}
	p.MustEval(`({Notes: document.querySelectorAll('#comments li').length, Editor: document.querySelector('#comment-text').value, Choice: document.querySelector('input[value="file"]').checked})`, &restored)
	if restored.Notes != 1 || restored.Editor != "Non ancora aggiunto" || !restored.Choice {
		t.Fatalf("restored draft %+v", restored)
	}

	p.Type("#comment-text", strings.Repeat("x", 32768))
	var over struct {
		Over     bool
		Disabled bool
		Length   int
	}
	p.MustEval(`({Over: document.querySelector('#comment-counter').classList.contains('over'), Disabled: document.querySelector('#send-feedback').disabled, Length: document.querySelector('#comment-text').value.length})`, &over)
	if !over.Over || !over.Disabled || over.Length != len("Non ancora aggiunto")+32768 {
		t.Fatalf("over the bound: %+v", over)
	}
	p.SelectAll()
	p.Insert("Non ancora aggiunto")

	p.Click("#send-feedback")
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 || !strings.HasSuffix(lines[0], feedbackTail("file", "Corretto", "Non ancora aggiunto")) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
}

const pageFacts = `(async () => {
  await document.fonts.ready;
  const rgb = c => c.match(/[\d.]+/g).map(Number);
  const lum = c => { const [r, g, b] = rgb(c).map(v => { v /= 255; return v <= .03928 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4; }); return .2126 * r + .7152 * g + .0722 * b; };
  const background = el => { for (; el; el = el.parentElement) { const c = getComputedStyle(el).backgroundColor; if (rgb(c)[3] !== 0) return c; } return 'rgb(255, 255, 255)'; };
  let worst = 99, where = '';
  for (const el of document.querySelectorAll('body *')) {
    if (!el.checkVisibility() || el.closest(':disabled') || ![...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) continue;
    const a = lum(getComputedStyle(el).color), b = lum(background(el));
    const ratio = (Math.max(a, b) + .05) / (Math.min(a, b) + .05);
    if (ratio < worst) { worst = ratio; where = el.tagName + '.' + el.className + ' ' + el.textContent.trim().slice(0, 30); }
  }
  const visible = s => [...document.querySelectorAll(s)].filter(e => e.checkVisibility()).length;
  return {
    Worst: worst, Where: where,
    Font: document.fonts.check('16px "Atkinson Hyperlegible Next"') && [...document.fonts].some(f => f.family.includes('Atkinson') && f.status === 'loaded'),
    Indexes: [...document.querySelectorAll('.chapter-index')].map(e => e.textContent + ' ' + getComputedStyle(e).backgroundColor),
    Labels: [...document.querySelectorAll('.chapter-name')].map(e => e.textContent),
    Editors: visible('textarea'), Sends: visible('#send-feedback'),
    Overflow: document.documentElement.scrollWidth > innerWidth,
  };
})()`

func TestPagePresentation(t *testing.T) {
	for name, width := range map[string]int{"desktop": 1280, "narrow": 390} {
		t.Run(name, func(t *testing.T) {
			_, p := openRound(t, richRound, width)
			var f struct {
				Worst           float64
				Where           string
				Font, Overflow  bool
				Indexes, Labels []string
				Editors, Sends  int
			}
			p.MustEval(pageFacts, &f)
			if f.Worst < 4.5 {
				t.Errorf("text contrast %.2f:1 at %s, want at least 4.5:1 (WCAG AA; inactive controls exempt)", f.Worst, f.Where)
			}
			if !f.Font {
				t.Error("Atkinson Hyperlegible Next did not load")
			}
			wantIndexes := []string{"01 rgb(36, 90, 150)", "02 rgb(133, 81, 11)", "03 rgb(18, 99, 91)"}
			if strings.Join(f.Indexes, "|") != strings.Join(wantIndexes, "|") || strings.Join(f.Labels, "|") != "Capire|Confrontare|Decidere" {
				t.Errorf("chapters %v %v", f.Indexes, f.Labels)
			}
			if f.Editors != 1 || f.Sends != 1 {
				t.Errorf("%d editors and %d send buttons, want one feedback area", f.Editors, f.Sends)
			}
			if f.Overflow {
				t.Error("the page overflows horizontally")
			}
		})
	}
}

func TestPageShowsTheClosingPage(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=closing")
	c := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(c.url, 1280, 900)
	p.WaitFor(formShown)
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	if _, code := c.finish(); code != 0 {
		t.Fatalf("round exit %d", code)
	}
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + returnedText + `' || document.querySelector('#delivery').textContent === '` + acceptedText + `'`)

	lines, code := run(t, environ, "", "close")
	if code != 0 || last(lines) != `{"lavagna":"closed","page":"shown"}` {
		t.Fatalf("close: exit %d, output %q", code, lines)
	}
	p.WaitFor(`!document.querySelector('#closed').hidden && document.querySelector('#feedback-form').hidden`)
	if got := text(p, "#closed h1"); got != "Frontiera chiusa, torna al terminale" {
		t.Fatalf("closing page %q", got)
	}
}
