package live

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/diagram"
	"github.com/taekwondodev/lavagna/internal/page"
)

// phaseRun drives successive round calls of one conversation through
// PhaseRound, as the agent would, while a browser page answers them.
type phaseRun struct {
	t      *testing.T
	getenv func(string) string
	url    string
	out    *bytes.Buffer
	done   chan int
}

type firstLine struct {
	once sync.Once
	ch   chan string
}

func (w *firstLine) Write(p []byte) (int, error) {
	w.once.Do(func() { w.ch <- string(p) })
	return len(p), nil
}

func newPhaseRun(t *testing.T) *phaseRun {
	home := t.TempDir()
	t.Setenv("HOME", home)
	session := "page-" + conversation.Secret(8)
	return &phaseRun{t: t, getenv: func(key string) string {
		switch key {
		case "HOME":
			return home
		case "LAVAGNA_SESSION":
			return session
		case "BROWSER":
			return "true"
		}
		return ""
	}}
}

// call starts one call with source and the question resources in files, keyed
// by their path under DIR, and returns once the page origin listens.
func (r *phaseRun) call(source string, files map[string]string) {
	r.t.Helper()
	dir := ""
	if len(files) > 0 {
		dir = r.t.TempDir()
		files["round.md"] = source
		for name, body := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				r.t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				r.t.Fatal(err)
			}
		}
	}
	out := &bytes.Buffer{}
	status := &firstLine{ch: make(chan string, 1)}
	done := make(chan int, 1)
	go func() { done <- PhaseRound(r.getenv, strings.NewReader(source), dir, out, status) }()
	select {
	case line := <-status.ch:
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "http://") {
				r.url = field
			}
		}
		if r.url == "" {
			r.t.Fatalf("status line %q", line)
		}
	case code := <-done:
		r.t.Fatalf("call ended with %d: %s", code, out.String())
	}
	r.out, r.done = out, done
}

// outcome waits for the running call to return and decodes its result.
func (r *phaseRun) outcome() map[string]any {
	r.t.Helper()
	select {
	case code := <-r.done:
		if code != exitOK {
			r.t.Fatalf("call ended with %d: %s", code, r.out.String())
		}
	case <-time.After(10 * time.Second):
		r.t.Fatal("the call did not return a batch")
	}
	var result map[string]any
	if err := json.Unmarshal(r.out.Bytes(), &result); err != nil {
		r.t.Fatalf("result %q: %v", r.out.String(), err)
	}
	return result
}

func evalString(p *cdptest.Page, expr string) string {
	var s string
	p.MustEval(expr, &s)
	return s
}

func frameString(f *cdptest.Frame, expr string) string {
	var s string
	f.MustEval(expr, &s)
	return s
}

func wantText(t *testing.T, p *cdptest.Page, selector, want string) {
	t.Helper()
	if got := evalString(p, `document.querySelector(`+jsString(selector)+`).textContent`); got != want {
		t.Errorf("%s: %q, want %q", selector, got, want)
	}
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const crashRound = "::: phase Archivio delle sessioni\n:::\n" + `# Cosa succede se una sessione crasha a metà scrittura? {id="crash"}
Con un file per sessione resta un rischio: il file può restare scritto a metà.

## Capire
Il salvataggio tronca il file e poi lo riscrive.

## Confrontare
<p id="screen">schermata</p>

## Decidere
- [atomic] Scrittura atomica: file temporaneo + rename {recommended}
  Il file è sempre vecchio o nuovo.
  => Il crash avviene sul ` + "`.tmp`" + `: il file resta integro fino al rename.
- [journal] Journal append-only con checksum
  => Si perde solo l'ultimo record.

Risolve il caso con poche righe.

# Quanto teniamo le sessioni chiuse? {id="retention"}
Le sessioni chiuse occupano spazio.

## Decidere
- [week] Una settimana
- [month] Un mese

# Chi esegue la pulizia? {id="cleanup" after="retention"}
`

// screenScript follows the choice from inside the frame, as a round prototype
// would.
const screenScript = `document.addEventListener('lavagna:option', event => {
  document.getElementById('screen').textContent = JSON.stringify(event.detail);
});`

func crashFiles() map[string]string { return map[string]string{"crash/screen.js": screenScript} }

func TestPageAnswersAPhaseOneQuestionAtATime(t *testing.T) {
	run := newPhaseRun(t)
	run.call(crashRound, crashFiles())
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)

	wantText(t, p, "#phase-title", "Archivio delle sessioni")
	wantText(t, p, "#round-label", "r1")
	wantText(t, p, "#turn", "Tocca a te")
	var rail []string
	p.MustEval(`[...document.querySelectorAll('#rail > *')].map(n => n.dataset.target || n.querySelector('.round-head') && n.querySelector('.round-head').textContent || n.className)`, &rail)
	if jsonOf(rail) != `[":overview","Round 1current","Round 2previsto","legend"]` {
		t.Errorf("rail %v, want the Overview, the current round, the planned round and the legend", rail)
	}
	var cards []string
	p.MustEval(`[...document.querySelectorAll('.round-current .card')].map(c => c.dataset.target + (c.getAttribute('aria-current') ? ':active' : ''))`, &cards)
	if jsonOf(cards) != `["crash:active","retention"]` {
		t.Errorf("current round cards %v, want crash active by default", cards)
	}
	wantText(t, p, ".round-planned .card-locked", "Q3Chi esegue la pulizia?dopo Q2")
	wantText(t, p, ".round-planned .round-note", "Previsto dall’albero delle decisioni: può cambiare.")

	wantText(t, p, "#question-head h1", "Cosa succede se una sessione crasha a metà scrittura?")
	wantText(t, p, "#question-head .lead", "Con un file per sessione resta un rischio: il file può restare scritto a metà.")
	wantText(t, p, ".option.recommended", "AConsigliataScrittura atomica: file temporaneo + renameIl file è sempre vecchio o nuovo.")
	wantText(t, p, ".reason", "Perché A. Risolve il caso con poche righe.")
	if got := evalString(p, `document.querySelector('#free-text').placeholder`); got != "Inserisci la tua risposta se nessuna opzione ti convince" {
		t.Errorf("free-text card %q", got)
	}

	f := p.Frame("#content")
	f.WaitFor(`document.querySelector('#screen').textContent.startsWith('{')`)
	variant := func() string {
		return frameString(f, `JSON.stringify([document.documentElement.dataset.option || null, document.documentElement.dataset.free, document.documentElement.dataset.variant,
			document.querySelector('.effect:not([hidden])') && document.querySelector('.effect:not([hidden])').textContent,
			document.querySelector('.chip.on').textContent, document.querySelector('.preview-note').textContent, document.querySelector('#screen').textContent])`)
	}
	if got := frameString(f, `[...document.querySelectorAll('main > section')].map(s => s.id).join()`); got != "capire,confrontare" {
		t.Errorf("frame sections %q, want 01 and 02 only", got)
	}
	if got := variant(); got != `[null,"false","atomic","Con A: Il crash avviene sul .tmp: il file resta integro fino al rename.","A ★","mostra la consigliata","{\"option\":null,\"free\":false,\"variant\":\"atomic\"}"]` {
		t.Errorf("02 before a choice: %s", got)
	}

	p.Click(`.option[data-option="journal"]`)
	f.WaitFor(`document.documentElement.dataset.option === 'journal'`)
	if got := variant(); got != `["journal","false","journal","Con B: Si perde solo l'ultimo record.","B","segue la tua scelta","{\"option\":\"journal\",\"free\":false,\"variant\":\"journal\"}"]` {
		t.Errorf("02 following the choice: %s", got)
	}
	wantText(t, p, `.card[data-target="crash"] .mark`, "B")
	wantText(t, p, "#summary", "1 in bozza · Q1 → B")

	f.Click(`.chip[data-variant="atomic"]`)
	f.WaitFor(`document.documentElement.dataset.variant === 'atomic'`)
	if got := variant(); got != `["journal","false","atomic","Con A: Il crash avviene sul .tmp: il file resta integro fino al rename.","A ★","anteprima: non è una scelta","{\"option\":\"journal\",\"free\":false,\"variant\":\"atomic\"}"]` {
		t.Errorf("02 previewing another option: %s", got)
	}
	wantText(t, p, "#summary", "1 in bozza · Q1 → B")

	p.Click(`.option[data-option="journal"]`)
	f.WaitFor(`!document.documentElement.dataset.option`)
	wantText(t, p, "#summary", "Niente in bozza")
	if got := evalString(p, `document.querySelector('.option[data-option="journal"]').getAttribute('aria-pressed')`); got != "false" {
		t.Errorf("clicking the selected option left it pressed: %s", got)
	}

	p.Type("#free-text", "Dipende dal disco")
	f.WaitFor(`document.documentElement.dataset.free === 'true'`)
	if got := variant(); got != `[null,"true","atomic","Con A: Il crash avviene sul .tmp: il file resta integro fino al rename.","A ★","risposta libera: l’agente la disegna nel prossimo turno","{\"option\":null,\"free\":true,\"variant\":\"atomic\"}"]` {
		t.Errorf("02 with a free-text answer: %s", got)
	}
	wantText(t, p, "#summary", "1 in bozza · Q1 → ✎")
	p.Click(`.option[data-option="journal"]`)
	f.WaitFor(`document.documentElement.dataset.option === 'journal' && document.documentElement.dataset.free === 'false'`)
	if got := evalString(p, `document.querySelector('.option-free').className`); got != "option option-free" {
		t.Errorf("an option did not replace the free-text answer: %s", got)
	}

	p.Type("#composer", "Il rename è atomico su NFS?")
	p.Click("#stage-message")
	wantText(t, p, "#thread .message-staged", "Tu · in bozza×Il rename è atomico su NFS?")
	p.Click(`.card[data-target=":overview"]`)
	wantText(t, p, "#question-head h1", "Archivio delle sessioni")
	wantText(t, p, "#question-head .empty", "Ancora nessuna decisione presa.")
	if n := evalString(p, `String(document.querySelectorAll('iframe').length)`); n != "0" {
		t.Errorf("the Overview kept %s frames", n)
	}
	p.Type("#composer", "Il design mi convince")
	if !strings.Contains(evalString(p, `document.querySelector('.card[data-target="retention"]').className`), "unseen") {
		t.Error("an unopened question is not marked unseen")
	}
	p.Click(`.card[data-target="retention"]`)
	if strings.Contains(evalString(p, `document.querySelector('.card[data-target="retention"]').className`), "unseen") {
		t.Error("an opened question is still unseen")
	}
	wantText(t, p, "#summary", "3 in bozza · Q1 → B · Q1 +1 msg · Panoramica +1 msg")
	wantText(t, p, "#send-count", "3")

	p.Press("Enter", 13, 2)
	got := run.outcome()
	if jsonOf(got["questions"]) != `{"crash":{"choice":"journal","messages":["Il rename è atomico su NFS?"]}}` || jsonOf(got["overview"]) != `{"messages":["Il design mi convince"]}` {
		t.Fatalf("batch %s", jsonOf(got))
	}
	p.WaitFor(`document.querySelector('#turn').textContent === 'Consegnato al terminale'`)
	// No Pi session witnesses this conversation, so live status is unavailable.
	wantText(t, p, "#summary", "Consegnato al terminale · stato in tempo reale non disponibile")
	if evalString(p, `String(document.querySelector('#send').disabled)`) != "true" {
		t.Error("Send is enabled before the next call")
	}
	stored := evalString(p, `localStorage.getItem('lavagna:' + location.pathname)`)
	if strings.Contains(stored, "NFS") || strings.Contains(stored, "convince") {
		t.Errorf("the phase draft keeps sent items: %s", stored)
	}

	p.Click(`.card[data-target="crash"]`)
	wantText(t, p, "#thread .message-user", "TuIl rename è atomico su NFS?")
	p.Type("#composer", "E su un disco di rete?")
	p.Click("#stage-message")
	wantText(t, p, "#summary", "Consegnato al terminale · stato in tempo reale non disponibile · 1 in bozza")
	f = p.Frame("#content")
	f.WaitFor(`document.documentElement.dataset.option === 'journal'`)
	f.MustEval(`window.kept = true`, nil)
	p.MustEval(`document.querySelector('#question-head h1').dataset.kept = 'yes'`, nil)

	run.call("::: reply crash\nIl rename è atomico sullo stesso filesystem.\n:::\n::: reply retention\nUna settimana basta?\n:::\n", nil)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Tocca a te'`)
	var thread []string
	p.MustEval(`[...document.querySelectorAll('#thread .message')].map(m => m.className.replace('message message-', '') + ':' + m.querySelector('.message-text').textContent)`, &thread)
	if jsonOf(thread) != `["user:Il rename è atomico su NFS?","agent:Il rename è atomico sullo stesso filesystem.","staged:E su un disco di rete?"]` {
		t.Errorf("thread after a reply %v", thread)
	}
	if frameString(f, `String(window.kept)`) != "true" || evalString(p, `document.querySelector('#question-head h1').dataset.kept || ''`) != "yes" {
		t.Error("a reply redrew the question or its frame")
	}
	if evalString(p, `String(Boolean(document.querySelector('.card[data-target="retention"] .card-new')))`) != "true" {
		t.Error("a reply to another question shows no new-reply dot")
	}
	p.Click(`.card[data-target="retention"]`)
	if evalString(p, `String(Boolean(document.querySelector('.card[data-target="retention"] .card-new')))`) != "false" {
		t.Error("opening the question did not clear its new-reply dot")
	}
	wantText(t, p, "#summary", "1 in bozza · Q1 +1 msg")
	p.Click("#send")
	got = run.outcome()
	if jsonOf(got["questions"]) != `{"crash":{"choice":"journal","messages":["E su un disco di rete?"]}}` {
		t.Fatalf("second batch %s: the current round's answers repeat and staged messages follow", jsonOf(got))
	}
}

func TestPageFollowsRoundsDecisionsAndReplacements(t *testing.T) {
	run := newPhaseRun(t)
	run.call(crashRound, crashFiles())
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	p.Click(`.option[data-option="journal"]`)
	p.Click("#send")
	run.outcome()

	run.call("::: settled crash\nNessuna dipendenza nuova.\n:::\n# Versioniamo il formato? {id=\"format\"}\n## Decidere\n- [v] Campo version\n- [none] Nessuna versione\n", nil)
	p.WaitFor(`document.querySelector('#round-label').textContent === 'r2'`)
	wantText(t, p, ".round-closed .round-head", "Round 1chiuso")
	wantText(t, p, `.round-closed .card[data-target="crash"] .mark`, "✓ B")
	wantText(t, p, `.round-closed .card-ghost[data-ghost="retention"] .card-moved`, "spostata nel round corrente")
	var cards []string
	p.MustEval(`[...document.querySelectorAll('.round-current .card')].map(c => c.dataset.target)`, &cards)
	if jsonOf(cards) != `["retention","format"]` {
		t.Errorf("current round %v, want the unanswered question moved in before the new one", cards)
	}
	wantText(t, p, `.card[data-target=":overview"] .mark`, "1")

	wantText(t, p, "#question-head .state", "chiuso · deciso B")
	wantText(t, p, "#question-head .banner", "Round chiuso. Puoi rileggerla e commentarla a destra: il commento parte col prossimo invio e l’agente decide se riaprirla.")
	var options []string
	p.MustEval(`[...document.querySelectorAll('#decide .option')].map(o => o.dataset.option + ':' + o.getAttribute('aria-pressed') + ':' + o.disabled)`, &options)
	if jsonOf(options) != `["atomic:false:true","journal:true:true"]` || evalString(p, `String(Boolean(document.querySelector('#free-text')))`) != "false" {
		t.Errorf("a closed question's 03 %v must show its decision and stay read-only", options)
	}
	p.Click(`.option[data-option="atomic"]`)
	wantText(t, p, "#summary", "Niente in bozza")
	p.Type("#composer", "Rileggendo: e su NFS?")
	p.Click("#stage-message")
	p.Click(`.card[data-target="retention"]`)
	p.Click(`.option[data-option="week"]`)

	p.Click(`.card[data-target=":overview"]`)
	var rows [][]string
	p.MustEval(`[...document.querySelectorAll('.decisions tr')].map(r => [...r.children].map(c => c.textContent))`, &rows)
	if jsonOf(rows) != `[["Domanda","Decisione","Round","Perché","Scartate"],["Q1 · Cosa succede se una sessione crasha a metà scrittura?","Journal append-only con checksum","r1","Nessuna dipendenza nuova.","Scrittura atomica: file temporaneo + rename"]]` {
		t.Errorf("decisions table %v", rows)
	}
	p.Click(`.card[data-target="crash"]`)
	f := p.Frame("#content")
	f.WaitFor(`document.readyState === 'complete'`)
	f.MustEval(`window.kept = true`, nil)
	p.Click("#send")
	got := run.outcome()
	if jsonOf(got["questions"]) != `{"crash":{"messages":["Rileggendo: e su NFS?"]},"retention":{"choice":"week"}}` {
		t.Fatalf("batch %s: a closed question carries only its messages", jsonOf(got))
	}

	run.call(`# Quanto teniamo le sessioni chiuse? {id="retention"}
Riformulata.
## Decidere
- [week] Una settimana
- [year] Un anno
`, nil)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Tocca a te'`)
	if frameString(f, `String(window.kept)`) != "true" {
		t.Error("replacing another question redrew the active one")
	}
	if !strings.Contains(evalString(p, `document.querySelector('.card[data-target="retention"]').className`), "unseen") {
		t.Error("a replaced question did not return to unseen")
	}
	wantText(t, p, `.card[data-target="retention"] .mark`, "A")
	p.Type("#composer", "Ok")
	p.Click("#stage-message")
	p.Click("#send")
	run.outcome()

	run.call(`# Cosa succede se una sessione crasha a metà scrittura? {id="crash"}
Rivista dopo il tuo commento.
## Decidere
- [atomic] Scrittura atomica
- [journal] Journal
`, nil)
	p.WaitFor(`document.querySelector('#question-head .lead') && document.querySelector('#question-head .lead').textContent === 'Rivista dopo il tuo commento.'`)
	wantText(t, p, "#question-head .state", "riaperta")
	if n := evalString(p, `String(document.querySelectorAll('iframe').length)`); n != "0" {
		t.Errorf("the new version has no 01 or 02 but %s frames remain", n)
	}
	wantText(t, p, `.round-closed .card-ghost[data-ghost="crash"] .card-moved`, "riaperta nel round corrente")
	wantText(t, p, `.round-current .card[data-target="crash"] .card-chip`, "riaperta")
	wantText(t, p, `.round-current .card[data-target="crash"] .mark`, "B")
	if evalString(p, `document.querySelector('.option[data-option="journal"]').getAttribute('aria-pressed')`) != "true" || evalString(p, `String(document.querySelector('.option[data-option="journal"]').disabled)`) != "false" {
		t.Error("a reopened question must keep its surviving answer and take a new one")
	}
	p.Click(`.card[data-target=":overview"]`)
	if evalString(p, `document.querySelector('.decisions tbody tr').className`) != "struck" {
		t.Error("the reopened question's decisions row is not struck through")
	}
	p.Click(`.card[data-target="crash"]`)
	p.Click(`.option[data-option="atomic"]`)
	p.Click("#send")
	if got := run.outcome(); jsonOf(got["questions"]) != `{"crash":{"choice":"atomic"},"format":{},"retention":{"choice":"week"}}` && jsonOf(got["questions"]) != `{"crash":{"choice":"atomic"},"retention":{"choice":"week"}}` {
		t.Fatalf("batch after reopening %s", jsonOf(got))
	}
}

const boundaryRound = `# Prima domanda {id="one"}
## Capire
<p id="mark">uno</p>
## Decidere
- [a] Alfa
- [b] Beta

# Seconda domanda {id="two"}
## Capire
<div id="tall">due</div>
## Decidere
- [c] Gamma
- [d] Delta
`

func TestFrameLearnsOnlyTheOptionAndOnlyTheActiveFrameSpeaks(t *testing.T) {
	run := newPhaseRun(t)
	run.call(boundaryRound, map[string]string{
		"one/spy.js":   `window.got = []; addEventListener('message', e => got.push(e.data));`,
		"two/tall.css": `#tall { height: 1500px; }`,
	})
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	f.WaitFor(`window.got && window.got.length > 0`)
	token := evalString(p, `view.token`)
	frameURL := frameString(f, `location.href`)
	if strings.Contains(frameURL, token) || strings.Contains(frameURL, strings.Split(run.url, "/")[4]) {
		t.Errorf("frame URL %s carries the round token or the capability", frameURL)
	}

	p.Type("#free-text", "SEGRETO-LIBERO")
	p.Type("#composer", "SEGRETO-MESSAGGIO")
	p.Click("#stage-message")
	p.Click("#composer")
	png, err := base64.StdEncoding.DecodeString(pixel)
	if err != nil {
		t.Fatal(err)
	}
	p.Paste("image/png", png)
	p.WaitFor(`document.querySelector('#thread img')`)
	dropped := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(dropped, png, 0o600); err != nil {
		t.Fatal(err)
	}
	p.Drop("#thread", []string{dropped})
	p.WaitFor(`document.querySelectorAll('#thread .message-staged img').length === 2`)
	wantText(t, p, "#summary", "4 in bozza · Q1 → ✎ · Q1 +1 msg · Q1 +2 screenshot")
	p.Click(`.option[data-option="b"]`)
	f.WaitFor(`document.documentElement.dataset.option === 'b'`)
	var got []map[string]any
	f.MustEval(`got`, &got)
	if len(got) < 3 {
		t.Fatalf("the frame received %v, want an option message per change", got)
	}
	for _, m := range got {
		if len(m) != 3 || m["lavagna"] != "option" || !strings.Contains(`{"free":true,"lavagna":"option","option":null} {"free":false,"lavagna":"option","option":null} {"free":false,"lavagna":"option","option":"b"}`, jsonOf(m)) {
			t.Errorf("the frame received %s", jsonOf(m))
		}
	}
	if attrs := frameString(f, `[...document.documentElement.attributes].map(a => a.name + '=' + a.value).join(' ')`); attrs != `lang=it class=frame data-free=false data-option=b data-variant=b` {
		t.Errorf("frame <html> attributes %q", attrs)
	}

	// cdptest follows one frame per page: mark the active one before another
	// window attaches.
	f.MustEval(`window.kept = true`, nil)
	height := evalString(p, `document.querySelector('#content').style.height`)
	p.MustEval(`window.forged = {self: 0, other: 0}; addEventListener('message', e => {
		if (e.data && e.data.lavagna !== 'layout') return;
		if (e.source === window) forged.self++;
		const other = document.querySelector('#other');
		if (other && e.source === other.contentWindow) forged.other++;
	})`, nil)
	p.MustEval(`window.postMessage({lavagna: 'layout', height: 7}, '*')`, nil)
	p.MustEval(`(() => { const other = document.createElement('iframe'); other.id = 'other'; other.setAttribute('sandbox', 'allow-scripts'); other.src = view.phase.questions[1].frame; document.body.append(other); })()`, nil)
	p.WaitFor(`forged.self > 0 && forged.other > 0`)
	time.Sleep(200 * time.Millisecond)
	if now := evalString(p, `document.querySelector('#content').style.height`); now != height {
		t.Errorf("a message from another window resized the active frame: %s, was %s", now, height)
	}
	p.MustEval(`document.querySelector('#other').remove()`, nil)

	p.Click(`.card[data-target="two"]`)
	p.WaitFor(`document.querySelector('#content') && document.querySelector('#content').src.includes('/two/')`)
	p.WaitFor(`parseInt(document.querySelector('#content').style.height) > 1000`)
	p.Click(`.card[data-target="one"]`)
	p.WaitFor(`document.querySelector('#content').src.includes('/one/')`)
	f = p.Frame("#content")
	f.WaitFor(`document.documentElement.dataset.option === 'b'`)
	if frameString(f, `String(window.kept)`) != "undefined" || evalString(p, `String(document.querySelectorAll('iframe').length)`) != "1" {
		t.Error("returning to a question must recreate its frame, the only one")
	}
}

func TestSendFreezesTheDraftOnlyWhileAwaitingItsReply(t *testing.T) {
	run := newPhaseRun(t)
	run.call(crashRound, crashFiles())
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)

	p.Click("#composer")
	p.Insert(strings.Repeat("x", 25000))
	wantText(t, p, "#counter", "24,4 KB di 32,0 KB disponibili per il testo")
	p.Insert(strings.Repeat("x", 8000))
	if evalString(p, `document.querySelector('#counter').className + ' ' + document.querySelector('#send').disabled`) != "counter over true" {
		t.Error("text over 32 KiB must block Send")
	}
	p.MustEval(`(() => { const c = document.querySelector('#composer'); c.value = 'Breve'; c.dispatchEvent(new Event('input')); })()`, nil)
	if evalString(p, `String(document.querySelector('#counter').hidden)`) != "true" {
		t.Error("the counter stays visible below 75 %")
	}

	p.Click(`.option[data-option="journal"]`)
	p.Hold("*/send")
	p.Click("#send")
	deadline := time.Now().Add(5 * time.Second)
	for len(p.Held()) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(p.Held()) == 0 {
		t.Fatal("Send made no request")
	}
	frozen := evalString(p, `[document.querySelector('#turn').textContent, document.querySelector('#composer').readOnly, document.querySelector('.option[data-option="atomic"]').disabled, document.querySelector('#free-text').readOnly, document.querySelector('#send').disabled].join()`)
	if frozen != "Invio in corso…,true,true,true,true" {
		t.Errorf("while Send awaits its reply: %s", frozen)
	}
	p.Release(p.Held()[0])
	run.outcome()
	p.WaitFor(`document.querySelector('#turn').textContent === 'Consegnato al terminale'`)
	after := evalString(p, `[document.querySelector('#composer').readOnly, document.querySelector('.option[data-option="atomic"]').disabled, document.querySelector('#send').disabled].join()`)
	if after != "false,false,true" {
		t.Errorf("after the reply the draft must stay editable and Send disabled until the next call: %s", after)
	}
	p.Click(`.option[data-option="atomic"]`)
	wantText(t, p, "#summary", "Consegnato al terminale · stato in tempo reale non disponibile · 1 in bozza")
	run.call("::: reply crash\nVisto.\n:::\n", nil)
	p.WaitFor(`document.querySelector('#turn').textContent === 'Tocca a te'`)
	if evalString(p, `String(document.querySelector('#send').disabled)`) != "false" {
		t.Error("the next call did not enable Send for the staged choice")
	}
	wantText(t, p, "#summary", "1 in bozza · Q1 → A")
}

func TestDeliveryStagesShowInTitleBarAndFooter(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: conversation.Secret(32)}
	s := newRound(phaseSpec(t, o, crashRound, nil))
	hs := &http.Server{Handler: s.handler()}
	go hs.Serve(ln)
	t.Cleanup(func() {
		s.stop()
		hs.Close()
	})
	p := cdptest.Start(t).Open(o.URL(), 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	stage := func() string {
		return evalString(p, `document.querySelector('#turn').textContent + ' | ' + document.querySelector('#summary').textContent`)
	}
	waitStage := func(want string) {
		t.Helper()
		p.WaitFor(`document.querySelector('#turn').textContent + ' | ' + document.querySelector('#summary').textContent === ` + jsString(want))
	}
	if got := stage(); got != "Tocca a te | Niente in bozza" {
		t.Errorf("before a send: %s", got)
	}
	p.Click(`.option[data-option="journal"]`)
	p.Click("#send")
	<-s.accepted
	waitStage("Inviato | Ricevuto da lavagna · non ancora consegnato all’agente")
	p.Type("#composer", "Ancora una cosa")
	waitStage("Inviato | Ricevuto da lavagna · non ancora consegnato all’agente · 1 in bozza")
	s.returned(true)
	waitStage("Consegnato al terminale | Consegnato al terminale · 1 in bozza")
	s.advance(received)
	waitStage("L’agente lavora | Letto dall’agente · l’agente lavora · 1 in bozza")
	s.advance(ended)
	waitStage("Grilling ancora aperto | L’agente ha terminato il turno prima di chiudere la frontiera. Il tuo feedback è conservato. · 1 in bozza")

	p.Click(`.card[data-target="retention"]`)
	reload(p)
	if strings.Contains(evalString(p, `document.querySelector('.card[data-target="retention"]').className`), "unseen") {
		t.Error("a reload lost the seen mark")
	}
	if evalString(p, `String(document.querySelector('.card[data-target="retention"]').getAttribute('aria-current'))`) != "true" {
		t.Error("a reload lost the active question")
	}
	p.Click(`.card[data-target="crash"]`)
	if got := evalString(p, `document.querySelector('#composer').value`); got != "Ancora una cosa" {
		t.Errorf("a reload lost the composer draft: %q", got)
	}
}

const diagramRound = `# Cosa succede se una sessione crasha a metà scrittura? {id="crash"}
## Capire
::: sequence Salvataggio interrotto
Sessione A | a.json.tmp [atomic] | a.log [journal] | a.json [-journal]
Sessione A -> a.json: apre con O_TRUNC !1 [now none]
Sessione A -> a.json.tmp: scrive 9 KB e fsync [atomic]
Sessione A -> a.log: append record e checksum [journal]
:::
## Decidere
- [atomic] Scrittura atomica {recommended}
- [journal] Journal
- [none] Nessuna modifica
`

func TestDiagramsFollowTheVariantShown(t *testing.T) {
	run := newPhaseRun(t)
	run.call(diagramRound, nil)
	p := cdptest.Start(t).Open(run.url, 1280, 860)
	p.WaitFor(`document.querySelector('#content')`)
	f := p.Frame("#content")
	shown := `[...document.querySelectorAll('#confrontare .diagram-variant')].filter(d => !d.hidden).map(d => d.dataset.variant).join(' ')`
	capire := `[...document.querySelectorAll('#capire svg text')].map(t => t.textContent).join('|')`
	f.WaitFor(shown + ` === 'atomic'`)
	p.Click(`.option[data-option="journal"]`)
	f.WaitFor(shown + ` === 'journal'`)
	f.Click(`.preview .chip[data-variant="none"]`)
	f.WaitFor(shown + ` === 'none'`)
	f.Click(`.preview .chip[data-variant="none"]`)
	f.WaitFor(shown + ` === 'journal'`)
	if got := frameString(f, capire); !strings.Contains(got, "apre con O_TRUNC") || strings.Contains(got, "a.json.tmp") || strings.Contains(got, "a.log") {
		t.Errorf("01 must keep the present state: %s", got)
	}

	// The frame draws the labels as wide as the CLI measured them.
	b, err := fs.ReadFile(page.Assets, page.Font)
	if err != nil {
		t.Fatal(err)
	}
	font, err := diagram.ParseFont(b)
	if err != nil {
		t.Fatal(err)
	}
	var labels []struct {
		Text                string
		Size, Weight, Width float64
	}
	f.MustEval(`document.fonts.ready.then(() => [...document.querySelectorAll('#confrontare .diagram-variant:not([hidden]) text')]
		.filter(t => t.children.length === 0)
		.map(t => ({Text: t.textContent, Size: +t.getAttribute('font-size'), Weight: +t.getAttribute('font-weight'), Width: t.getComputedTextLength()})))`, &labels)
	if len(labels) < 5 {
		t.Fatalf("labels %v", labels)
	}
	for _, l := range labels {
		if want := font.Width(l.Text, l.Size, l.Weight); math.Abs(l.Width-want) > 0.016 {
			t.Errorf("%q at %v px, weight %v: Chrome %.4f, measured %.4f", l.Text, l.Size, l.Weight, l.Width, want)
		}
	}
}
