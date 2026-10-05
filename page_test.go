package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
)

const (
	acceptedText = "Ricevuto da lavagna · non ancora consegnato all’agente"
	returnedText = "Consegnato al terminale"
	blindText    = returnedText + " · stato in tempo reale non disponibile"
	formShown    = `!document.querySelector('#feedback-form').hidden`
)

func settled(t *testing.T, p *cdptest.Page) {
	t.Helper()
	p.WaitFor(formShown)
	var framed bool
	p.MustEval(`Boolean(document.querySelector('#content'))`, &framed)
	if !framed {
		return
	}
	f := p.Frame("#content")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var inner, outer int
		f.WaitFor(`document.readyState === 'complete' && document.fonts.status === 'loaded'`)
		f.MustEval(`document.body.scrollHeight`, &inner)
		p.MustEval(`document.querySelector('#content').offsetHeight`, &outer)
		if inner == outer && inner > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the content frame is %dpx tall in the page, its document %dpx", outer, inner)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

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
	settled(t, p)
	return c, p
}

func text(p *cdptest.Page, selector string) string {
	var s string
	p.MustEval(`document.querySelector('`+selector+`').textContent`, &s)
	return s
}

func heldOnce(t *testing.T, p *cdptest.Page, what string) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(p.Held()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	held := p.Held()
	if len(held) != 1 {
		t.Fatalf("%s issued %d send requests, want 1", what, len(held))
	}
	return held
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
	held := heldOnce(t, p, "three clicks")
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
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + blindText + `'`)
	time.Sleep(200 * time.Millisecond)
	var disabled bool
	p.MustEval(`document.querySelector('#send-feedback').disabled`, &disabled)
	if !disabled {
		t.Error("send is enabled after the batch was returned")
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
	settled(t, p)
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

	var counterHidden bool
	p.MustEval(`document.querySelector('#comment-counter').hidden`, &counterHidden)
	if !counterHidden {
		t.Error("the counter shows far from the limit")
	}
	p.Type("#comment-text", strings.Repeat("x ", 16384))
	var over struct {
		Over     bool
		Disabled bool
		Length   int
	}
	p.MustEval(`({Over: document.querySelector('#comment-counter').classList.contains('over') && !document.querySelector('#comment-counter').hidden, Disabled: document.querySelector('#send-feedback').disabled, Length: document.querySelector('#comment-text').value.length})`, &over)
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

type facts struct {
	Worst           float64
	Where           string
	Font, Overflow  bool
	Indexes, Labels []string
	Editors, Sends  int
}

func TestPagePresentation(t *testing.T) {
	for name, width := range map[string]int{"desktop": 1280, "narrow": 390} {
		t.Run(name, func(t *testing.T) {
			_, p := openRound(t, richRound, width)
			var shell, content facts
			p.MustEval(pageFacts, &shell)
			p.Frame("#content").MustEval(pageFacts, &content)
			for where, f := range map[string]facts{"page": shell, "content frame": content} {
				if f.Worst < 4.5 {
					t.Errorf("%s: text contrast %.2f:1 at %s, want at least 4.5:1 (WCAG AA; inactive controls exempt)", where, f.Worst, f.Where)
				}
				if !f.Font {
					t.Errorf("%s: Atkinson Hyperlegible Next did not load", where)
				}
				if f.Overflow {
					t.Errorf("%s overflows horizontally", where)
				}
			}
			indexes := append(content.Indexes, shell.Indexes...)
			labels := append(content.Labels, shell.Labels...)
			wantIndexes := []string{"01 rgb(36, 90, 150)", "02 rgb(133, 81, 11)", "03 rgb(18, 99, 91)"}
			if strings.Join(indexes, "|") != strings.Join(wantIndexes, "|") || strings.Join(labels, "|") != "Capire|Confrontare|Decidere" {
				t.Errorf("chapters %v %v", indexes, labels)
			}
			if shell.Editors != 1 || shell.Sends != 1 || content.Editors != 0 || content.Sends != 0 {
				t.Errorf("%d+%d editors and %d+%d send buttons, want one feedback area in the page", shell.Editors, content.Editors, shell.Sends, content.Sends)
			}
		})
	}
}

func TestPageShowsTheClosingPage(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=closing")
	c := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(c.url, 1280, 900)
	settled(t, p)
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	if _, code := c.finish(); code != 0 {
		t.Fatalf("round exit %d", code)
	}
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + blindText + `' || document.querySelector('#delivery').textContent === '` + acceptedText + `'`)

	lines, code := run(t, environ, "", "close")
	if code != 0 || last(lines) != `{"lavagna":"closed","page":"shown"}` {
		t.Fatalf("close: exit %d, output %q", code, lines)
	}
	p.WaitFor(`!document.querySelector('#closed').hidden && document.querySelector('#feedback-form').hidden`)
	if got := text(p, "#closed h1"); got != "Frontiera chiusa, torna al terminale" {
		t.Fatalf("closing page %q", got)
	}
	if got := text(p, "#closed"); !strings.Contains(got, "Puoi chiudere questa scheda.") || text(p, "#turn") != "Concluso" {
		t.Fatalf("closing page %q, turn %q", got, text(p, "#turn"))
	}
	var left struct{ Worker, Caches, Records int }
	for range 40 {
		p.MustEval(`(async () => ({Worker: (await navigator.serviceWorker.getRegistrations()).length, Caches: (await caches.keys()).length, Records: Object.keys(localStorage).filter(k => k.startsWith('lavagna:')).length}))()`, &left)
		if left.Worker+left.Caches+left.Records == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if left.Worker+left.Caches+left.Records != 0 {
		t.Fatalf("the closed conversation left page state behind: %+v", left)
	}
}

func TestPageGuidesTheTurn(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	var start struct{ Turn, Hint string }
	p.MustEval(`({Turn: document.querySelector('#turn').textContent, Hint: document.querySelector('#send-hint').textContent})`, &start)
	if start.Turn != "Tocca a te" || start.Hint != "Scegli un’opzione, scrivi un commento o allega uno screenshot per inviare." {
		t.Fatalf("before any feedback: %+v", start)
	}

	p.Click(`input[value="db"]`)
	p.Type("#comment-text", "Serve un esempio")
	var review struct {
		Items   []string
		Counter bool
	}
	p.MustEval(`({Items: [...document.querySelectorAll('#review-list li')].map(li => li.innerText.replace(/\s+/g, ' ').trim()), Counter: !document.querySelector('#comment-counter').hidden})`, &review)
	if strings.Join(review.Items, "|") != "Dove salviamo lo stato? Un database locale|Serve un esempio nell’editor" || review.Counter {
		t.Fatalf("review before sending: %+v", review)
	}

	p.Hold("*/send")
	p.Press("Enter", 13, 4)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Invio in corso…'`)
	held := heldOnce(t, p, "⌘+Invio")
	p.Release(held[0])
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 || !strings.HasSuffix(lines[0], feedbackTail("db", "Serve un esempio")) {
		t.Fatalf("⌘+Invio: exit %d, output %q", code, lines)
	}
	p.WaitFor(`document.querySelector('#turn').textContent === 'In attesa del prossimo round'`)
	var after struct {
		Eyebrow, Sent string
		Draft         bool
		Actions       int
	}
	p.MustEval(`({Eyebrow: document.querySelector('#feedback-eyebrow').textContent, Sent: document.querySelector('#sent-area').innerText, Draft: !document.querySelector('#draft-area').hidden, Actions: [...document.querySelectorAll('.note-actions button')].filter(b => b.checkVisibility()).length})`, &after)
	if after.Eyebrow != "Feedback inviato" || !strings.Contains(after.Sent, "Un database locale") || !strings.Contains(after.Sent, "Serve un esempio") || after.Draft || after.Actions != 0 {
		t.Fatalf("after sending: %+v", after)
	}
}

func TestPageRouteFollowsTheReading(t *testing.T) {
	_, p := openRound(t, richRound, 1280)
	current := `document.querySelector('#route a[aria-current="location"]')?.hash`
	p.WaitFor(current + ` === '#capire'`)
	seen := map[string]bool{}
	for range 40 {
		p.Wheel(150)
		time.Sleep(30 * time.Millisecond)
		var hash string
		p.MustEval(current, &hash)
		seen[hash] = true
	}
	if !seen["#confrontare"] || !seen["#decidere"] {
		t.Fatalf("chapters highlighted while scrolling: %v", seen)
	}
}

func TestPageMobileBarLeadsToTheFeedback(t *testing.T) {
	c, p := openRound(t, richRound, 390)
	barShows := func(summary, label string) {
		t.Helper()
		p.MustEval(`window.scrollTo({top: 0, behavior: 'instant'})`, nil)
		p.WaitFor(`scrollY === 0 && !document.querySelector('#mobile-bar').hidden && document.querySelector('#mobile-summary').textContent === '` + summary + `' && document.querySelector('#to-feedback').textContent === '` + label + `'`)
	}
	follow := func(focus string) {
		t.Helper()
		p.Click("#to-feedback")
		p.WaitFor(`document.querySelector('#mobile-bar').hidden`)
		var focused string
		p.MustEval(`document.activeElement.id`, &focused)
		if focused != focus {
			t.Fatalf("focus on %q after following the bar, want %q", focused, focus)
		}
	}

	barShows("Tocca a te", "Rivedi e invia")
	p.Click(`input[value="db"]`)
	barShows("1 risposta in bozza", "Rivedi e invia")
	follow("send-feedback")
	var inView bool
	p.MustEval(`(() => { const r = document.querySelector('#send-feedback').getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight; })()`, &inView)
	if !inView {
		t.Fatal("the send button is not in view after following the bar")
	}

	p.Click("#send-feedback")
	if _, code := c.finish(); code != 0 {
		t.Fatalf("round exit %d", code)
	}
	barShows("In attesa del prossimo round", "Vedi riepilogo")
	follow("sent-area")
}

func TestPageCounterAppearsNearTheLimit(t *testing.T) {
	_, p := openRound(t, richRound, 1280)
	counterShown := `!document.querySelector('#comment-counter').hidden`
	p.Type("#comment-text", strings.Repeat("x ", 12287)+"x")
	var shown bool
	p.MustEval(counterShown, &shown)
	if shown {
		t.Fatal("the counter shows below 75% of the limit")
	}
	p.Insert("x")
	p.WaitFor(counterShown)
}

func detach(t *testing.T, c *call) {
	t.Helper()
	c.cmd.Process.Kill()
	c.cmd.Wait()
	ln, err := net.Listen("tcp", strings.TrimPrefix(c.origin, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	foreign := &http.Server{Handler: http.NotFoundHandler()}
	go foreign.Serve(ln)
	t.Cleanup(func() { foreign.Close() })
}

func TestPageDetachedTabKeepsTheDraft(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	p.Type("#comment-text", "Commento salvato")
	p.Click("#add-comment")
	p.Type("#comment-text", "Bozza da non perdere")
	p.Click(".note-actions button")
	detach(t, c)

	p.WaitFor(`document.querySelector('#turn').textContent === 'Scheda non collegata'`)
	var tab struct {
		Delivery, Eyebrow, Hint string
		SendDisabled            bool
	}
	p.MustEval(`({Delivery: document.querySelector('#delivery').textContent, Eyebrow: document.querySelector('#feedback-eyebrow').textContent, Hint: document.querySelector('#send-hint').textContent, SendDisabled: document.querySelector('#send-feedback').disabled})`, &tab)
	if tab.Delivery != "Questa scheda non è più collegata alla conversazione" || tab.Eyebrow != "Bozza conservata · non inviata" || tab.Hint != "" || !tab.SendDisabled {
		t.Fatalf("detached tab: %+v", tab)
	}
	p.Click("#cancel-edit")
	var editor string
	p.MustEval(`document.querySelector('#comment-text').value`, &editor)
	if editor != "Bozza da non perdere" {
		t.Fatalf("editor after cancelling the edit: %q", editor)
	}
}

func TestPageDetachedTabAfterSendingStaysSent(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	if _, code := c.finish(); code != 0 {
		t.Fatalf("round exit %d", code)
	}
	p.WaitFor(`document.querySelector('#turn').textContent === 'In attesa del prossimo round'`)
	detach(t, c)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Scheda non collegata'`)
	var tab struct{ Eyebrow, Delivery, Sent string }
	p.MustEval(`({Eyebrow: document.querySelector('#feedback-eyebrow').textContent, Delivery: document.querySelector('#delivery').textContent, Sent: document.querySelector('#sent-area').innerText})`, &tab)
	if tab.Eyebrow != "Feedback inviato" || tab.Delivery != "Questa scheda non è più collegata alla conversazione" || !strings.Contains(tab.Sent, "Un database locale") {
		t.Fatalf("detached after sending: %+v", tab)
	}
}

const prototypeRound = `# Capire
## Il contatore {ref="Titolo"}
Prova il prototipo qui sotto.

# Confrontare
<div class="ui-prototype" data-ref="UI · contatore"><button id="piu" type="button">Aggiungi</button> <output id="conteggio">0</output></div>

# Decidere
## Va bene? {id="ok"}
- [si] Sì
- [no] No
`

const prototypeScript = `document.querySelector('#piu').addEventListener('click', () => {
  const out = document.querySelector('#conteggio');
  out.textContent = String(Number(out.textContent) + 1);
});
`

func TestPageAnchorsWithoutActivatingThePrototype(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"round.md": prototypeRound, "prototipo.js": prototypeScript})
	c := startDir(t, env(t, "LAVAGNA_SESSION=anchor"), dir)
	p := cdptest.Start(t).Open(c.url, 1280, 900)
	settled(t, p)
	f := p.Frame("#content")
	count := func() string {
		var s string
		f.MustEval(`document.querySelector('#conteggio').textContent`, &s)
		return s
	}
	explore := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for count() != want {
			if time.Now().After(deadline) {
				t.Fatalf("the prototype control stays inactive after picking: count %s, want %s", count(), want)
			}
			f.Click("#piu")
			time.Sleep(100 * time.Millisecond)
		}
	}
	anchor := `document.querySelector('#anchor-label').textContent`
	picking := `document.querySelector('#pick-anchor').getAttribute('aria-pressed') === 'true'`

	p.Type("#comment-text", "Il pulsante si vede poco")
	p.Click("#pick-anchor")
	p.WaitFor(picking + ` && !document.querySelector('#picker-hint').hidden`)
	f.Click("#piu")
	p.WaitFor(anchor + ` === 'Riferimento: UI · contatore' && !(` + picking + `)`)
	if got := count(); got != "0" {
		t.Fatalf("picking the anchor activated the prototype control: count %s", got)
	}
	f.Click("#piu")
	f.WaitFor(`document.querySelector('[data-ref="UI · contatore"]').classList.contains('referenced')`)
	if got := count(); got != "0" {
		t.Fatalf("the second click of a double click activated the prototype control: count %s", got)
	}

	explore("1")
	var draft struct {
		Editor, Anchor string
	}
	p.MustEval(`({Editor: document.querySelector('#comment-text').value, Anchor: `+anchor+`})`, &draft)
	if draft.Editor != "Il pulsante si vede poco" || draft.Anchor != "Riferimento: UI · contatore" {
		t.Fatalf("exploring again changed the draft: %+v", draft)
	}

	p.Click("#pick-anchor")
	p.WaitFor(picking)
	p.MustEval(`window.postMessage({lavagna: 'anchor', ref: 'Titolo'}, '*')`, nil)
	f.MustEval(`parent.postMessage({lavagna: 'anchor', ref: 'Inventato'}, '*'); parent.postMessage({lavagna: 'cancel'}, '*')`, nil)
	p.WaitFor(`!(` + picking + `)`)
	var forged string
	p.MustEval(anchor, &forged)
	if forged != "Riferimento: UI · contatore" {
		t.Fatalf("a forged anchor message changed the anchor: %q", forged)
	}
	p.Click("#pick-anchor")
	p.WaitFor(picking)
	p.Press("Escape", 27, 0)
	p.WaitFor(`!(` + picking + `)`)
	explore("2")

	p.Click("#add-comment")
	p.Type("#comment-text", "In generale va bene")
	var review []string
	p.MustEval(`[...document.querySelectorAll('#review-list .review-comment')].map(li => li.innerText.replace(/\s+/g, ' ').trim())`, &review)
	if strings.Join(review, "|") != "UI · contatore Il pulsante si vede poco|In generale va bene nell’editor" {
		t.Fatalf("review %q", review)
	}
	p.Click("#send-feedback")
	lines, code := c.finish()
	want := `,"choices":{},"comments":[{"anchor":"UI · contatore","text":"Il pulsante si vede poco"},{"anchor":null,"text":"In generale va bene"}],"images":[]}`
	if code != 0 || len(lines) != 1 || !strings.HasSuffix(lines[0], want) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
}

const webp = "UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA=="

func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const shots = `document.querySelectorAll('#images li').length`

func TestPageAttachesPastedAndDroppedScreenshots(t *testing.T) {
	c, p := openRound(t, richRound, 1280)
	png := screenshot(t)
	jpg := encoded(t, func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, nil) })
	gifImage := encoded(t, func(w io.Writer, m image.Image) error { return gif.Encode(w, m, nil) })
	webpImage, _ := base64.StdEncoding.DecodeString(webp)

	p.Drop("#comment-text", []string{writeFile(t, "shot.png", png), writeFile(t, "photo.jpg", jpg)})
	p.WaitFor(shots + ` === 2`)
	p.Click("#comment-text")
	p.Paste("image/png", png)
	p.WaitFor(shots + ` === 3`)
	p.Drop("#draft-area", []string{writeFile(t, "anim.gif", gifImage), writeFile(t, "pic.webp", webpImage)})
	p.WaitFor(shots + ` === 5`)
	p.WaitFor(`[...document.querySelectorAll('#images img')].every(img => img.complete && img.naturalWidth > 0)`)

	p.Click("#images li:nth-child(2) button")
	p.WaitFor(shots + ` === 4`)
	var draft struct {
		Review, Refusal, Editor string
		Disabled                bool
	}
	p.MustEval(`({Review: document.querySelector('#review-list').innerText, Refusal: document.querySelector('#image-refusal').textContent, Editor: document.querySelector('#comment-text').value, Disabled: document.querySelector('#send-feedback').disabled})`, &draft)
	if !strings.Contains(draft.Review, "4 screenshot") || draft.Refusal != "" || draft.Editor != "" || draft.Disabled {
		t.Fatalf("draft with screenshots: %+v", draft)
	}

	p.Reload()
	p.WaitFor(formShown + ` && ` + shots + ` === 4 && !document.querySelector('#send-feedback').disabled`)
	p.MustEval(`document.querySelector('#comment-text').focus()`, nil)
	p.Press("Enter", 13, 4)
	p.WaitFor(`document.querySelector('#feedback').dataset.phase === 'sent'`)
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	got := images(t, lines[0])
	if len(got) != 4 {
		t.Fatalf("images %q, want 4", got)
	}
	for i, want := range [][]byte{png, nil, gifImage, webpImage} {
		b, err := os.ReadFile(got[i])
		switch {
		case !filepath.IsAbs(got[i]) || err != nil:
			t.Errorf("image %d at %q: %v", i, got[i], err)
		case want == nil && !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
			t.Errorf("pasted image %d is not a PNG: % x", i, b[:min(8, len(b))])
		case want != nil && !bytes.Equal(b, want):
			t.Errorf("image %d differs from the dropped file", i)
		}
	}
	p.WaitFor(`document.querySelector('#sent-list').innerText.includes('4 screenshot')`)
}

func TestPageRefusesScreenshotsOutsideTheBounds(t *testing.T) {
	c, p := openRound(t, richRound, 390)
	png := screenshot(t)
	refusal := func(contains string, want int) {
		t.Helper()
		p.WaitFor(`document.querySelector('#image-refusal').textContent.includes(` + fmt.Sprintf("%q", contains) + `) && document.querySelector('#image-refusal').checkVisibility()`)
		var n int
		p.MustEval(shots, &n)
		if n != want {
			t.Fatalf("after the refusal %q, %d screenshots are in the draft, want %d", contains, n, want)
		}
	}
	nine := make([]string, 9)
	for i := range nine {
		nine[i] = writeFile(t, fmt.Sprintf("shot-%d.png", i), png)
	}

	p.Drop("#comment-text", []string{writeFile(t, "shot.png", png), writeFile(t, "note.png", []byte("not an image"))})
	refusal("«note.png» non è un’immagine PNG, JPEG, WebP o GIF", 0)
	p.Drop("#comment-text", nil, cdptest.DragItem{MimeType: "text/uri-list", Data: "https://example.com/shot.png"})
	refusal("Link e percorsi non vengono caricati", 0)
	p.Drop("#comment-text", nine)
	refusal("Al massimo 8 screenshot per invio", 0)
	p.Drop("#comment-text", nil, cdptest.DragItem{MimeType: "text/uri-list", Data: "file:///Users/me/Desktop/shot.png"})
	refusal("Link e percorsi non vengono caricati", 0)
	p.Drop("#comment-text", []string{writeFile(t, "huge.png", append(png, make([]byte, 10<<20-len(png)+1)...))})
	refusal("«huge.png» supera 10 MB", 0)

	p.Drop("#comment-text", nine[:8])
	p.WaitFor(shots + ` === 8 && document.querySelector('#image-refusal').textContent === ''`)
	p.Drop("#comment-text", nine[8:])
	refusal("con questi sarebbero 9", 8)
	p.MustEval(`window.scrollTo({top: 0, behavior: 'instant'})`, nil)
	p.WaitFor(`scrollY === 0 && document.querySelector('#mobile-summary').textContent === '8 screenshot in bozza'`)
	p.Click("#send-feedback")
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 || len(images(t, lines[0])) != 8 {
		t.Fatalf("exit %d, output %q", code, lines)
	}
}
