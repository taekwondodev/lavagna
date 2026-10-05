package live

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	s := newRound(roundSpec{Origin: o, ID: "r1", Token: "tok-1", FrameKey: conversation.Secret(16), Round: r, Files: files, Images: t.TempDir()})
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

var screenshots = map[string]string{
	"png":  "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR",
	"jpeg": "\xff\xd8\xff\xe0\x00\x10JFIF\x00",
	"webp": "RIFF\x24\x00\x00\x00WEBPVP8 ",
	"gif":  "GIF89a\x01\x00\x01\x00",
}

func postImage(t *testing.T, s *server, body string, edits ...func(*http.Request)) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.origin.URL()+"images", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+s.origin.Host())
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Lavagna-Round", "r1")
	req.Header.Set("Lavagna-Token", "tok-1")
	for _, edit := range edits {
		edit(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Image string }
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out.Image
}

func imageBatch(submission string, ids ...string) string {
	b, _ := json.Marshal(ids)
	return fmt.Sprintf(`{"round":"r1","token":"tok-1","submission":%q,"images":%s}`, submission, b)
}

func TestUploadAcceptsScreenshotsByMagicBytes(t *testing.T) {
	s, _ := serve(t)
	for name, body := range screenshots {
		if status, id := postImage(t, s, body); status != http.StatusCreated || id == "" {
			t.Errorf("%s: status %d, image %q, want 201 with an image", name, status, id)
		}
	}
	for name, body := range map[string]string{
		"text":       "not an image",
		"svg":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"pdf":        "%PDF-1.7\n",
		"bmp":        "BM\x36\x00\x00\x00\x00\x00",
		"empty":      "",
		"remote URL": "https://example.com/shot.png",
		"host path":  "/Users/me/Desktop/shot.png",
	} {
		if status, _ := postImage(t, s, body); status != http.StatusUnsupportedMediaType {
			t.Errorf("%s: status %d, want 415", name, status)
		}
	}
}

func TestUploadRefusesImagesOverTenMiB(t *testing.T) {
	s, _ := serve(t)
	png := screenshots["png"]
	if status, _ := postImage(t, s, png+strings.Repeat("\x00", 10<<20-len(png))); status != http.StatusCreated {
		t.Fatalf("10 MiB: status %d, want 201", status)
	}
	if status, _ := postImage(t, s, png+strings.Repeat("\x00", 10<<20-len(png)+1)); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("10 MiB + 1 byte: status %d, want 413", status)
	}
}

func TestUploadRefusesUnauthorizedImages(t *testing.T) {
	s, post := serve(t)
	png := screenshots["png"]
	header := func(name, value string) func(*http.Request) {
		return func(r *http.Request) {
			if value == "" {
				r.Header.Del(name)
			} else {
				r.Header.Set(name, value)
			}
		}
	}
	cases := []struct {
		name   string
		edit   func(*http.Request)
		status int
	}{
		{"missing Origin", header("Origin", ""), http.StatusForbidden},
		{"foreign Origin", header("Origin", "http://evil.example"), http.StatusForbidden},
		{"simple content type", header("Content-Type", "text/plain"), http.StatusUnsupportedMediaType},
		{"missing token", header("Lavagna-Token", ""), http.StatusConflict},
		{"stale token", header("Lavagna-Token", "tok-0"), http.StatusConflict},
		{"stale round", header("Lavagna-Round", "r0"), http.StatusConflict},
		{"foreign capability", func(r *http.Request) { r.URL.Path = "/s/cap-b/images" }, http.StatusForbidden},
	}
	for _, c := range cases {
		if status, _ := postImage(t, s, png, c.edit); status != c.status {
			t.Errorf("%s: status %d, want %d", c.name, status, c.status)
		}
	}
	if status := post(sendBatch("s-00000000000000aa", "ok")); status != http.StatusAccepted {
		t.Fatalf("send: %d", status)
	}
	if status, _ := postImage(t, s, png); status != http.StatusConflict {
		t.Fatalf("after the batch was accepted: status %d, want 409", status)
	}
}

func TestSendReturnsAttachedImagesAsAbsolutePaths(t *testing.T) {
	s, post := serve(t)
	_, png := postImage(t, s, screenshots["png"])
	_, gif := postImage(t, s, screenshots["gif"])
	postImage(t, s, screenshots["jpeg"])
	if status := post(imageBatch("s-00000000000000aa", gif, png)); status != http.StatusAccepted {
		t.Fatalf("send: %d", status)
	}
	got := (<-s.accepted).line()
	if len(got.Images) != 2 {
		t.Fatalf("images %q, want two paths", got.Images)
	}
	for i, want := range []string{screenshots["gif"], screenshots["png"]} {
		b, err := os.ReadFile(got.Images[i])
		if !filepath.IsAbs(got.Images[i]) || err != nil || string(b) != want {
			t.Errorf("image %d at %q: %v, content %q, want an absolute path to %q", i, got.Images[i], err, b, want)
		}
	}
}

func TestUploadedImagesRemainValidAcrossRoundServers(t *testing.T) {
	s, _ := serve(t)
	_, id := postImage(t, s, screenshots["png"])
	next := newRound(roundSpec{Origin: s.origin, ID: "r2", Token: "tok-2", Round: s.view.Round, Images: s.imageDir})
	u, ok := next.uploads[id]
	if !ok {
		t.Fatalf("new server did not restore image reference %q", id)
	}
	b, err := os.ReadFile(u.path)
	if err != nil || string(b) != screenshots["png"] {
		t.Fatalf("restored image bytes %q, error %v", b, err)
	}
}

func TestImagesAreServedOnlyAsTheirSniffedType(t *testing.T) {
	s, _ := serve(t)
	_, id := postImage(t, s, screenshots["webp"])
	get := func(url string) (int, string, string) {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type") + " " + resp.Header.Get("X-Content-Type-Options"), string(b)
	}
	if status, headers, body := get(s.origin.URL() + "images/" + id); status != http.StatusOK || headers != "image/webp nosniff" || body != screenshots["webp"] {
		t.Errorf("own image: %d %q %q", status, headers, body)
	}
	if status, _, _ := get(s.origin.URL() + "images/0123456789abcdef0123456789abcdef"); status != http.StatusNotFound {
		t.Errorf("unknown image: %d, want 404", status)
	}
	foreign := strings.Replace(s.origin.URL(), "/cap-a/", "/cap-b/", 1)
	if status, _, _ := get(foreign + "images/" + id); status != http.StatusForbidden {
		t.Errorf("foreign capability: %d, want 403", status)
	}
}

func TestSendRefusesImagesOutsideTheBounds(t *testing.T) {
	s, post := serve(t)
	var ids []string
	for range 9 {
		_, id := postImage(t, s, screenshots["png"])
		ids = append(ids, id)
	}
	cases := []struct {
		name   string
		ids    []string
		status int
	}{
		{"nine images", ids, http.StatusRequestEntityTooLarge},
		{"unknown image", []string{"0123456789abcdef0123456789abcdef"}, http.StatusBadRequest},
		{"host path", []string{"/etc/hosts"}, http.StatusBadRequest},
		{"remote URL", []string{"https://example.com/shot.png"}, http.StatusBadRequest},
		{"same image twice", []string{ids[0], ids[0]}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if status := post(imageBatch("s-00000000000000aa", c.ids...)); status != c.status {
			t.Errorf("%s: status %d, want %d", c.name, status, c.status)
		}
	}
	if status := post(imageBatch("s-00000000000000aa", ids[:8]...)); status != http.StatusAccepted {
		t.Fatalf("eight images: status %d, want 202", status)
	}
}
