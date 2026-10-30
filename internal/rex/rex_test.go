package rex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	sessionID = "session:test0001"
	anchorID  = "block:anchor001"
	windowID  = "window:win00001"
	appID     = "client:app00001"
	capA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	capB      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	pageA     = "http://127.0.0.1:41000/s/" + capA + "/"
	pageB     = "http://127.0.0.1:41000/s/" + capB + "/"
)

func TestMain(m *testing.M) {
	switch filepath.Base(os.Args[0]) {
	case "rex", "terminal-browser":
		os.Exit(fakeTool(filepath.Base(os.Args[0]), os.Args[1:]))
	}
	os.Exit(m.Run())
}

type fakeBlock struct {
	Session string `json:"session"`
	block
}

type fakeState struct {
	Blocks []fakeBlock `json:"blocks"`
	Next   int         `json:"next"`

	FailRunAfterCreate bool `json:"fail_run_after_create"`
	RefuseActions      bool `json:"refuse_actions"`
}

type call struct {
	Tool string            `json:"tool"`
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

func fakeTool(tool string, args []string) int {
	entry := call{Tool: tool, Args: args, Env: map[string]string{}}
	for _, k := range []string{"HOME", "XDG_STATE_HOME", "TERMINAL_BROWSER_CONFIG_DIR", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "TERMINAL_BROWSER_APPDATA", "TERMINAL_BROWSER_PRESENT", "TERMINAL_BROWSER_NO_TELEMETRY"} {
		if v, ok := os.LookupEnv(k); ok {
			entry.Env[k] = v
		}
	}
	line, _ := json.Marshal(entry)
	log, _ := os.OpenFile(os.Getenv("FAKE_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	log.Write(append(line, '\n'))
	log.Close()
	if tool == "terminal-browser" {
		return 0
	}

	lockf, _ := os.OpenFile(os.Getenv("FAKE_STATE")+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	syscall.Flock(int(lockf.Fd()), syscall.LOCK_EX)
	defer lockf.Close()
	var st fakeState
	raw, _ := os.ReadFile(os.Getenv("FAKE_STATE"))
	json.Unmarshal(raw, &st)
	save := func() { b, _ := json.Marshal(st); os.WriteFile(os.Getenv("FAKE_STATE"), b, 0o600) }
	deny := func() int { fmt.Fprintln(os.Stderr, "boom http://127.0.0.1:41000/SECRETCAP-STDERR"); return 1 }

	rest := args
	for len(rest) > 0 && strings.HasPrefix(rest[0], "--") {
		if rest[0] == "--server" || rest[0] == "--timeout" {
			rest = rest[2:]
		} else {
			rest = rest[1:]
		}
	}
	flag := func(name string) string {
		i := slices.Index(rest, name)
		if i < 0 {
			return ""
		}
		return rest[i+1]
	}
	switch rest[0] {
	case "run":
		anchor := slices.IndexFunc(st.Blocks, func(b fakeBlock) bool { return b.ID == flag("--block") && b.Session == flag("--session") })
		if anchor < 0 {
			return deny()
		}
		st.Next++
		st.Blocks = append(st.Blocks, fakeBlock{flag("--session"), block{
			ID: fmt.Sprintf("block:made%04d", st.Next), Label: flag("--label"), Placement: "placed",
			Creator: terminalCreator, Window: windowID, Owner: appID}})
		save()
		if st.FailRunAfterCreate {
			return deny()
		}
		fmt.Println("{}")
	case "api":
		var in map[string]any
		json.Unmarshal([]byte(rest[3]), &in)
		switch rest[2] {
		case "session.list_blocks":
			if in["session_id"] != sessionID {
				return deny()
			}
			out := []block{}
			for _, b := range st.Blocks {
				if b.Session == sessionID {
					out = append(out, b.block)
				}
			}
			json.NewEncoder(os.Stdout).Encode(map[string]any{"blocks": out})
		case "client.perform":
			if st.RefuseActions {
				return deny()
			}
		case "client.list":
			fmt.Printf(`{"clients":[{"client_id":"client:cli00001","info":{"kind":"cli","platform":"macos"}},`+
				`{"client_id":%q,"info":{"kind":"app","platform":"macos"},"session_ids":[%q]}]}`+"\n", appID, sessionID)
		}
	}
	return 0
}

type env struct {
	t       *testing.T
	home    string
	target  *Target
	state   string
	logPath string
	bin     string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	e := &env{t: t, home: filepath.Join(dir, "home"), state: filepath.Join(dir, "state.json"), logPath: filepath.Join(dir, "log"), bin: filepath.Join(dir, "bin")}
	os.MkdirAll(e.home, 0o755)
	os.MkdirAll(e.bin, 0o755)
	self, _ := os.Executable()
	for _, name := range []string{"rex", "terminal-browser"} {
		if err := os.Symlink(self, filepath.Join(e.bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("FAKE_STATE", e.state)
	t.Setenv("FAKE_LOG", e.logPath)
	t.Cleanup(func() {
		for _, page := range []string{pageA, pageB} {
			sum := sha256.Sum256([]byte(page))
			dir, _ := runtimeDir(e.home, fmt.Sprintf("lavagna-%x", sum[:8]))
			os.RemoveAll(dir)
		}
	})
	e.write(fakeState{Blocks: []fakeBlock{{sessionID, block{ID: anchorID, Label: "shell", Placement: "placed", Creator: terminalCreator, Window: windowID, Owner: appID}}}})
	e.target = Capture(e.getenv(nil))
	if e.target == nil {
		t.Fatal("complete local Rex context was not captured")
	}
	return e
}

func (e *env) getenv(extra map[string]string) func(string) string {
	vars := map[string]string{
		"HOME": e.home, "REX_BIN_DIR": e.bin, "PATH": e.bin,
		"REX_SERVER": "unix:///tmp/rex%20test/server.sock", "REX_SESSION": sessionID, "REX_BLOCK": anchorID,
	}
	for k, v := range extra {
		vars[k] = v
	}
	return func(k string) string { return vars[k] }
}

func (e *env) write(st fakeState) {
	b, _ := json.Marshal(st)
	os.WriteFile(e.state, b, 0o600)
}

func (e *env) read() fakeState {
	var st fakeState
	b, _ := os.ReadFile(e.state)
	json.Unmarshal(b, &st)
	return st
}

func (e *env) calls(tool string, firstArgs ...string) []call {
	var out []call
	f, err := os.Open(e.logPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c call
		json.Unmarshal(sc.Bytes(), &c)
		if c.Tool == tool && strings.Contains(strings.Join(c.Args, " "), strings.Join(firstArgs, " ")) {
			out = append(out, c)
		}
	}
	return out
}

func (e *env) open(page string, focus bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return e.target.Open(ctx, page, focus)
}

func TestCaptureOnlyForCompleteLocalRexContext(t *testing.T) {
	e := newEnv(t)
	for name, extra := range map[string]map[string]string{
		"browser override": {"BROWSER": "firefox --new-window"},
		"remote server":    {"REX_SERVER": "tcp://example.com:9000"},
		"credentialed":     {"REX_SERVER": "unix://user:pw@/tmp/s.sock"},
		"missing session":  {"REX_SESSION": ""},
		"malformed block":  {"REX_BLOCK": "block:../../x"},
		"missing server":   {"REX_SERVER": ""},
		"relative home":    {"HOME": "home"},
	} {
		if Capture(e.getenv(extra)) != nil {
			t.Errorf("%s: captured a target", name)
		}
	}
	if Capture(e.getenv(map[string]string{"BROWSER": "  "})) == nil {
		t.Error("blank BROWSER blocked Rex")
	}
}

func TestTargetTransportRoundTripsAndRejectsUnsafePayloads(t *testing.T) {
	e := newEnv(t)
	raw := e.target.Encode()
	if strings.Contains(raw, capA) || strings.Contains(raw, "\n") {
		t.Fatalf("payload carries a page or spans lines: %s", raw)
	}
	got, err := Decode(raw)
	if err != nil || *got != *e.target {
		t.Fatalf("round trip: %v %+v", err, got)
	}
	for name, mutate := range map[string]func(map[string]any){
		"remote server":  func(m map[string]any) { m["server"] = "tcp://example.com:9000" },
		"malformed id":   func(m map[string]any) { m["session"] = "session:a b" },
		"relative tool":  func(m map[string]any) { m["rex"] = "rex" },
		"relative home":  func(m map[string]any) { m["home"] = "~/x" },
		"unknown field":  func(m map[string]any) { m["cmd"] = "x" },
		"other version":  func(m map[string]any) { m["v"] = 2 },
		"relative brows": func(m map[string]any) { m["browser"] = "./terminal-browser" },
	} {
		var m map[string]any
		json.Unmarshal([]byte(raw), &m)
		mutate(m)
		b, _ := json.Marshal(m)
		if _, err := Decode(string(b)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Decode(raw[:len(raw)-1] + strings.Repeat(" ", maxPayload) + "}"); err == nil {
		t.Error("oversized payload accepted")
	}
	for _, invalid := range []string{"not json", raw + "}", raw + " {}"} {
		if _, err := Decode(invalid); err == nil {
			t.Errorf("malformed or trailing JSON accepted: %q", invalid)
		}
	}
}

func TestMissingToolsGiveSafeActionableErrors(t *testing.T) {
	e := newEnv(t)
	noRex := *e.target
	noRex.rex = ""
	noBrowser := *e.target
	noBrowser.browser = ""
	for name, tc := range map[string]struct {
		target *Target
		want   string
	}{"rex": {&noRex, "Rex CLI not found"}, "browser": {&noBrowser, "terminal-browser not found"}} {
		err := tc.target.Open(context.Background(), pageA, false)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "SECRETCAP") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.calls("rex", "run")) != 0 {
		t.Error("created a panel without its tools")
	}
	if Capture(e.getenv(map[string]string{"REX_BIN_DIR": "", "PATH": ""})).rex != "" {
		t.Error("found a Rex CLI that does not exist")
	}
}

func TestOpenCreatesIsolatedSplitWithoutFocus(t *testing.T) {
	e := newEnv(t)
	t.Setenv("XDG_STATE_HOME", "/preserved/state")
	if err := e.open(pageA, false); err != nil {
		t.Fatal(err)
	}
	runs := e.calls("rex", "run")
	if len(runs) != 1 {
		t.Fatalf("runs: %d", len(runs))
	}
	args := strings.Join(runs[0].Args, "\x00")
	for _, want := range []string{
		"--server\x00unix:///tmp/rex%20test/server.sock", "--autostart=false",
		"run\x00--session\x00" + sessionID + "\x00--block\x00" + anchorID + "\x00--split\x00right\x00--ratio\x0050\x00--focus=false",
		"--shell=none", "\x00--\x00/usr/bin/env\x00", "open\x00" + pageA + "\x00--no-merge",
		"TERMINAL_BROWSER_NO_TELEMETRY=1", "TERMINAL_BROWSER_PRESENT=animation", "XDG_RUNTIME_DIR=/tmp/lr-",
		"TERMINAL_BROWSER_APPDATA=", "TERMINAL_BROWSER_CONFIG_DIR=",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("run lacks %q in %q", want, args)
		}
	}
	if strings.Contains(args, "XDG_STATE_HOME") || strings.Contains(args, "\x00HOME=") {
		t.Errorf("run overrides home or state: %q", args)
	}
	settings := map[string]string{}
	for _, c := range e.calls("terminal-browser", "config set") {
		settings[c.Args[2]] = c.Args[3]
		if c.Env["HOME"] != os.Getenv("HOME") || c.Env["XDG_STATE_HOME"] != "/preserved/state" {
			t.Errorf("config set changed HOME or XDG_STATE_HOME: %v", c.Env)
		}
		for _, k := range []string{"TERMINAL_BROWSER_CONFIG_DIR", "XDG_DATA_HOME", "XDG_CACHE_HOME", "TERMINAL_BROWSER_APPDATA"} {
			if !strings.HasPrefix(c.Env[k], e.home) {
				t.Errorf("%s not isolated under home: %v", k, c.Env)
			}
		}
		if !strings.HasPrefix(c.Env["XDG_RUNTIME_DIR"], "/tmp/lr-") {
			t.Errorf("runtime not private /tmp: %v", c.Env)
		}
	}
	if len(settings) != 1 || settings["render.fps"] != "30" {
		t.Errorf("unexpected settings changed: %v; want only render.fps=30", settings)
	}
	if len(e.calls("rex", "focus_block")) != 0 {
		t.Error("an unfocused open focused")
	}
}

func TestOpenSamePageOnceAndDifferentPagesApart(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	failures := make(chan error, 6)
	for range 6 {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- e.open(pageA, false) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Errorf("concurrent open failed: %v", err)
		}
	}
	if err := e.open(pageA, false); err != nil {
		t.Fatal(err)
	}
	if n := len(e.calls("rex", "run")); n != 1 {
		t.Fatalf("one page created %d panels", n)
	}
	if err := e.open(pageB, false); err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, b := range e.read().Blocks[1:] {
		labels[b.Label] = true
	}
	if len(labels) != 2 {
		t.Fatalf("labels: %v", labels)
	}
	configs := e.calls("terminal-browser", "config set")
	if len(configs) != 2 {
		t.Fatalf("different pages configured %d browsers, want 2", len(configs))
	}
	for _, key := range []string{"TERMINAL_BROWSER_APPDATA", "TERMINAL_BROWSER_CONFIG_DIR", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		if configs[0].Env[key] == "" || configs[0].Env[key] == configs[1].Env[key] {
			t.Errorf("different pages share browser state: %s=%q", key, configs[0].Env[key])
		}
	}
	for _, c := range configs {
		socket := filepath.Join(c.Env["XDG_RUNTIME_DIR"], "terminal-browser-ffffffff", "agent-browser", "terminal-browser-999999-999.sock")
		if len(socket) >= 104 {
			t.Errorf("browser runtime exceeds macOS Unix socket limit: %s", socket)
		}
	}
}

func TestFocusReusesPanelSelectsAppAndRecreatesAfterClose(t *testing.T) {
	e := newEnv(t)
	if err := e.open(pageA, false); err != nil {
		t.Fatal(err)
	}
	if err := e.open(pageA, true); err != nil {
		t.Fatal(err)
	}
	if n := len(e.calls("rex", "run")); n != 1 {
		t.Fatalf("focus created a panel: %d runs", n)
	}
	made := e.read().Blocks[1].ID
	if got := e.calls("rex", "session.focus_block"); len(got) != 1 || !strings.Contains(strings.Join(got[0].Args, " "), `"block_id":"`+made+`"`) {
		t.Errorf("focus_block: %+v", got)
	}
	perform := e.calls("rex", "client.perform")
	want := fmt.Sprintf(`{"action":"session.select","args":{"session_id":%q,"window_id":%q},"client_id":%q}`, sessionID, windowID, appID)
	if len(perform) != 1 || perform[0].Args[len(perform[0].Args)-1] != want {
		t.Errorf("perform: %+v", perform)
	}

	st := e.read()
	st.RefuseActions = true
	e.write(st)
	if err := e.open(pageA, true); err == nil || !strings.Contains(err.Error(), "Remote Control") || strings.Contains(err.Error(), "SECRETCAP") {
		t.Fatalf("refused app selection needs a safe, actionable diagnostic: %v", err)
	}
	st.RefuseActions = false
	st.Blocks = st.Blocks[:1]
	e.write(st)
	if err := e.open(pageA, true); err != nil {
		t.Fatal(err)
	}
	if n := len(e.calls("rex", "run")); n != 2 {
		t.Fatalf("closed panel was not recreated: %d runs", n)
	}
}

func TestFailuresAreSafeAndCreateNothingUnrelated(t *testing.T) {
	e := newEnv(t)
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", name)
			return
		}
		for _, leak := range []string{capA, "SECRETCAP", "41000", "boom"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s leaks %q: %v", name, leak, err)
			}
		}
	}

	st := e.read()
	st.Blocks = nil
	e.write(st)
	err := e.open(pageA, true)
	check("gone anchor", err)
	if err != nil && !strings.Contains(err.Error(), "pane is gone") {
		t.Errorf("gone anchor not actionable: %v", err)
	}

	gone := *e.target
	gone.session = "session:other001"
	check("unknown session", gone.Open(context.Background(), pageA, false))
	for name, page := range map[string]string{
		"remote page":             "https://example.com/SECRETCAP",
		"arbitrary loopback path": "http://127.0.0.1:41000/not-lavagna",
		"page query":              pageA + "?secret=SECRETCAP",
		"page fragment":           pageA + "#SECRETCAP",
		"invalid port":            "http://127.0.0.1:0/s/" + capA + "/",
	} {
		check(name, e.target.Open(context.Background(), page, false))
	}
	if n := len(e.calls("rex", "run")); n != 0 {
		t.Errorf("a failed open still created %d panels", n)
	}
}

func TestRetryAdoptsPanelCreatedBeforeFailedResponse(t *testing.T) {
	e := newEnv(t)
	st := e.read()
	st.FailRunAfterCreate = true
	e.write(st)
	if err := e.open(pageA, false); err != nil {
		t.Fatalf("created panel was not adopted: %v", err)
	}
	st = e.read()
	st.FailRunAfterCreate = false
	e.write(st)
	if err := e.open(pageA, false); err != nil || len(e.read().Blocks) != 2 {
		t.Fatalf("retry duplicated the panel: %v %d", err, len(e.read().Blocks))
	}
}
