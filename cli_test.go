package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
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
	return append([]string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "BROWSER=true"}, vars...)
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

func spawn(t *testing.T, environ []string, src string) *call {
	t.Helper()
	cmd := exec.Command(binary, "round")
	cmd.Env = environ
	cmd.Stdin = strings.NewReader(src)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	c := &call{t: t, cmd: cmd, out: bufio.NewReader(stdout)}
	first, err := c.out.ReadString('\n')
	if err != nil {
		t.Fatalf("no first line: %v", err)
	}
	c.first = strings.TrimRight(first, "\n")
	if m := statusLine.FindStringSubmatch(c.first); m != nil {
		c.origin, c.url = m[2], m[2]+m[3]
	}
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

func (c *call) finish() ([]string, int) {
	c.t.Helper()
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
	return fmt.Sprintf(`{"round":%q,"token":%q,"submission":%q,"choices":{"storage":"db"},"comments":[{"text":"ok, ma..."},{"text":"generale"}]}`, round, token, submission)
}

const decision = `# Capire
Lo stato vive in un solo file.

# Decidere
## Dove salviamo lo stato? {id="storage"}
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
	lines, code := run(t, env(t, "LAVAGNA_SESSION=a"), "# Capire\ntesto\n::: card\nx\n:::\n", "round")
	if code != 2 || len(lines) != 1 || lines[0] != `{"lavagna":"invalid","errors":["round.md:3: unknown block ::: card"]}` {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	lines, code = run(t, env(t), decision, "round")
	if code != 2 || !strings.HasPrefix(last(lines), `{"lavagna":"invalid","errors":["no conversation identity`) {
		t.Fatalf("exit %d, output %q", code, lines)
	}
	lines, code = run(t, env(t, "LAVAGNA_SESSION=a"), "", "round", "/tmp/round")
	if code != 2 || !strings.HasPrefix(last(lines), `{"lavagna":"invalid","errors":["usage: `) {
		t.Fatalf("exit %d, output %q", code, lines)
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
	want := `{"lavagna":"feedback","round":"r1","submission":"s-0123456789abcdef","choices":{"storage":"db"},"comments":[{"anchor":null,"text":"ok, ma..."},{"anchor":null,"text":"generale"}],"images":[]}`
	if code != 0 || len(lines) != 1 || lines[0] != want {
		t.Fatalf("exit %d, output %q", code, lines)
	}

	lines, code = run(t, environ, "", "close")
	if code != 0 || last(lines) != `{"lavagna":"closed","page":"not-connected"}` {
		t.Fatalf("close: exit %d, output %q", code, lines)
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
