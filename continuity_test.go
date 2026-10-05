package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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
	uncertainText = "Consegna non riuscita: l’agente è stato interrotto. Il tuo invio è conservato, puoi reinviarlo."
	carriedText   = "Bozza ripresa dal round precedente: rivedila prima di inviare."
	detachedText  = "Questa scheda non è più collegata alla conversazione"
	controlled    = `navigator.serviceWorker.controller !== null`
)

const nextRound = `# Capire
Il secondo round chiarisce il primo.

# Decidere
## Dove salviamo lo stato? {id="storage"}
- [file] Un file per sessione
- [db] Un database locale
`

func turnIs(p *cdptest.Page, turn string) {
	p.WaitFor(`document.querySelector('#turn').textContent === '` + turn + `'`)
}

func roundShown(p *cdptest.Page, n string) {
	p.WaitFor(formShown + ` && document.querySelector('#round-label').textContent === 'Round ` + n + `'`)
	var framed bool
	p.MustEval(`Boolean(document.querySelector('#content'))`, &framed)
	if framed {
		p.Frame("#content").WaitFor(`document.readyState === 'complete' && document.fonts.status === 'loaded'`)
		p.WaitFor(`document.querySelector('#content').offsetHeight > 0`)
	}
}

func unreachable(t *testing.T, origin string) {
	t.Helper()
	if conn, err := net.Dial("tcp", strings.TrimPrefix(origin, "http://")); err == nil {
		conn.Close()
		t.Fatalf("something still listens on %s", origin)
	}
}

func browserStub(t *testing.T) (string, func() []string) {
	t.Helper()
	dir := t.TempDir()
	opened := filepath.Join(dir, "opened")
	script := filepath.Join(dir, "browser")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"$1\" >> "+opened+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return "BROWSER=" + script, func() []string {
		b, _ := os.ReadFile(opened)
		return strings.Fields(string(b))
	}
}

func startBlocked(t *testing.T, environ []string, src string, args ...string) (*call, *bufio.Reader) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := command(environ, src, args...)
	cmd.Stdout = w
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &call{t: t, cmd: cmd}
	t.Cleanup(func() { c.esc(); r.Close(); w.Close() })
	out := bufio.NewReader(r)
	status := bufio.NewReader(stderr)
	first, err := status.ReadString('\n')
	go io.Copy(io.Discard, status)
	c.status(first)
	if err != nil || c.url == "" {
		t.Fatalf("status line %q: %v", first, err)
	}
	fillPipeSoTheResultLineBlocks(w)
	return c, out
}

func fillPipeSoTheResultLineBlocks(w *os.File) {
	go w.Write(make([]byte, 1<<20))
	time.Sleep(200 * time.Millisecond)
}

func TestPageFollowsConsecutiveRoundsWithoutRefresh(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=consecutive")
	first := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.MustEval(`window.sameDocument = true`, nil)
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	if _, code := first.finish(); code != 0 {
		t.Fatalf("round 1 exit %d", code)
	}
	turnIs(p, "In attesa del prossimo round")

	second := startRound(t, environ, nextRound)
	if second.url != first.url {
		t.Fatalf("round 2 serves %s, want the recorded origin %s", second.url, first.url)
	}
	roundShown(p, "2")
	turnIs(p, "Tocca a te")
	var page struct {
		Same    bool
		Checked int
	}
	p.MustEval(`({Same: window.sameDocument === true, Checked: document.querySelectorAll('#document input:checked').length})`, &page)
	p.Frame("#content").WaitFor(`document.body.textContent.includes('Il secondo round chiarisce il primo.')`)
	if !page.Same || page.Checked != 0 {
		t.Fatalf("round 2 in the same tab: %+v", page)
	}
	p.Click(`input[value="file"]`)
	p.Click("#send-feedback")
	lines, code := second.finish()
	if code != 0 || !strings.HasPrefix(lines[0], `{"lavagna":"feedback","round":"r2"`) || !strings.HasSuffix(lines[0], feedbackTail("file")) {
		t.Fatalf("round 2: exit %d, output %q", code, lines)
	}
}

func TestPageReloadBetweenRoundsUsesThePrecache(t *testing.T) {
	browser, opened := browserStub(t)
	environ := env(t, "LAVAGNA_SESSION=reload", browser)
	first := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.WaitFor(controlled)
	p.Click(`input[value="db"]`)
	p.Type("#comment-text", "Inviato prima della pausa")
	p.Click("#send-feedback")
	if _, code := first.finish(); code != 0 {
		t.Fatalf("round 1 exit %d", code)
	}
	turnIs(p, "In attesa del prossimo round")
	unreachable(t, first.origin)

	p.Reload()
	roundShown(p, "1")
	turnIs(p, "In attesa del prossimo round")
	var restored struct {
		Sent, Delivery string
		SendHidden     bool
	}
	p.MustEval(`({Sent: document.querySelector('#sent-area').innerText, Delivery: document.querySelector('#delivery').textContent, SendHidden: document.querySelector('#send-feedback').hidden})`, &restored)
	if !strings.Contains(restored.Sent, "Un database locale") || !strings.Contains(restored.Sent, "Inviato prima della pausa") || restored.Delivery != blindText || !restored.SendHidden {
		t.Fatalf("reloaded with nobody on the port: %+v", restored)
	}

	second := startRound(t, environ, nextRound)
	roundShown(p, "2")
	turnIs(p, "Tocca a te")
	var draft struct {
		Notes  int
		Editor string
	}
	p.MustEval(`({Notes: document.querySelectorAll('#comments li').length, Editor: document.querySelector('#comment-text').value})`, &draft)
	if draft.Notes != 0 || draft.Editor != "" {
		t.Fatalf("the returned batch came back as a draft in round 2: %+v", draft)
	}
	time.Sleep(2500 * time.Millisecond)
	if got := opened(); len(got) != 1 || got[0] != first.url {
		t.Fatalf("tabs opened %q, want only round 1's: the reloaded tab must reconnect before lavagna opens another", got)
	}
	second.esc()
}

func TestPageEscKeepsTheDraftForTheNextRound(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=esc")
	first := startRound(t, environ, richRound)
	b := cdptest.Start(t)
	p := b.Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.WaitFor(controlled)
	p.Click(`input[value="file"]`)
	p.Type("#comment-text", "Scritto prima di Esc")
	p.Click("#add-comment")
	p.Type("#comment-text", "Ancora nell'editor")

	first.esc()
	turnIs(p, "Riconnessione a lavagna…")
	p.Close()
	unreachable(t, first.origin)

	reopened := b.Open(first.url, 1280, 900)
	roundShown(reopened, "1")
	var restored struct {
		Comments, Editor, Hint string
		Choice, SendDisabled   bool
	}
	facts := `({Comments: document.querySelector('#comments').textContent, Editor: document.querySelector('#comment-text').value, Hint: document.querySelector('#send-hint').textContent, Choice: document.querySelector('input[value="file"]').checked, SendDisabled: document.querySelector('#send-feedback').disabled})`
	reopened.MustEval(facts, &restored)
	if !strings.Contains(restored.Comments, "Scritto prima di Esc") || restored.Editor != "Ancora nell'editor" || !restored.Choice ||
		!restored.SendDisabled || restored.Hint != "Lavagna non è collegata: il feedback resta in bozza." {
		t.Fatalf("reopened tab with nobody on the port: %+v", restored)
	}

	second := startRound(t, environ, nextRound)
	if second.url != first.url {
		t.Fatalf("the call after Esc serves %s, want the recorded origin %s", second.url, first.url)
	}
	roundShown(reopened, "2")
	turnIs(reopened, "Tocca a te")
	var carried struct {
		Comments, Editor, Status string
		Choice                   bool
	}
	reopened.MustEval(`({Comments: document.querySelector('#comments').textContent, Editor: document.querySelector('#comment-text').value, Status: document.querySelector('#editor-status').textContent, Choice: document.querySelector('input[value="file"]').checked})`, &carried)
	if !strings.Contains(carried.Comments, "Scritto prima di Esc") || carried.Editor != "Ancora nell'editor" || !carried.Choice || carried.Status != carriedText {
		t.Fatalf("round 2 after Esc: %+v", carried)
	}
	reopened.Click("#send-feedback")
	lines, code := second.finish()
	if code != 0 || !strings.HasSuffix(lines[0], feedbackTail("file", "Scritto prima di Esc", "Ancora nell'editor")) {
		t.Fatalf("round 2: exit %d, output %q", code, lines)
	}
}

func TestPageShowsUncertainWhenTheCallDiesBeforeReturning(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=uncertain")
	first, _ := startBlocked(t, environ, richRound)

	p := cdptest.Start(t).Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.Click(`input[value="db"]`)
	p.Type("#comment-text", "Da non perdere")
	p.WaitFor(controlled)
	png := screenshot(t)
	p.Drop("#comment-text", []string{writeFile(t, "interrupted.png", png)})
	p.WaitFor(shots + ` === 1 && document.querySelector('#images img').naturalWidth > 0`)
	p.Click("#send-feedback")
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + acceptedText + `'`)
	time.Sleep(500 * time.Millisecond)
	if got := text(p, "#delivery"); got != acceptedText {
		t.Fatalf("while the result line cannot be written the page says %q, want %q", got, acceptedText)
	}

	first.esc()
	turnIs(p, "Consegna non riuscita")
	var kept struct {
		Delivery, Comments string
		Choice             bool
	}
	keptFacts := `({Delivery: document.querySelector('#delivery').textContent, Comments: document.querySelector('#comments').textContent, Choice: document.querySelector('input[value="db"]').checked})`
	p.MustEval(keptFacts, &kept)
	if kept.Delivery != uncertainText || !strings.Contains(kept.Comments, "Da non perdere") || !kept.Choice {
		t.Fatalf("after Esc between Accepted and Returned: %+v", kept)
	}

	p.Reload()
	roundShown(p, "1")
	p.WaitFor(shots + ` === 1 && document.querySelector('#images img').naturalWidth > 0`)
	second := startRound(t, environ, nextRound)
	if second.url != first.url {
		t.Fatalf("the call after Esc serves %s, want the recorded origin %s", second.url, first.url)
	}
	roundShown(p, "2")
	p.MustEval(keptFacts, &kept)
	if kept.Delivery != uncertainText || !strings.Contains(kept.Comments, "Da non perdere") || !kept.Choice {
		t.Fatalf("round 2 offers the uncertain batch for resend: %+v", kept)
	}
	p.WaitFor(shots + ` === 1 && !document.querySelector('#send-feedback').disabled`)
	p.Click("#send-feedback")
	lines, code := second.finish()
	if code != 0 || len(lines) != 1 {
		t.Fatalf("resend: exit %d, output %q", code, lines)
	}
	var feedback struct {
		Round    string
		Choices  map[string]string
		Comments []struct{ Text string }
	}
	if err := json.Unmarshal([]byte(lines[0]), &feedback); err != nil || feedback.Round != "r2" || feedback.Choices["storage"] != "db" || len(feedback.Comments) != 1 || feedback.Comments[0].Text != "Da non perdere" {
		t.Fatalf("resent feedback %s: %v", lines[0], err)
	}
	paths := images(t, lines[0])
	if len(paths) != 1 {
		t.Fatalf("resent images %q, want one", paths)
	}
	got, err := os.ReadFile(paths[0])
	if err != nil || !bytes.Equal(got, png) {
		t.Fatalf("resent image at %q has bytes % x, error %v", paths[0], got[:min(8, len(got))], err)
	}
}

func TestPortTakenByAnotherProcessMintsAFreshOrigin(t *testing.T) {
	browser, opened := browserStub(t)
	environ := env(t, "LAVAGNA_SESSION=squatted", browser)
	first := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.Type("#comment-text", "Bozza da copiare")
	detach(t, first)

	turnIs(p, "Scheda non collegata")
	var old struct {
		Delivery, Editor string
		SendDisabled     bool
	}
	p.MustEval(`({Delivery: document.querySelector('#delivery').textContent, Editor: document.querySelector('#comment-text').value, SendDisabled: document.querySelector('#send-feedback').disabled})`, &old)
	if old.Delivery != detachedText || old.Editor != "Bozza da copiare" || !old.SendDisabled {
		t.Fatalf("old tab with a foreign server on its port: %+v", old)
	}

	second := startRound(t, environ, nextRound)
	if second.origin == first.origin || second.url == first.url {
		t.Fatalf("the call reused %s while another process holds it", second.url)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(strings.Join(opened(), " "), second.url) {
		if time.Now().After(deadline) {
			t.Fatalf("no new tab opened at %s; opened %q", second.url, opened())
		}
		time.Sleep(50 * time.Millisecond)
	}
	round, token := second.view()
	if status := second.post(second.url+"send", second.origin, "application/json", batch(round, token, "s-0123456789abcdef")); status != http.StatusAccepted {
		t.Fatalf("send on the fresh origin: %d", status)
	}
	if _, code := second.finish(); code != 0 {
		t.Fatalf("round 2 exit %d", code)
	}

	third := startRound(t, environ, nextRound)
	if third.url != second.url {
		t.Fatalf("round 3 serves %s, want the persisted fresh origin %s", third.url, second.url)
	}
	third.esc()
	if got := text(p, "#turn"); got != "Scheda non collegata" {
		t.Fatalf("the old tab now says %q", got)
	}
}

func TestPageReopenedAfterReturnedDoesNotClaimUncertain(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=reopened")
	first, out := startBlocked(t, environ, richRound)
	b := cdptest.Start(t)
	p := b.Open(first.url, 1280, 900)
	roundShown(p, "1")
	p.WaitFor(controlled)
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + acceptedText + `'`)
	p.Close()

	go io.Copy(io.Discard, out)
	if err := first.cmd.Wait(); err != nil {
		t.Fatalf("the call did not return the batch: %v", err)
	}
	reopened := b.Open(first.url, 1280, 900)
	roundShown(reopened, "1")
	time.Sleep(time.Second)
	var tab struct{ Turn, Delivery string }
	reopened.MustEval(`({Turn: document.querySelector('#turn').textContent, Delivery: document.querySelector('#delivery').textContent})`, &tab)
	if tab.Turn != "Riconnessione a lavagna…" || tab.Delivery != "" {
		t.Fatalf("a tab that missed how its batch ended claims %+v, want no delivery state", tab)
	}

	second := startRound(t, environ, nextRound)
	roundShown(reopened, "2")
	var next struct {
		Notes            int
		Status, Delivery string
	}
	reopened.MustEval(`({Notes: document.querySelectorAll('#comments li').length, Status: document.querySelector('#editor-status').textContent, Delivery: document.querySelector('#delivery').textContent})`, &next)
	if next.Notes != 0 || next.Status != "" || next.Delivery != "" {
		t.Fatalf("round 2 after a returned batch: %+v", next)
	}
	second.esc()
}

func TestPageTabsDoNotCarryADeliveredBatch(t *testing.T) {
	// This owns cross-tab draft delivery, not rich-frame layout.
	const choices = `# Decidere
## Dove salviamo lo stato? {id="storage"}
- [file] Un file per sessione
- [db] Un database locale
`
	environ := env(t, "LAVAGNA_SESSION=tabs")
	first := startRound(t, environ, choices)
	b := cdptest.Start(t)
	one := b.Open(first.url, 1280, 900)
	roundShown(one, "1")
	other := b.Open(first.url, 1280, 900)
	roundShown(other, "1")
	one.Click(`input[value="db"]`)
	one.Type("#comment-text", "Già consegnato")
	other.WaitFor(`document.querySelector('#comment-text').value === 'Già consegnato' && document.querySelector('input[value="db"]').checked`)
	one.Click("#send-feedback")
	if _, code := first.finish(); code != 0 {
		t.Fatalf("round 1 exit %d", code)
	}
	for _, p := range []*cdptest.Page{one, other} {
		turnIs(p, "In attesa del prossimo round")
	}
	time.Sleep(time.Second)

	second := startRound(t, environ, choices)
	for _, p := range []*cdptest.Page{one, other} {
		roundShown(p, "2")
		var next struct {
			Notes          int
			Editor, Status string
		}
		p.MustEval(`({Notes: document.querySelectorAll('#comments li').length, Editor: document.querySelector('#comment-text').value, Status: document.querySelector('#editor-status').textContent})`, &next)
		if next.Notes != 0 || next.Editor != "" || next.Status != "" {
			t.Fatalf("round 2 brought back the delivered batch: %+v", next)
		}
	}
	second.esc()
}
