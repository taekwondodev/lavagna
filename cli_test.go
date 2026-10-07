package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "lavagna-test")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "lavagna")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func env(t *testing.T, vars ...string) []string {
	home, err := os.MkdirTemp("/tmp", "lavagna-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	return append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "BROWSER=true"}, vars...)
}

func run(t *testing.T, environ []string, stdin string, args ...string) (lines []string, code int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Env = environ
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n"), code
}

func last(lines []string) string { return lines[len(lines)-1] }

type call struct {
	t      *testing.T
	cmd    *exec.Cmd
	out    *bufio.Reader
	first  string
	url    string
	origin string
}

var statusLine = regexp.MustCompile(`^lavagna · round (r\d+) · (http://127\.0\.0\.1:\d+)(/s/[0-9a-f]{64}/) · Esc per interrompere$`)

func command(environ []string, src string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, append([]string{"round"}, args...)...)
	cmd.Env = environ
	cmd.Stdin = strings.NewReader(src)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func (c *call) status(first string) {
	c.first = strings.TrimRight(first, "\n")
	if m := statusLine.FindStringSubmatch(c.first); m != nil {
		c.origin, c.url = m[2], m[2]+m[3]
	}
}

func spawn(t *testing.T, environ []string, src string, args ...string) *call {
	t.Helper()
	return spawnCommand(t, command(environ, src, args...))
}

func spawnCommand(t *testing.T, cmd *exec.Cmd) *call {
	t.Helper()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &call{t: t, cmd: cmd, out: bufio.NewReader(stdout)}
	t.Cleanup(c.esc)
	status := bufio.NewReader(stderr)
	first, err := status.ReadString('\n')
	if errors.Is(err, io.EOF) && first == "" {
		// A refused round returns only a JSON outcome on stdout.
		first, err = c.out.ReadString('\n')
	}
	if err != nil {
		t.Fatalf("no first line: %v", err)
	}
	c.status(first)
	go io.Copy(io.Discard, status)
	return c
}

func startRound(t *testing.T, environ []string, src string) *call {
	t.Helper()
	c := spawn(t, environ, src)
	if c.url == "" {
		t.Fatalf("status line %q", c.first)
	}
	return c
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func startDir(t *testing.T, environ []string, dir string) *call {
	t.Helper()
	c := spawn(t, environ, "", dir)
	if c.url == "" {
		t.Fatalf("status line %q", c.first)
	}
	return c
}

func (c *call) esc() {
	if c.cmd.ProcessState != nil {
		return
	}
	syscall.Kill(-c.cmd.Process.Pid, syscall.SIGKILL)
	c.cmd.Wait()
}

func (c *call) finish() ([]string, int) {
	c.t.Helper()
	deadline := time.AfterFunc(20*time.Second, func() { c.cmd.Process.Kill() })
	defer deadline.Stop()
	rest, _ := io.ReadAll(c.out)
	err := c.cmd.Wait()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	}
	return strings.Split(strings.TrimRight(string(rest), "\n"), "\n"), code
}

func (c *call) view() (round, token string) {
	c.t.Helper()
	resp, err := http.Get(c.url + "events")
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			var v struct{ Round, Token string }
			if err := json.Unmarshal([]byte(data), &v); err != nil {
				c.t.Fatal(err)
			}
			return v.Round, v.Token
		}
	}
	c.t.Fatal("no round event")
	return "", ""
}

func (c *call) post(url, origin, contentType, body string) int {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	resp.Body.Close()
	return resp.StatusCode
}

func batch(round, token, submission string) string {
	return fmt.Sprintf(`{"round":%q,"token":%q,"submission":%q,"questions":{"storage":{"choice":"db","messages":["ok, ma...","generale"]}}}`, round, token, submission)
}

const decision = `# Dove salviamo lo stato? {id="storage"}
## Capire
Lo stato vive in un solo file.

## Decidere
- [file] Un file per sessione
- [db] Un database locale
`

func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		vars []string
		ok   bool
	}{
		{"pi session", []string{"PI_SESSION_ID=abc", "PI_SESSION_FILE=/tmp/s.jsonl"}, true},
		{"lavagna session", []string{"LAVAGNA_SESSION=other-harness"}, true},
		{"pi id without file", []string{"PI_SESSION_ID=abc"}, false},
		{"no identity", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(binary, "check")
			cmd.Env = env(t, c.vars...)
			out, err := cmd.CombinedOutput()
			if c.ok && err != nil {
				t.Fatalf("exit %v: %s", err, out)
			}
			if !c.ok && (err == nil || !strings.Contains(string(out), "LAVAGNA_SESSION")) {
				t.Fatalf("want a non-zero exit with the reason, got %v: %s", err, out)
			}
		})
	}
}

func TestRoundRefusesInvalidInputBeforeServing(t *testing.T) {
	bad := "# Q {id=\"q\"}\n## Capire\n::: card\nx\n:::\n## Decidere\n- [a] A\n- [b] B\n"
	lines, code := run(t, env(t, "LAVAGNA_SESSION=a"), bad, "round")
	if code != 2 || len(lines) != 1 || !strings.Contains(lines[0], `round.md:3: unknown block ::: card`) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	lines, code = run(t, env(t), decision, "round")
	if code != 2 || !strings.HasPrefix(last(lines), `{"lavagna":"invalid","errors":["no conversation identity`) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	lines, code = run(t, env(t, "LAVAGNA_SESSION=a"), "", "round", "uno", "due")
	if code != 2 || last(lines) != `{"lavagna":"invalid","errors":["usage: lavagna check | round [DIR] | round --help [grammar] | feedback SUBMISSION [--question ID | --overview | --all] | feedback --help | close"]}` {
		t.Fatalf("exit %d, output %q", code, lines)
	}
}

func TestRoundDirRefusesBoundsBeforeServing(t *testing.T) {
	cases := map[string]struct {
		files map[string]string
		want  string
	}{
		"invalid resource": {map[string]string{"round.md": "# Q {id=\"q\"}\n## Capire\n<img src=\"q/missing.png\" alt=\"\">\n## Decidere\n- [a] A\n- [b] B\n"}, ""},
		"33 files":         {map[string]string{"round.md": decision}, `{"lavagna":"invalid","errors":["round: more than 32 files"]}`},
		"4 MiB":            {map[string]string{"round.md": decision, "storage/grande.png": strings.Repeat("x", 4<<20)}, fmt.Sprintf(`{"lavagna":"invalid","errors":["round: %d bytes exceed the 4194304 byte bound"]}`, (4<<20)+len(decision))},
		"missing file":     {map[string]string{"round.md": "# Q {id=\"q\"}\n## Capire\n<img src=\"q/manca.png\" alt=\"\">\n## Decidere\n- [a] A\n- [b] B\n"}, `{"lavagna":"invalid","errors":["round.md:3: src=\"q/manca.png\" is not a file of the round directory or a data: image"]}`},
	}
	for i := range 32 {
		cases["33 files"].files[fmt.Sprintf("%02d.css", i)] = ""
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, c.files)
			lines, code := run(t, env(t, "LAVAGNA_SESSION=dir"), "", "round", dir)
			if code != 2 || len(lines) != 1 || c.want != "" && lines[0] != c.want || c.want == "" && !strings.Contains(lines[0], `"lavagna":"invalid"`) {
				t.Fatalf("exit %d, output %q", code, lines)
			}
		})
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"round.md": decision})
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts.png")); err != nil {
		t.Fatal(err)
	}
	lines, code := run(t, env(t, "LAVAGNA_SESSION=dir"), "", "round", dir)
	if code != 2 || last(lines) != `{"lavagna":"invalid","errors":["round: hosts.png: symlinks are not allowed"]}` {
		t.Fatalf("symlink: exit %d, output %q", code, lines)
	}
}

func TestRoundHelpPrintsPerQuestionFormat(t *testing.T) {
	cmd := exec.Command(binary, "round", "--help", "grammar")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	help := string(out)
	for _, want := range []string{`{id="crash"`, `after="storage"`, `45!1`, `{recommended}`, `=> consequence`, `/f/<key>/<question-id>/`, `--question ID`, `32 KiB`, `6 MiB`} {
		if !strings.Contains(help, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, legacy := range []string{"--reuse", "{ref=", "--comment", "--offset"} {
		if strings.Contains(help, legacy) {
			t.Errorf("help retains removed syntax %q", legacy)
		}
	}
}

func TestRoundReturnsOneFeedbackBatch(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=a")
	c := startRound(t, environ, decision)

	lines, code := run(t, environ, decision, "round")
	if code != 3 || last(lines) != `{"lavagna":"busy","round":"r1"}` {
		t.Fatalf("second round: exit %d, output %q", code, lines)
	}

	round, token := c.view()
	if status := c.post(c.url+"send", c.origin, "application/json", batch(round, token, "s-0123456789abcdef")); status != http.StatusAccepted {
		t.Fatalf("send: %d", status)
	}
	lines, code = c.finish()
	want := `{"lavagna":"feedback","round":"r1","submission":"s-0123456789abcdef","questions":{"storage":{"choice":"db","messages":["ok, ma...","generale"]}}}`
	if code != 0 || len(lines) != 1 || lines[0] != want {
		t.Fatalf("exit %d, output %q", code, lines)
	}

	lines, code = run(t, environ, "", "close")
	if code != 0 || last(lines) != `{"lavagna":"closed","page":"not-connected"}` {
		t.Fatalf("close: exit %d, output %q", code, lines)
	}
}

func TestDeferredQuestionFeedbackAndSelectors(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=deferred")
	c := startRound(t, environ, decision)
	roundID, token := c.view()
	message := strings.Repeat("feedback ", 300)
	body := fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-0123456789abcdef","questions":{"storage":{"choice":"db","messages":[%q]}},"overview":{"messages":["general"]}}`, roundID, token, message)
	if status := c.post(c.url+"send", c.origin, "application/json", body); status != http.StatusAccepted {
		t.Fatalf("send %d", status)
	}
	if lines, code := c.finish(); code != 0 || len(lines) != 1 || !strings.Contains(lines[0], `"deferred":true`) || !strings.Contains(lines[0], `"messages":1`) {
		t.Fatalf("deferred outcome: exit %d %q", code, lines)
	}
	lines, code := run(t, environ, "", "feedback", "s-0123456789abcdef")
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], `"deferred":true`) {
		t.Fatalf("summary read: %d %q", code, lines)
	}
	lines, code = run(t, environ, "", "feedback", "s-0123456789abcdef", "--question", "storage")
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], message) || !strings.Contains(lines[0], `"question":"storage"`) {
		t.Fatalf("question read: %d %q", code, lines)
	}
	lines, code = run(t, environ, "", "feedback", "s-0123456789abcdef", "--overview")
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], `"messages":["general"]`) {
		t.Fatalf("overview read: %d %q", code, lines)
	}
	lines, code = run(t, environ, "", "feedback", "s-0123456789abcdef", "--all")
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], message) || !strings.Contains(lines[0], `"overview"`) {
		t.Fatalf("full read: %d %q", code, lines)
	}
}

func TestConcurrentFirstCallsConvergeOnOneOwner(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=race")
	calls := make([]*call, 4)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Go(func() { calls[i] = spawn(t, environ, decision) })
	}
	wg.Wait()
	var owners []*call
	for _, c := range calls {
		switch {
		case c.url != "":
			owners = append(owners, c)
		case c.first != `{"lavagna":"busy","round":"r1"}`:
			t.Errorf("loser printed %q", c.first)
		}
	}
	if len(owners) != 1 {
		t.Fatalf("%d owners, want 1", len(owners))
	}
	round, token := owners[0].view()
	owners[0].post(owners[0].url+"send", owners[0].origin, "application/json", batch(round, token, "s-0123456789abcdef"))
	if _, code := owners[0].finish(); code != 0 {
		t.Fatalf("owner exit %d", code)
	}
}

func TestSendRefusesUnauthorizedBatches(t *testing.T) {
	home := t.TempDir()
	environ := func(session string) []string {
		return []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "BROWSER=true", "LAVAGNA_SESSION=" + session}
	}
	a := startRound(t, environ("a"), decision)
	b := startRound(t, environ("b"), decision)
	round, token := a.view()
	bRound, bToken := b.view()
	if a.origin == b.origin {
		t.Fatal("two conversations share an origin")
	}
	bPageOnA := strings.Replace(b.url, b.origin, a.origin, 1) + "send"

	cases := []struct {
		name, url, origin, contentType, body string
		status                               int
	}{
		{"malformed", a.url + "send", a.origin, "application/json", `{"round":`, http.StatusBadRequest},
		{"missing token", a.url + "send", a.origin, "application/json", batch(round, "", "s-0123456789abcdef"), http.StatusBadRequest},
		{"missing Origin", a.url + "send", "", "application/json", batch(round, token, "s-0123456789abcdef"), http.StatusForbidden},
		{"foreign Origin", a.url + "send", "http://evil.example", "application/json", batch(round, token, "s-0123456789abcdef"), http.StatusForbidden},
		{"form post", a.url + "send", a.origin, "application/x-www-form-urlencoded", batch(round, token, "s-0123456789abcdef"), http.StatusUnsupportedMediaType},
		{"stale round", a.url + "send", a.origin, "application/json", batch("r0", token, "s-0123456789abcdef"), http.StatusConflict},
		{"stale token", a.url + "send", a.origin, "application/json", batch(round, bToken, "s-0123456789abcdef"), http.StatusConflict},
		{"cross-session", bPageOnA, a.origin, "application/json", batch(bRound, bToken, "s-0123456789abcdef"), http.StatusConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if status := a.post(c.url, c.origin, c.contentType, c.body); status != c.status {
				t.Fatalf("status %d, want %d", status, c.status)
			}
		})
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { a.post(a.url+"send", a.origin, "application/json", batch(round, token, "s-00000000000000aa")) })
	}
	wg.Wait()
	lines, code := a.finish()
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], `"submission":"s-00000000000000aa"`) {
		t.Fatalf("concurrent sends: exit %d, output %q", code, lines)
	}

	if status := b.post(b.url+"send", b.origin, "application/json", batch(bRound, bToken, "s-00000000000000bb")); status != http.StatusAccepted {
		t.Fatalf("b send: %d", status)
	}
	lines, code = b.finish()
	if code != 0 || len(lines) != 1 || !strings.Contains(lines[0], `"submission":"s-00000000000000bb"`) {
		t.Fatalf("b: exit %d, output %q", code, lines)
	}
}

func encoded(t *testing.T, encode func(io.Writer, image.Image) error) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 18, G: 99, B: 91, A: 255})
	var buf bytes.Buffer
	if err := encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func screenshot(t *testing.T) []byte { return encoded(t, png.Encode) }

func images(t *testing.T, line string) []string {
	t.Helper()
	var got struct {
		Questions map[string]struct {
			Images []string `json:"images"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("result %q: %v", line, err)
	}
	return got.Questions["storage"].Images
}

func (c *call) attach(round, token string, body []byte) string {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, c.url+"images", bytes.NewReader(body))
	req.Header.Set("Origin", c.origin)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Lavagna-Round", round)
	req.Header.Set("Lavagna-Token", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct{ Image string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusCreated {
		c.t.Fatalf("upload: status %d, %v", resp.StatusCode, err)
	}
	return out.Image
}

func sendScreenshot(t *testing.T, environ []string, shot []byte) string {
	t.Helper()
	c := startRound(t, environ, decision)
	round, token := c.view()
	id := c.attach(round, token, shot)
	body := fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-0123456789abcdef","questions":{"storage":{"images":[%q]}}}`, round, token, id)
	if status := c.post(c.url+"send", c.origin, "application/json", body); status != http.StatusAccepted {
		t.Fatalf("send: %d", status)
	}
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	got := images(t, lines[0])
	if len(got) != 1 {
		t.Fatalf("images %q, want one", got)
	}
	return got[0]
}

func TestCloseDeletesTheConversationScreenshots(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=shots")
	shot := screenshot(t)
	path := sendScreenshot(t, environ, shot)
	if b, err := os.ReadFile(path); !filepath.IsAbs(path) || err != nil || !bytes.Equal(b, shot) {
		t.Fatalf("returned image %q: %v, want an absolute path to the pasted bytes", path, err)
	}
	if lines, code := run(t, environ, "", "close"); code != 0 {
		t.Fatalf("close: exit %d, output %q", code, lines)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the screenshot survived close: %v", err)
	}
}

func TestAnyStartSweepsIdleConversations(t *testing.T) {
	home := t.TempDir()
	environ := func(session string) []string {
		return []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "BROWSER=true", "LAVAGNA_SESSION=" + session}
	}
	conversationDir := func(session string, age time.Duration) string {
		dir := filepath.Dir(filepath.Dir(sendScreenshot(t, environ(session), screenshot(t))))
		then := time.Now().Add(-age)
		if err := os.Chtimes(dir, then, then); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	idle := conversationDir("idle", 8*24*time.Hour)
	recent := conversationDir("recent", 23*time.Hour)
	cmd := exec.Command(binary, "check")
	cmd.Env = environ("other")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("check: %v %s", err, out)
	}
	if _, err := os.Stat(idle); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a conversation untouched for 8 days survived a start: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("a conversation untouched for 23 hours was removed: %v", err)
	}
}
