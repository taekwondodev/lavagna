package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/page"
	"github.com/taekwondodev/lavagna/internal/round"
)

// storageRound is one open question, storage, whose 01 shows its own resource.
const storageRound = "# Dove? {id=\"storage\"}\n## Capire\n<img src=\"storage/punto.png\" alt=\"\">\n## Decidere\n- [file] File\n- [db] Database\n"

func serve(t *testing.T) (*server, func(body string) int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: "cap-a"}
	s := newRound(phaseSpec(t, o, storageRound, []round.File{{Name: "storage/punto.png", Body: []byte("\x89PNG")}}))
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
	return fmt.Sprintf(`{"round":"r1","token":"tok-1","submission":%q,"questions":{"storage":{"choice":"db","messages":[%s]}}}`, submission, c)
}

func TestQuestionFramesServeOnlyTheirOwnResources(t *testing.T) {
	o := conversation.Origin{Port: 43210, Cap: "cap-a"}
	ledger := conversation.Ledger{Round: 1, Order: []string{"one", "two"}, Questions: map[string]conversation.Question{
		"one": {Status: conversation.Open, Round: 1, Version: 1}, "two": {Status: conversation.Open, Round: 1, Version: 1},
	}}
	s := newRound(roundSpec{Origin: o, ID: "r1", Token: "tok-1", FrameKey: "frame-key-", Images: t.TempDir(), Ledger: ledger, Questions: []round.PhaseQuestion{
		{ID: "one", HTML: `<p>one</p>`, Resources: []round.File{{Name: "screen.js", Body: []byte("one-script")}}},
		{ID: "two", HTML: `<p>two</p>`, Resources: []round.File{{Name: "screen.js", Body: []byte("two-script")}}},
	}})
	request := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = o.Host()
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	for _, q := range s.view.Phase.Questions {
		root := request(q.Frame)
		if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "<p>"+q.ID+"</p>") {
			t.Fatalf("frame %s: status %d, body %s", q.ID, root.Code, root.Body.String())
		}
		resource := request(q.Frame + "screen.js")
		if resource.Code != http.StatusOK || resource.Body.String() != q.ID+"-script" {
			t.Fatalf("resource %s: status %d, body %s", q.ID, resource.Code, resource.Body.String())
		}
	}
	wrong := request(s.view.Phase.Questions[0].Frame + "missing.js")
	if wrong.Code != http.StatusNotFound {
		t.Fatalf("missing resource status %d", wrong.Code)
	}
	other := request(s.view.Phase.Questions[0].Frame + "two/screen.js")
	if other.Code != http.StatusNotFound {
		t.Fatalf("cross-question resource status %d", other.Code)
	}
}

func TestPhaseFeedbackIsGroupedByQuestionAndOverview(t *testing.T) {
	ledger := conversation.Ledger{Round: 2, Questions: map[string]conversation.Question{
		"one":    {Status: conversation.Open, Round: 2},
		"two":    {Status: conversation.Open, Round: 2},
		"closed": {Status: conversation.Settled, Round: 1},
	}}
	s := newRound(roundSpec{Origin: conversation.Origin{Port: 43210, Cap: "cap-a"}, ID: "r2", Token: "tok-1", Ledger: ledger, Questions: []round.PhaseQuestion{
		{ID: "one", Options: []round.PhaseOption{{ID: "yes"}, {ID: "no"}}},
		{ID: "two", Options: []round.PhaseOption{{ID: "a"}, {ID: "b"}}},
		{ID: "closed", Options: []round.PhaseOption{{ID: "x"}, {ID: "y"}}},
	}})
	s.uploads["img-1"] = upload{path: "/tmp/one.png"}
	if _, status, problem := s.validate(sendBody{Round: "r1", Submission: "s-12345678", Questions: map[string]sendQuestion{"closed": {Choice: "x"}}}); status != http.StatusBadRequest || problem != "question is not open in this round" {
		t.Fatalf("choice on a question outside the current round: %d %s", status, problem)
	}
	if _, status, problem := s.validate(sendBody{Round: "r1", Submission: "s-12345678", Questions: map[string]sendQuestion{"closed": {Messages: []string{"later thought"}}}}); status != 0 {
		t.Fatalf("message on a closed-round question: %d %s", status, problem)
	}
	batch, status, problem := s.validate(sendBody{Round: "r1", Submission: "s-12345678", Questions: map[string]sendQuestion{"one": {Choice: "yes", Messages: []string{"question note"}, Images: []string{"img-1"}}, "two": {Answer: "free text"}}, Overview: sendQuestion{Messages: []string{"overall"}}})
	if status != 0 {
		t.Fatalf("status %d: %s", status, problem)
	}
	encoded, err := json.Marshal(batch.phaseLine())
	if err != nil {
		t.Fatal(err)
	}
	got := string(encoded)
	for _, want := range []string{`"questions"`, `"one"`, `"choice":"yes"`, `"messages":["question note"]`, `"images":["/tmp/one.png"]`, `"answer":"free text"`, `"overview":{"messages":["overall"]}`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if _, status, _ := s.validate(sendBody{Questions: map[string]sendQuestion{"one": {Choice: "yes", Answer: "no"}}}); status != http.StatusBadRequest {
		t.Fatalf("choice and answer status %d", status)
	}
}

func TestCloseAcknowledgementRequiresOriginAndUnpredictableToken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	o := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: "cap-a"}
	s := newClose(o)
	hs := &http.Server{Handler: s.handler()}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	post := func(origin, token string) int {
		body, _ := json.Marshal(map[string]string{"token": token})
		req, _ := http.NewRequest(http.MethodPost, o.URL()+"close-ack", strings.NewReader(string(body)))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if got := post("http://"+o.Host(), "wrong"); got != http.StatusForbidden {
		t.Fatalf("wrong token status %d", got)
	}
	if got := post("http://attacker.invalid", s.closeToken); got != http.StatusForbidden {
		t.Fatalf("wrong origin status %d", got)
	}
	if got := post("http://"+o.Host(), s.closeToken); got != http.StatusNoContent {
		t.Fatalf("valid acknowledgement status %d", got)
	}
	select {
	case <-s.cleaned:
	default:
		t.Fatal("valid acknowledgement was not recorded")
	}
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
	messages := strings.TrimSuffix(strings.Repeat(`"a",`, 13000), ",")
	body := `{"round":"r1","token":"tok-1","submission":"s-00000000000000aa","questions":{"storage":{"messages":[` + messages + `]}}}`
	if status := post(body); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("13000 one-byte messages: status %d, want 413", status)
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
	frameURL := s.view.Phase.Questions[0].Frame
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
	if len(page.Build) != 16 || !bytes.Contains(page.Shell, []byte(`<meta name="lavagna-build" content="`+page.Build+`">`)) {
		t.Errorf("the shell does not carry the build id %q the view sends", page.Build)
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
	return fmt.Sprintf(`{"round":"r1","token":"tok-1","submission":%q,"overview":{"images":%s}}`, submission, b)
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
	got := (<-s.accepted).overview.Images
	if len(got) != 2 {
		t.Fatalf("images %q, want two paths", got)
	}
	for i, want := range []string{screenshots["gif"], screenshots["png"]} {
		b, err := os.ReadFile(got[i])
		if !filepath.IsAbs(got[i]) || err != nil || string(b) != want {
			t.Errorf("image %d at %q: %v, content %q, want an absolute path to %q", i, got[i], err, b, want)
		}
	}
}

func TestUploadedImagesRemainValidAcrossRoundServers(t *testing.T) {
	s, _ := serve(t)
	_, id := postImage(t, s, screenshots["png"])
	next := newRound(roundSpec{Origin: s.origin, ID: "r2", Token: "tok-2", Images: s.imageDir})
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

func TestViewCarriesTheLedgerThePageRenders(t *testing.T) {
	ledger := conversation.Ledger{Title: "Archivio", Round: 2, Order: []string{"storage", "crash", "later"},
		Questions: map[string]conversation.Question{
			"storage": {Title: "Dove?", Status: conversation.Settled, Round: 1, Version: 1, Answer: &conversation.Answer{Choice: "file"},
				Thread: []conversation.Message{
					{Author: "user", Round: 1, Submission: "s-1", Text: "E SQLite?"},
					{Author: "user", Round: 1, Submission: "s-1", Images: []string{"/cache/k/images/0123abcd.png"}},
					{Author: "agent", Round: 2, Text: "Resta A."},
				}},
			"crash": {Title: "Crash?", After: []string{"storage"}, Status: conversation.Open, Round: 2, Version: 3, Marks: []conversation.Marked{{Round: 1, Mark: conversation.Moved}}, Answer: &conversation.Answer{Text: "a modo mio"}},
			"later": {Title: "Pulizia?", After: []string{"crash"}, Status: conversation.Planned},
		},
		Overview:  []conversation.Message{{Author: "agent", Round: 1, Text: "Benvenuto"}},
		Decisions: []conversation.Decision{{Question: "storage", Title: "Dove?", Decision: "File", Round: 1, Why: "Semplice", Rejected: []string{"Database"}}},
	}
	questions := []round.PhaseQuestion{
		{ID: "storage", HTML: `<section class="chapter" id="capire"></section>`, Lead: "Due sessioni.", Reason: "Nessuna dipendenza.",
			Options: []round.PhaseOption{{ID: "file", Label: "File", Detail: "Uno per sessione", Recommended: true}, {ID: "db", Label: "Database"}}},
		{ID: "crash", Options: []round.PhaseOption{{ID: "atomic", Label: "Atomica"}, {ID: "none", Label: "Niente"}}},
		{ID: "later", Planned: true},
	}
	s := newRound(roundSpec{Origin: conversation.Origin{Port: 43210, Cap: "cap-a"}, ID: "r2", Call: "c4", Token: "tok-4", FrameKey: "key-", Images: t.TempDir(), Ledger: ledger, Questions: questions})
	got, err := json.Marshal(s.view)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"round":"r2","call":"c4","build":"` + page.Build + `","token":"tok-4"`,
		`"phase":{"title":"Archivio","round":2,"questions":[`,
		`{"id":"storage","title":"Dove?","status":"settled","round":1,"version":1,"lead":"Due sessioni.","options":[{"id":"file","label":"File","detail":"Uno per sessione","recommended":true},{"id":"db","label":"Database"}],"reason":"Nessuna dipendenza.","answer":{"choice":"file"},"thread":[`,
		`{"author":"user","round":1,"submission":"s-1","images":["0123abcd"]}`,
		`{"author":"agent","round":2,"text":"Resta A."}`,
		`"frame":"/f/key-storage/storage/","resources":["/f/key-storage/storage/","/f/key-storage/storage/.lavagna/lavagna.css"`,
		`{"id":"crash","title":"Crash?","after":["storage"],"status":"open","round":2,"marks":[{"round":1,"mark":"moved"}],"version":3,`,
		`"answer":{"text":"a modo mio"},"thread":[]}`,
		`{"id":"later","title":"Pulizia?","after":["crash"],"status":"planned","thread":[]}`,
		`"overview":[{"author":"agent","round":1,"text":"Benvenuto"}]`,
		`"decisions":[{"question":"storage","title":"Dove?","decision":"File","round":1,"why":"Semplice","rejected":["Database"]}]`,
	} {
		if !strings.Contains(string(got), want) {
			t.Errorf("view lacks %s\n%s", want, got)
		}
	}
	if strings.Contains(string(got), "/cache/") {
		t.Errorf("the view exposes a host path: %s", got)
	}
	if !s.answerable["crash"] || s.answerable["storage"] || s.answerable["later"] {
		t.Errorf("answerable %v, want only the current round's open question", s.answerable)
	}
}
