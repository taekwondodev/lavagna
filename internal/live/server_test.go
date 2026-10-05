package live

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/page"
	"github.com/taekwondodev/lavagna/internal/round"
)

const anchoredRound = "# Capire\nIl problema. {ref=\"Problema\"}\n<img src=\"punto.png\" alt=\"\">\n\n# Decidere\n## Dove? {id=\"storage\"}\n- [file] File\n- [db] Database\n"

func serve(t *testing.T) (*server, func(body string) int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: "cap-a"}
	files := []round.File{{Name: "punto.png", Body: []byte("\x89PNG")}}
	r, errs := round.Parse(round.Input{Source: []byte(anchoredRound), Files: map[string]bool{"punto.png": true}, Budget: round.MaxBytes})
	if errs != nil {
		t.Fatal(errs)
	}
	s := newRound(o, "r1", "tok-1", r, files, nil)
	hs := &http.Server{Handler: s.handler()}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	post := func(body string) int {
		req, _ := http.NewRequest(http.MethodPost, o.URL()+"send", strings.NewReader(body))
		req.Header.Set("Origin", "http://"+o.Host())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	return s, post
}

func sendBatch(submission, comment string) string {
	c, _ := json.Marshal(comment)
	return fmt.Sprintf(`{"round":"r1","token":"tok-1","submission":%q,"choices":{"storage":"db"},"comments":[{"text":%s}]}`, submission, c)
}

func TestSendResolvesADuplicateOnce(t *testing.T) {
	s, post := serve(t)
	for _, step := range []struct {
		submission string
		status     int
	}{
		{"s-00000000000000aa", http.StatusAccepted},
		{"s-00000000000000aa", http.StatusOK},
		{"s-00000000000000bb", http.StatusConflict},
	} {
		if status := post(sendBatch(step.submission, "ok")); status != step.status {
			t.Fatalf("%s: status %d, want %d", step.submission, status, step.status)
		}
	}
	if got := <-s.accepted; got.submission != "s-00000000000000aa" {
		t.Fatalf("accepted %q", got.submission)
	}
	select {
	case extra := <-s.accepted:
		t.Fatalf("a second batch was delivered: %q", extra.submission)
	default:
	}
}

func TestSendBoundsCommentTextAsTheResultEncodesIt(t *testing.T) {
	_, post := serve(t)
	if status := post(sendBatch("s-00000000000000aa", strings.Repeat(`"`, 16385))); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("32770 encoded bytes: status %d, want 413", status)
	}
	if status := post(sendBatch("s-00000000000000aa", strings.Repeat(`"`, 16384))); status != http.StatusAccepted {
		t.Fatalf("32768 encoded bytes: status %d, want 202", status)
	}
}

func TestSendRefusesTrailingDataAndBlankComments(t *testing.T) {
	_, post := serve(t)
	for name, body := range map[string]string{
		"trailing data":    sendBatch("s-00000000000000aa", "ok") + " GARBAGE{",
		"trailing bracket": sendBatch("s-00000000000000aa", "ok") + "]} not json",
		"trailing object":  sendBatch("s-00000000000000aa", "ok") + "{}",
		"blank comment":    sendBatch("s-00000000000000aa", "   "),
	} {
		if status := post(body); status != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, status)
		}
	}
}

func TestSendKeepsTheResultLineInsideTheTail(t *testing.T) {
	_, post := serve(t)
	comments := strings.TrimSuffix(strings.Repeat(`{"text":"a"},`, 2000), ",")
	body := `{"round":"r1","token":"tok-1","submission":"s-00000000000000aa","comments":[` + comments + `]}`
	if status := post(body); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("2000 one-byte comments: status %d, want 413", status)
	}
}

func TestSendKeepsAnchorsPresentInTheRound(t *testing.T) {
	s, post := serve(t)
	batch := func(anchor string) string {
		return `{"round":"r1","token":"tok-1","submission":"s-00000000000000aa","comments":[{"text":"qui","anchor":` + anchor + `},{"text":"in generale","anchor":null}]}`
	}
	if status := post(batch(`"Inventato"`)); status != http.StatusBadRequest {
		t.Fatalf("an anchor absent from the round: status %d, want 400", status)
	}
	if status := post(batch(`"Problema"`)); status != http.StatusAccepted {
		t.Fatalf("an anchor of the round: status %d, want 202", status)
	}
	line, _ := json.Marshal((<-s.accepted).line())
	want := `{"lavagna":"feedback","round":"r1","submission":"s-00000000000000aa","choices":{},"comments":[{"anchor":"Problema","text":"qui"},{"anchor":null,"text":"in generale"}],"images":[]}`
	if string(line) != want {
		t.Fatalf("result %s\nwant %s", line, want)
	}
}

func TestFrameServesTheRoundUnderItsOwnKey(t *testing.T) {
	s, _ := serve(t)
	base := "http://" + s.origin.Host()
	get := func(path string) *http.Response {
		t.Helper()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	frameURL := s.view.Frame
	if !strings.HasPrefix(frameURL, "/f/") || strings.Contains(frameURL, s.origin.Cap) || strings.Contains(frameURL, s.view.Token) {
		t.Fatalf("frame URL %q must carry its own key, not the capability or the round token", frameURL)
	}
	wantCSP := "sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'"
	for path, want := range map[string]struct {
		status      int
		contentType string
		cors        string
	}{
		frameURL:                           {200, "text/html; charset=utf-8", ""},
		frameURL + "punto.png":             {200, "image/png", ""},
		frameURL + ".lavagna/frame.js":     {200, "text/javascript; charset=utf-8", ""},
		frameURL + ".lavagna/" + page.Font: {200, "font/ttf", "*"},
		frameURL + "round.md":              {404, "", ""},
		"/f/sbagliata/":                    {404, "", ""},
		"/f/sbagliata/punto.png":           {404, "", ""},
	} {
		resp := get(path)
		if resp.StatusCode != want.status {
			t.Errorf("%s: status %d, want %d", path, resp.StatusCode, want.status)
			continue
		}
		if want.status != 200 {
			continue
		}
		if got := resp.Header.Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("%s: CSP %q", path, got)
		}
		if got := resp.Header.Get("Content-Type"); got != want.contentType {
			t.Errorf("%s: content type %q, want %q", path, got, want.contentType)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != want.cors {
			t.Errorf("%s: Access-Control-Allow-Origin %q, want %q", path, got, want.cors)
		}
	}
	shell := get("/s/" + s.origin.Cap + "/")
	if csp := shell.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("shell CSP %q", csp)
	}
}
