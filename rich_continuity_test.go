package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/taekwondodev/lavagna/internal/cdptest"
)

func TestInterruptedAnchoredFeedbackSurvivesReloadAndNewRound(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"round.md": prototypeRound, "prototipo.js": prototypeScript})
	environ := env(t, "LAVAGNA_SESSION=anchored-resend")
	first, _ := startBlocked(t, environ, "", dir)
	p := cdptest.Start(t).Open(first.url, 1280, 900)
	settled(t, p)
	p.WaitFor(controlled)
	p.Type("#comment-text", "Conserva il riferimento")
	p.Click("#pick-anchor")
	p.Frame("#content").Click("#piu")
	p.WaitFor(`document.querySelector('#anchor-label').textContent === 'Riferimento: UI · contatore'`)
	p.Click("#add-comment")
	p.Type("#comment-text", "Commento generale")
	p.Click("#send-feedback")
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + acceptedText + `'`)
	first.esc()
	turnIs(p, "Consegna non riuscita")
	p.Reload()
	roundShown(p, "1")
	p.WaitFor(`document.querySelector('#comments').textContent.includes('UI · contatore')`)

	second := startRound(t, environ, nextRound)
	roundShown(p, "2")
	turnIs(p, "Tocca a te")
	p.Click("#send-feedback")
	lines, code := second.finish()
	want := `,"choices":{},"comments":[{"anchor":"UI · contatore","text":"Conserva il riferimento"},{"anchor":null,"text":"Commento generale"}],"images":[]}`
	if code != 0 || len(lines) != 1 || !strings.HasSuffix(lines[0], want) {
		t.Fatalf("resend after a round with different anchors: exit %d, output %q", code, lines)
	}
}

func TestRichRoundSurvivesOfflineReloadAndReopen(t *testing.T) {
	var requests atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer remote.Close()
	dir := t.TempDir()
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, dir, map[string]string{
		"round.md":   "# Capire\n## Snapshot {ref=\"Snapshot\"}\nTesto ricco conservato.\n\n| Prima | Dopo |\n| --- | --- |\n| A | B |\n\n<img id=\"picture\" src=\"screen.png\" alt=\"Schermata\">\n\n<button id=\"prototype\">Prova</button>\n\n# Decidere\n## Dove? {id=\"storage\"}\n- [file] File\n- [db] Database\n",
		"screen.png": string(image),
		"style.css":  "#prototype { color: rgb(12, 34, 56); }",
		"app.js":     "document.querySelector('#prototype').onclick = function () { this.textContent = 'Funziona'; };",
	})
	first := startDir(t, env(t, "LAVAGNA_SESSION=rich-offline"), dir)
	b := cdptest.Start(t)
	p := b.Open(first.url, 1280, 900)
	settled(t, p)
	p.WaitFor(controlled)
	p.Type("#comment-text", "Bozza accanto al contenuto")
	first.esc()
	unreachable(t, first.origin)

	check := func(p *cdptest.Page) {
		t.Helper()
		settled(t, p)
		p.WaitFor(`document.querySelector('#comment-text').value === 'Bozza accanto al contenuto'`)
		f := p.Frame("#content")
		f.WaitFor(`document.body.textContent.includes('Testo ricco conservato.') && document.querySelector('table').textContent.includes('Dopo')`)
		f.WaitFor(`getComputedStyle(document.querySelector('#prototype')).color === 'rgb(12, 34, 56)'`)
		f.WaitFor(`document.querySelector('#picture').naturalWidth === 1 && [...document.fonts].some(font => font.status === 'loaded')`)
		var isolated bool
		f.MustEval(`(() => { try { void parent.document; return false; } catch { return true; } })()`, &isolated)
		if !isolated {
			t.Fatal("the offline frame can read its parent")
		}
		var blocked bool
		f.MustEval(fmt.Sprintf(`fetch(%q, {mode:'no-cors'}).then(() => false, () => true)`, remote.URL), &blocked)
		if !blocked || requests.Load() != 0 {
			t.Fatal("the offline frame can send HTTP requests")
		}
		f.Click("#prototype")
		f.WaitFor(`document.querySelector('#prototype').textContent === 'Funziona'`)
	}
	p.Reload()
	check(p)
	p.Close()
	check(b.Open(first.url, 1280, 900))
}
