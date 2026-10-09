package live

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
	"github.com/taekwondodev/lavagna/internal/conversation"
)

// callServer serves one call's spec on a fixed origin and can stop without
// returning a batch, as an interrupted call does.
type callServer struct {
	t  *testing.T
	s  *server
	hs *http.Server
}

func serveCall(t *testing.T, o conversation.Origin, spec roundSpec) *callServer {
	t.Helper()
	ln, err := net.Listen("tcp", o.Host())
	if err != nil {
		t.Fatal(err)
	}
	c := &callServer{t: t, s: newRound(spec), hs: &http.Server{}}
	c.hs.Handler = c.s.handler()
	go c.hs.Serve(ln)
	t.Cleanup(c.stop)
	return c
}

func (c *callServer) stop() {
	c.s.stop()
	c.hs.Close()
}

func freeOrigin(t *testing.T) conversation.Origin {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: conversation.Secret(32)}
}

// nextCall is spec as a later call of the same phase serves it.
func nextCall(spec roundSpec, call string) roundSpec {
	spec.Call, spec.Token = call, "tok-"+call
	return spec
}

// reload reloads p and waits for the new document's shell.
func reload(p *cdptest.Page) {
	p.MustEval(`window.previousDocument = true`, nil)
	p.Reload()
	p.WaitFor(`!window.previousDocument && document.readyState === 'complete' && document.querySelector('#rail .card')`)
}

func stubClipboard(p *cdptest.Page) {
	p.MustEval(`navigator.clipboard.writeText = text => { window.copied = text; return Promise.resolve(); }`, nil)
}

func TestReloadRebuildsThePageAndRereadsAnyQuestionWhileNothingListens(t *testing.T) {
	run := newPhaseRun(t)
	run.call(boundaryRound, map[string]string{"two/tall.css": `#tall { height: 1500px; }`})
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.WaitFor(`navigator.serviceWorker.controller`)
	p.WaitFor(`(async () => { const keys = await (await caches.open('lavagna:' + location.pathname)).keys(); const urls = keys.map(k => k.url);
		return urls.some(u => u.endsWith('/view.json')) && view.phase.questions.every(q => q.resources.every(r => urls.includes(new URL(r, location.origin).href))); })()`)

	p.Click(`.option[data-option="a"]`)
	p.Click("#send")
	run.outcome()
	p.WaitFor(`document.querySelector('#turn').textContent !== 'Tocca a te'`)
	run.silence()
	p.Type("#composer", "Scritto dopo l'invio")
	p.Click(`.card[data-target=":overview"]`)
	p.Type("#composer", "Commento generale in bozza")

	reload(p)
	// The batch was returned before the reload, so its stage stays.
	wantText(t, p, "#turn", "Consegnato al terminale")
	if evalString(p, `String(document.querySelector('.card[data-target=":overview"]').getAttribute('aria-current'))`) != "true" {
		t.Error("the reload did not restore the active Overview")
	}
	if got := evalString(p, `document.querySelector('#composer').value`); got != "Commento generale in bozza" {
		t.Errorf("Overview draft after the reload %q", got)
	}
	if !strings.Contains(evalString(p, `document.querySelector('.card[data-target="two"]').className`), "unseen") ||
		strings.Contains(evalString(p, `document.querySelector('.card[data-target="one"]').className`), "unseen") {
		t.Error("the reload lost the seen marks")
	}
	p.Click(`.card[data-target="one"]`)
	if got := evalString(p, `document.querySelector('#composer').value`); got != "Scritto dopo l'invio" {
		t.Errorf("question draft after the reload %q", got)
	}

	p.Click(`.card[data-target="two"]`)
	p.WaitFor(`document.querySelector('#content') && document.querySelector('#content').src.startsWith('blob:')`)
	f := p.Frame("#content")
	f.WaitFor(`document.querySelector('#tall') && getComputedStyle(document.querySelector('#tall')).height === '1500px'`)
	if got := evalString(p, `document.querySelector('#content').getAttribute('sandbox')`); got != "allow-scripts" {
		t.Errorf("the offline frame is sandboxed as %q", got)
	}
	snapshot := frameString(f, `JSON.stringify([[...document.querySelectorAll('link[rel=stylesheet], script[src]')].every(n => (n.href || n.src).startsWith('data:')),
		[...document.querySelectorAll('script[src]')].every(n => n.integrity.startsWith('sha256-')),
		/script-src 'sha256-/.test(document.querySelector('meta[http-equiv="Content-Security-Policy"]').content)])`)
	if snapshot != "[true,true,true]" {
		t.Errorf("offline frame resources as data URLs, scripts pinned, CSP hashes: %s", snapshot)
	}
}

const reconcileSecond = `::: settled crash journal
Si perde al massimo un record.
:::
::: reply retention
Ho tolto il mese: troppo spazio.
:::
# Quanto teniamo le sessioni chiuse? {id="retention"}
## Decidere
- [week] Una settimana
- [year] Un anno
# Versioniamo il formato? {id="format"}
## Decidere
- [v] Campo version
- [none] Nessuna versione
`

func TestNextCallReconcilesTheDraftPerQuestion(t *testing.T) {
	run := newPhaseRun(t)
	run.call(crashRound, crashFiles())
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.Click(`.option[data-option="journal"]`)
	p.Type("#composer", "Prima nota")
	p.Click("#stage-message")
	p.Click(`.card[data-target="retention"]`)
	p.Click(`.option[data-option="week"]`)
	p.Click("#send")
	run.outcome()
	// Wait for the relay to serve the page, which redraws it.
	p.WaitFor(`live && document.querySelector('#turn').textContent === 'Consegnato al terminale'`)

	p.Click(`.option[data-option="month"]`)
	p.Type("#composer", "Ancora sulla durata")
	p.Click("#stage-message")
	p.Click(`.card[data-target="crash"]`)
	p.Click(`.option[data-option="atomic"]`)
	wantText(t, p, "#summary", "Consegnato al terminale · stato in tempo reale non disponibile · 3 in bozza")

	run.call(reconcileSecond, nil)
	p.WaitFor(`document.querySelector('#round-label').textContent === 'r2'`)
	wantText(t, p, "#question-head h1", "Quanto teniamo le sessioni chiuse?")
	p.Click(`.card[data-target="crash"]`)
	thread := func() string {
		var entries []string
		p.MustEval(`[...document.querySelectorAll('#thread .message')].map(m => m.querySelector('.message-who').textContent.replace('×', '') + ': ' + m.querySelector('.message-text').textContent)`, &entries)
		return strings.Join(entries, " | ")
	}
	if got := thread(); got != "Tu: Prima nota | lavagna: Chiusa con B; la tua bozza A non è stata inviata. Scrivilo qui se vuoi riaprirla." {
		t.Errorf("settled question thread: %s", got)
	}
	if evalString(p, `document.querySelector('.card[data-target="retention"]').closest('.round').className`) != "round round-current" {
		t.Error("the unsettled question did not move into the new round")
	}
	p.Click(`.card[data-target="retention"]`)
	if got := thread(); got != "Agente: Ho tolto il mese: troppo spazio. | lavagna: La tua scelta B non esiste più nella nuova versione | Tu · in bozza: Ancora sulla durata" {
		t.Errorf("rewritten question thread: %s", got)
	}
	if evalString(p, `document.querySelector('.option[data-option="week"]').getAttribute('aria-pressed')`) != "true" {
		t.Error("the sent choice whose option survives was not kept")
	}
	wantText(t, p, "#summary", "1 in bozza · Q2 +1 msg")
	p.Click("#send")
	if got := run.outcome(); jsonOf(got["questions"]) != `{"retention":{"choice":"week","messages":["Ancora sulla durata"]}}` {
		t.Fatalf("batch after reconciling %s", jsonOf(got))
	}
}

func TestUncertainReturnsTheBatchToTheDraftAndTheLedgerDeduplicates(t *testing.T) {
	o := freeOrigin(t)
	spec := phaseSpec(t, o, crashRound, nil)
	first := serveCall(t, o, spec)
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.Click(`.option[data-option="journal"]`)
	p.Type("#composer", "Inviato e perso")
	p.Click("#stage-message")
	p.Click("#send")
	sent := <-first.s.accepted
	p.WaitFor(`document.querySelector('#turn').textContent === 'Inviato'`)
	p.Click(`.option[data-option="atomic"]`)
	p.Type("#composer", "Scritto dopo")
	p.Click("#stage-message")

	first.stop()
	p.WaitFor(`document.querySelector('#turn').textContent === 'Consegna non riuscita'`)
	wantText(t, p, "#summary", "Consegna non riuscita: l’agente è stato interrotto. I messaggi inviati sono tornati in bozza: puoi reinviarli. · 3 in bozza")
	var staged []string
	p.MustEval(`[...document.querySelectorAll('#thread .message')].map(m => m.className.replace('message message-', '') + ':' + m.querySelector('.message-text').textContent)`, &staged)
	if jsonOf(staged) != `["staged:Inviato e perso","staged:Scritto dopo"]` {
		t.Errorf("thread after Uncertain %v: the batch returns before what was written after Send", staged)
	}
	if evalString(p, `document.querySelector('.option[data-option="atomic"]').getAttribute('aria-pressed')`) != "true" {
		t.Error("the choice changed after Send lost to the batch's")
	}

	held := nextCall(spec, "c2")
	held.Ledger = held.Ledger.Accept(sent.ledgerBatch())
	second := serveCall(t, o, held)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Tocca a te'`)
	p.MustEval(`[...document.querySelectorAll('#thread .message')].map(m => m.className.replace('message message-', '') + ':' + m.querySelector('.message-text').textContent)`, &staged)
	if jsonOf(staged) != `["user:Inviato e perso","staged:Scritto dopo"]` {
		t.Errorf("thread when the ledger already holds the batch %v", staged)
	}
	p.Click("#send")
	if got := (<-second.s.accepted).phaseLine(); jsonOf(got.Questions) != `{"crash":{"choice":"atomic","messages":["Scritto dopo"]}}` {
		t.Fatalf("resend %s", jsonOf(got.Questions))
	}
}

func TestUncertainBatchTheLedgerNeverReceivedIsSentAgain(t *testing.T) {
	o := freeOrigin(t)
	spec := phaseSpec(t, o, crashRound, nil)
	first := serveCall(t, o, spec)
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.Click(`.option[data-option="journal"]`)
	p.Type("#composer", "Mai arrivato")
	p.Click("#stage-message")
	p.Click("#send")
	<-first.s.accepted
	p.WaitFor(`document.querySelector('#turn').textContent === 'Inviato'`)
	first.stop()
	p.WaitFor(`document.querySelector('#turn').textContent === 'Consegna non riuscita'`)

	second := serveCall(t, o, nextCall(spec, "c2"))
	p.WaitFor(`document.querySelector('#turn').textContent === 'Tocca a te'`)
	wantText(t, p, "#summary", "2 in bozza · Q1 → B · Q1 +1 msg")
	p.Click("#send")
	if got := (<-second.s.accepted).phaseLine(); jsonOf(got.Questions) != `{"crash":{"choice":"journal","messages":["Mai arrivato"]}}` {
		t.Fatalf("resend %s", jsonOf(got.Questions))
	}
}

func TestReloadInTheAcceptedWindowShowsTheBatchWithoutAStage(t *testing.T) {
	o := freeOrigin(t)
	call := serveCall(t, o, phaseSpec(t, o, crashRound, nil))
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.WaitFor(`navigator.serviceWorker.controller`)
	p.Type("#composer", "Nel batch accettato")
	p.Click("#stage-message")
	p.Click("#send")
	<-call.s.accepted
	p.WaitFor(`document.querySelector('#turn').textContent === 'Inviato'`)

	p.Hold("*/events")
	reload(p)
	if got := evalString(p, `document.querySelector('#turn').textContent + ' | ' + document.querySelector('#summary').textContent`); got != "Riconnessione a lavagna… | Riconnessione a lavagna…" {
		t.Errorf("reloaded in the Accepted window: %s", got)
	}
	wantText(t, p, "#thread .message-user", "TuNel batch accettato")
	if evalString(p, `String(document.querySelector('#send').disabled)`) != "true" {
		t.Error("Send is enabled while the stream is down")
	}
	for _, id := range p.Held() {
		p.Release(id)
	}
	p.WaitFor(`document.querySelector('#turn').textContent === 'Inviato'`)
}

func TestTabsShareTheDraftAndAStaleTabCannotOverwriteIt(t *testing.T) {
	o := freeOrigin(t)
	serveCall(t, o, phaseSpec(t, o, crashRound, nil))
	b := cdptest.Start(t)
	one := b.Open(o.URL(), 1280, 860)
	one.WaitFor(`document.querySelector('#content')`)
	two := b.Open(o.URL(), 1280, 860)
	two.WaitFor(`document.querySelector('#content')`)
	one.Type("#composer", "Scritto nella prima scheda")
	two.WaitFor(`document.querySelector('#composer').value === 'Scritto nella prima scheda'`)
	one.Click(`.option[data-option="journal"]`)
	two.WaitFor(`document.querySelector('.option[data-option="journal"]').getAttribute('aria-pressed') === 'true'`)
	wantText(t, two, "#summary", "2 in bozza · Q1 → B · Q1 +1 msg")

	two.MustEval(`(() => { const key = 'lavagna:' + location.pathname; const record = JSON.parse(localStorage.getItem(key)); record.call = 'c9'; record.overview.composer = 'Dalla chiamata successiva'; localStorage.setItem(key, JSON.stringify(record)); })()`, nil)
	one.Type("#composer", " e ancora")
	time.Sleep(300 * time.Millisecond)
	stored := evalString(one, `localStorage.getItem('lavagna:' + location.pathname)`)
	if !strings.Contains(stored, `"call":"c9"`) || strings.Contains(stored, "e ancora") {
		t.Errorf("a tab of call c1 overwrote the record of call c9: %s", stored)
	}
}

func TestDetachedTabIsReadOnlyAndCopiesTheDraft(t *testing.T) {
	o := freeOrigin(t)
	call := serveCall(t, o, phaseSpec(t, o, crashRound, nil))
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.Click(`.option[data-option="journal"]`)
	p.Type("#composer", "Una domanda sul journal")
	p.Click("#stage-message")
	p.Click(`.card[data-target="retention"]`)
	p.Type("#free-text", "Finché serve")
	p.Click(`.card[data-target=":overview"]`)
	p.Type("#composer", "In generale va bene")

	call.stop()
	ln, err := net.Listen("tcp", o.Host())
	if err != nil {
		t.Fatal(err)
	}
	squatter := &http.Server{Handler: http.NotFoundHandler()}
	go squatter.Serve(ln)
	t.Cleanup(func() { squatter.Close() })

	// The squatter answers the stream with 404, so it fails for good: the tab
	// is detached, which is the only state a stream failing for good reaches.
	p.WaitFor(`document.querySelector('#turn').textContent === 'Scheda non collegata'`)
	if got := evalString(p, `document.querySelector('#composer').value`); got != "In generale va bene" {
		t.Errorf("the Overview draft after the stream failed: %q", got)
	}
	p.Click(`.card[data-target="crash"]`)
	wantText(t, p, "#thread .message-staged", "Tu · in bozza×Una domanda sul journal")
	if evalString(p, `document.querySelector('.option[data-option="journal"]').getAttribute('aria-pressed')`) != "true" {
		t.Error("the staged choice was lost when the stream failed")
	}
	if got := evalString(p, `[document.querySelector('#composer').readOnly, document.querySelector('#send').disabled, document.querySelector('#copy-draft').hidden].join()`); got != "true,true,false" {
		t.Errorf("detached tab composer read-only, Send disabled, Copia bozza hidden: %s", got)
	}
	p.MustEval(`window.sends = 0; const original = window.fetch; window.fetch = (url, init) => { if (String(url).endsWith('send')) sends++; return original(url, init); }`, nil)
	p.Press("Enter", 13, 4)
	time.Sleep(300 * time.Millisecond)
	if evalString(p, `String(window.sends)`) != "0" {
		t.Error("⌘↩ sent from a detached tab")
	}
	stubClipboard(p)
	p.Click("#copy-draft")
	p.WaitFor(`window.copied`)
	want := "Q1 · Cosa succede se una sessione crasha a metà scrittura?\nScelta B: Journal append-only con checksum\nUna domanda sul journal\n\nQ2 · Quanto teniamo le sessioni chiuse?\nRisposta: Finché serve\n\nPanoramica\nIn generale va bene"
	if got := evalString(p, `window.copied`); got != want {
		t.Errorf("copied draft %q\nwant %q", got, want)
	}
}

func TestFreshOriginRebuildsRailAndThreadsFromTheServer(t *testing.T) {
	o := freeOrigin(t)
	spec := phaseSpec(t, o, crashRound, nil)
	crash := spec.Ledger.Questions["crash"]
	crash.Thread = append(crash.Thread, conversation.Message{Author: "agent", Round: 1, Text: "Un messaggio già nel thread."})
	spec.Ledger.Questions["crash"] = crash
	serveCall(t, o, spec)
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#thread .message-agent')`)
	wantText(t, p, "#thread .message-agent", "AgenteUn messaggio già nel thread.")
	var cards []string
	p.MustEval(`[...document.querySelectorAll('#rail [data-target], #rail [data-planned]')].map(c => c.dataset.target || c.dataset.planned)`, &cards)
	if jsonOf(cards) != `[":overview","crash","retention","cleanup"]` {
		t.Errorf("rail on a fresh origin %v", cards)
	}
}

func TestBuildMismatchReloadsOnceAndAnUnreadableDraftIsOfferedOnce(t *testing.T) {
	o := freeOrigin(t)
	call := serveCall(t, o, phaseSpec(t, o, crashRound, nil))
	call.s.view.Build = "another-build"
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`sessionStorage.getItem('lavagna:reload:' + location.pathname) === 'another-build' && document.querySelector('#content')`)
	if got := evalString(p, `performance.getEntriesByType('navigation')[0].type`); got != "reload" {
		t.Errorf("a different build did not reload the tab: %s", got)
	}
	p.MustEval(`window.stayed = true`, nil)
	time.Sleep(700 * time.Millisecond)
	if evalString(p, `String(window.stayed)`) != "true" {
		t.Fatal("the tab reloaded again for the same build")
	}

	p.MustEval(`localStorage.setItem('lavagna:' + location.pathname, JSON.stringify({view: {round: 'r1'}, draft: {comments: [{text: 'Vecchio commento', anchor: null}], editor: 'Frase a metà', choices: {crash: 'atomic'}}}))`, nil)
	reload(p)
	p.WaitFor(`document.querySelector('#content')`)
	if evalString(p, `String(document.querySelector('#salvage').hidden)`) != "false" {
		t.Fatal("an unreadable draft was not offered")
	}
	stubClipboard(p)
	p.Click("#salvage-copy")
	p.WaitFor(`window.copied`)
	if got := evalString(p, `window.copied`); got != "Vecchio commento\n\nFrase a metà" {
		t.Errorf("salvaged text %q", got)
	}
	reload(p)
	p.WaitFor(`document.querySelector('#content')`)
	if evalString(p, `String(document.querySelector('#salvage').hidden)`) != "true" {
		t.Error("the unreadable draft was offered twice")
	}
}

func TestCloseRemovesDraftSeenMarksCacheAndWorker(t *testing.T) {
	run := newPhaseRun(t)
	run.call(crashRound, crashFiles())
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.WaitFor(`navigator.serviceWorker.controller`)
	p.WaitFor(`(async () => (await caches.keys()).length > 0)()`)
	p.Click(`.option[data-option="journal"]`)
	p.Click("#send")
	run.outcome()
	p.Type("#composer", "Bozza da cancellare")

	var out bytes.Buffer
	if code := Close(run.getenv, &out); code != exitOK || !strings.Contains(out.String(), `"page":"cleaned"`) {
		t.Fatalf("close %d: %s", code, out.String())
	}
	p.WaitFor(`document.querySelector('#turn').textContent === 'Concluso'`)
	got := evalString(p, `(async () => JSON.stringify([localStorage.length, sessionStorage.length, (await caches.keys()).length, Boolean(await navigator.serviceWorker.getRegistration())]))()`)
	if got != "[0,0,0,false]" {
		t.Errorf("after close: localStorage, sessionStorage, caches, worker = %s", got)
	}
}

func TestPhaseDraftSizeNearTheBatchBounds(t *testing.T) {
	o := freeOrigin(t)
	call := serveCall(t, o, phaseSpec(t, o, crashRound, nil))
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	png, err := base64.StdEncoding.DecodeString(pixel)
	if err != nil {
		t.Fatal(err)
	}
	var shots []string
	for i := range 8 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("shot-%d.png", i))
		if err := os.WriteFile(path, png, 0o600); err != nil {
			t.Fatal(err)
		}
		shots = append(shots, path)
	}
	stageText := func() {
		p.Click("#composer")
		p.Insert(prose(32000))
		p.Click("#stage-message")
	}
	stageText()
	p.Drop("#thread", shots)
	p.WaitFor(`document.querySelectorAll('#thread .message-staged img').length === 8`)
	p.Click("#send")
	<-call.s.accepted
	p.WaitFor(`document.querySelector('#turn').textContent === 'Inviato'`)
	// Uploads close once a batch is admitted, so the new draft holds text only.
	stageText()
	size := len(evalString(p, `localStorage.getItem('lavagna:' + location.pathname)`))
	t.Logf("phase draft holding an accepted batch at the 32 KiB text and 8-screenshot bounds plus a new 32 KiB text draft: %d UTF-16 code units", size)
	if size > 256<<10 {
		t.Errorf("phase draft %d code units, want under 256 Ki", size)
	}
}
