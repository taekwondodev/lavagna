package live

import (
	"encoding/base64"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/page"
	"github.com/taekwondodev/lavagna/internal/round"
)

type hit struct {
	method, path, origin, site string
	status                     int
}

type recorder struct {
	mu   sync.Mutex
	hits []hit
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() { w.ResponseWriter.(http.Flusher).Flush() }

func (rec *recorder) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		rec.mu.Lock()
		rec.hits = append(rec.hits, hit{r.Method, r.URL.Path, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"), sw.status})
		rec.mu.Unlock()
	})
}

func (rec *recorder) to(path string) []hit {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var out []hit
	for _, h := range rec.hits {
		if strings.HasPrefix(h.path, path) {
			out = append(out, h)
		}
	}
	return out
}

const isolationRound = `# Procediamo? {id="go"}
## Capire
Il contenuto prova a uscire dal frame.
<p id="styled">Stile del round</p>
<img id="self" src="go/punto.png" alt="">
<img id="data" src="data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7" alt="">
<script>window.inline = true;</script>
<p id="senza">Senza CORS</p>
<p id="con">Con CORS</p>

## Decidere
- [yes] Sì
- [no] No
`

const isolationStyle = `#styled { color: rgb(1, 2, 3); }
@font-face { font-family: "Senza CORS"; src: url(font.png); }
@font-face { font-family: "Con CORS"; src: url(.lavagna/fonts/AtkinsonHyperlegibleNext.ttf); }
#senza { font-family: "Senza CORS"; }
#con { font-family: "Con CORS"; }
`

const isolationProbe = `'use strict';
window.probe = { done: false };
const results = window.probe;
const attempt = (name, fn) => {
  try {
    const value = fn();
    results[name] = value === undefined ? 'ok' : String(value);
  } catch (error) {
    results[name] = error.name;
  }
};
const loaded = (img) => img.decode().then(() => 'loaded', () => 'blocked');
(async () => {
  attempt('parentDocument', () => parent.document.title);
  attempt('parentWindow', () => parent.view);
  attempt('topLocation', () => top.location.href);
  attempt('topNavigate', () => { top.location.href = OTHER + '/top'; });
  attempt('open', () => window.open(SEND));
  attempt('localStorage', () => localStorage.length);
  attempt('cookie', () => document.cookie);
  results.inline = String(window.inline === true);
  try {
    await fetch(SEND, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    results.fetch = 'sent';
  } catch (error) {
    results.fetch = error.name;
  }
  attempt('beacon', () => navigator.sendBeacon(SEND, '{}'));
  try {
    const socket = new WebSocket(SEND.replace('http:', 'ws:'));
    results.socket = await new Promise(done => { socket.onopen = () => done('open'); socket.onerror = () => done('error'); });
  } catch (error) {
    results.socket = error.name;
  }
  attempt('form', () => {
    const form = document.createElement('form');
    form.method = 'post';
    form.action = SEND;
    document.body.append(form);
    form.submit();
  });
  const other = new Image();
  other.src = OTHER + '/pixel.png';
  results.otherImage = await loaded(other);
  const send = new Image();
  send.src = SEND;
  results.sendImage = await loaded(send);
  results.style = getComputedStyle(document.querySelector('#styled')).color;
  results.selfImage = await loaded(document.querySelector('#self'));
  results.dataImage = await loaded(document.querySelector('#data'));
  document.body.getBoundingClientRect();
  for (const family of ['Senza CORS', 'Con CORS']) {
    const face = [...document.fonts].find(f => f.family.replace(/"/g, '') === family);
    if (face) await face.load().catch(() => {});
    results[family] = face ? face.status : 'missing';
  }
  results.done = true;
})();
`

const pixel = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

func TestFrameEscapesStayBlocked(t *testing.T) {
	other := &recorder{}
	otherServer := httptest.NewServer(other.wrap(http.NotFoundHandler()))
	defer otherServer.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: conversation.Secret(32)}
	send := o.URL() + "send"
	font, err := fs.ReadFile(page.Assets, page.Font)
	if err != nil {
		t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(pixel)
	if err != nil {
		t.Fatal(err)
	}
	probe := "const SEND = '" + send + "';\nconst OTHER = '" + otherServer.URL + "';\n" + isolationProbe
	files := []round.File{{Name: "go/probe.js", Body: []byte(probe)}, {Name: "go/probe.css", Body: []byte(isolationStyle)}, {Name: "go/punto.png", Body: png}, {Name: "go/font.png", Body: font}}
	s := newRound(phaseSpec(t, o, isolationRound, files))
	rec := &recorder{}
	hs := &http.Server{Handler: rec.wrap(s.handler())}
	go hs.Serve(ln)
	defer func() {
		s.stop()
		hs.Close()
	}()

	p := cdptest.Start(t).Open(o.URL(), 1280, 900)
	p.WaitFor(`document.querySelector('#content')`)
	p.MustEval(`window.probeMessages = []; addEventListener('message', e => { if (e.data && e.data.probe) probeMessages.push({Origin: e.origin, Source: e.source === document.querySelector('#content').contentWindow}); })`, nil)
	f := p.Frame("#content")
	f.WaitFor(`window.probe && window.probe.done`)
	var got map[string]any
	f.MustEval(`window.probe`, &got)
	time.Sleep(300 * time.Millisecond)

	var frameURL string
	f.MustEval(`location.href`, &frameURL)
	if strings.Contains(frameURL, o.Cap) || strings.Contains(frameURL, s.view.Token) || !strings.Contains(frameURL, "/go/") {
		t.Errorf("the frame URL %q carries the capability or the round token", frameURL)
	}

	rows := []struct {
		attempt, want string
	}{
		{"parentDocument", "SecurityError"},
		{"parentWindow", "SecurityError"},
		{"topLocation", "SecurityError"},
		{"topNavigate", "SecurityError"},
		{"open", "null"},
		{"localStorage", "SecurityError"},
		{"cookie", "SecurityError"},
		{"inline", "false"},
		{"fetch", "TypeError"},
		{"otherImage", "blocked"},
		{"sendImage", "blocked"},
		{"style", "rgb(1, 2, 3)"},
		{"selfImage", "loaded"},
		{"dataImage", "loaded"},
		{"Senza CORS", "error"},
		{"Con CORS", "loaded"},
	}
	for _, row := range rows {
		if got[row.attempt] != row.want {
			t.Errorf("%s: %v, want %s", row.attempt, got[row.attempt], row.want)
		}
	}

	sendPath := "/s/" + o.Cap + "/send"
	hits := rec.to(sendPath)
	if len(hits) != 1 || hits[0] != (hit{"GET", sendPath, "", "cross-site", http.StatusMethodNotAllowed}) {
		t.Errorf("requests to the send endpoint %+v, want only the image GET, cross-site without Origin, refused with 405", hits)
	}
	if len(other.to("/")) != 0 {
		t.Errorf("another loopback port received %+v", other.to("/"))
	}
	var top string
	p.MustEval(`location.href`, &top)
	if top != o.URL() {
		t.Errorf("the page is at %s, want %s", top, o.URL())
	}
	select {
	case b := <-s.accepted:
		t.Fatalf("content delivered a batch: %+v", b)
	default:
	}

	f.MustEval(`parent.postMessage({probe: 'ciao'}, '*')`, nil)
	p.WaitFor(`probeMessages.length === 1`)
	var seen []struct {
		Origin string
		Source bool
	}
	p.MustEval(`probeMessages`, &seen)
	if seen[0].Origin != "null" || !seen[0].Source {
		t.Errorf("postMessage from the frame: %+v, want origin \"null\" from the frame's window", seen[0])
	}

	p.Click(`.option[data-option="yes"]`)
	p.Click("#send")
	select {
	case b := <-s.accepted:
		if b.questions["go"].Choice != "yes" {
			t.Fatalf("shell batch %+v", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the shell's send with the token was not accepted")
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.to(sendPath)) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if posts := rec.to(sendPath); len(posts) != 2 || posts[1].method != "POST" || posts[1].origin != "http://"+o.Host() || posts[1].status != http.StatusAccepted {
		t.Errorf("shell send %+v", posts)
	}

	f.MustEval(`location.href = '`+otherServer.URL+`/navigated'`, nil)
	time.Sleep(500 * time.Millisecond)
	if len(other.to("/")) != 0 {
		t.Errorf("the frame navigated itself to another origin: %+v", other.to("/"))
	}
}
