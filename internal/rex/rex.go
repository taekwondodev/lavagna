// Package rex opens Lavagna's page in a terminal-browser panel next to the
// agent inside a local Rex session, and focuses that panel again later.
//
// A Target is captured once from the agent's environment and travels as one
// opaque argument to a later process, which can only open pages in the Rex
// session it was captured from.
package rex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// maxPayload bounds an encoded Target read from a notification.
const maxPayload = 16 * 1024

// terminalCreator is the creator of terminal blocks in Rex's registry.
const terminalCreator = "com.superlogical.terminal"

var idPattern = regexp.MustCompile(`^(session|block|window|client):[0-9a-zA-Z-]{8,64}$`)

// Target is where a page opens: a local Rex server, its session and the block
// of the agent the panel splits beside.
type Target struct {
	rex     string
	browser string
	server  string
	session string
	anchor  string
	home    string
}

// wire is the encoded Target. It carries no page and no capability.
type wire struct {
	V       int    `json:"v"`
	Rex     string `json:"rex"`
	Browser string `json:"browser"`
	Server  string `json:"server"`
	Session string `json:"session"`
	Anchor  string `json:"anchor"`
	Home    string `json:"home"`
}

// Capture returns the Target for a complete local Rex context, or nil when
// BROWSER is set or the context is remote, incomplete or malformed. A missing
// tool is kept as an empty path so Open can report it.
func Capture(getenv func(string) string) *Target {
	if len(strings.Fields(getenv("BROWSER"))) > 0 {
		return nil
	}
	t := &Target{
		server:  getenv("REX_SERVER"),
		session: getenv("REX_SESSION"),
		anchor:  getenv("REX_BLOCK"),
		home:    getenv("HOME"),
	}
	if dir := getenv("REX_BIN_DIR"); dir != "" {
		t.rex = executable(filepath.Join(dir, "rex"))
	}
	if t.rex == "" {
		t.rex = lookPath(getenv("PATH"), "rex")
	}
	t.browser = lookPath(getenv("PATH"), "terminal-browser")
	if t.validate() != nil {
		return nil
	}
	return t
}

func executable(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	return path
}

// lookPath finds name in the PATH list as an absolute path, or "".
func lookPath(path, name string) string {
	for _, dir := range filepath.SplitList(path) {
		if p, err := filepath.Abs(filepath.Join(dir, name)); err == nil && filepath.IsAbs(dir) {
			if found := executable(p); found != "" {
				return found
			}
		}
	}
	return ""
}

func (t *Target) validate() error {
	for _, tool := range []string{t.rex, t.browser} {
		if tool != "" && (!filepath.IsAbs(tool) || strings.ContainsRune(tool, 0)) {
			return errors.New("tool path is not absolute")
		}
	}
	if !filepath.IsAbs(t.home) || strings.ContainsRune(t.home, 0) {
		return errors.New("home is not absolute")
	}
	for _, id := range []struct{ kind, value string }{{"session:", t.session}, {"block:", t.anchor}} {
		if !strings.HasPrefix(id.value, id.kind) || !idPattern.MatchString(id.value) {
			return errors.New("malformed Rex id")
		}
	}
	u, err := url.Parse(t.server)
	if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		!filepath.IsAbs(u.Path) || strings.ContainsRune(u.Path, 0) {
		return errors.New("Rex server is not a local unix endpoint")
	}
	return nil
}

// Encode returns the Target as one plain JSON argument.
func (t *Target) Encode() string {
	b, _ := json.Marshal(wire{1, t.rex, t.browser, t.server, t.session, t.anchor, t.home})
	return string(b)
}

// Decode validates a string produced by Encode.
func Decode(raw string) (*Target, error) {
	if len(raw) > maxPayload {
		return nil, errors.New("rex target is too large")
	}
	var w wire
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil || w.V != 1 || dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("rex target is malformed")
	}
	t := &Target{w.Rex, w.Browser, w.Server, w.Session, w.Anchor, w.Home}
	if err := t.validate(); err != nil {
		return nil, fmt.Errorf("rex target is invalid: %w", err)
	}
	return t, nil
}

type block struct {
	ID        string `json:"block_id"`
	Label     string `json:"label"`
	Placement string `json:"placement"`
	Creator   string `json:"creator_name"`
	Window    string `json:"window_id"`
	Owner     string `json:"owner"`
}

// Open shows page in its panel, creating it beside the agent when no placed
// terminal carries the page's label. With focus it also selects that panel.
// Diagnostics never repeat the page or the output of the tools.
func (t *Target) Open(ctx context.Context, page string, focus bool) error {
	if err := loopback(page); err != nil {
		return err
	}
	if t.rex == "" {
		return errors.New("Rex CLI not found; install Rex or set REX_BIN_DIR")
	}
	sum := sha256.Sum256([]byte(page))
	label := "lavagna-" + hex.EncodeToString(sum[:8])
	dir, err := runtimeDir(t.home, label)
	if err != nil {
		return fmt.Errorf("cannot prepare the Rex browser runtime: %w", err)
	}
	unlock, err := lock(ctx, filepath.Join(dir, label+".lock"))
	if err != nil {
		return err
	}
	defer unlock()

	panel, found, err := t.find(ctx, label)
	if err != nil {
		return err
	}
	if !found {
		if panel, err = t.create(ctx, dir, label, page); err != nil {
			return err
		}
	}
	if !focus {
		return nil
	}
	if fresh, found, err := t.find(ctx, label); err != nil {
		return err
	} else if found {
		panel = fresh
	} else {
		return errors.New("the Rex panel closed while selecting it; retry the notification")
	}
	return t.focus(ctx, panel)
}

var pagePath = regexp.MustCompile(`^/s/[0-9a-f]{64}/$`)

func loopback(page string) error {
	u, err := url.Parse(page)
	if err == nil && u.Scheme == "http" && u.User == nil && u.Hostname() == "127.0.0.1" &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" && pagePath.MatchString(u.Path) {
		if port, err := strconv.Atoi(u.Port()); err == nil && port > 0 && port <= 65535 {
			return nil
		}
	}
	return errors.New("page is not a local Lavagna address")
}

// runtimeDir is private to a page and short enough for terminal-browser's
// longest socket names, since macOS TMPDIR makes Unix socket paths too long.
func runtimeDir(home, label string) (string, error) {
	sum := sha256.Sum256([]byte(home + "\x00" + label))
	uid := os.Getuid()
	dir := "/tmp/lr-" + strconv.Itoa(uid) + "-" + hex.EncodeToString(sum[:8])
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || !info.IsDir() || int(st.Uid) != uid || info.Mode().Perm() != 0o700 {
		return "", errors.New("existing runtime directory is not private")
	}
	return dir, nil
}

// lock serialises every process working on one page. Lock files intentionally
// persist until OS cleanup: unlinking can split waiters across different inodes.
func lock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("cannot lock the Rex panel: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("cannot lock the Rex panel: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, errors.New("timed out waiting for another Lavagna process opening this page")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// rexCall runs the Rex CLI against the target server and returns its stdout.
// Stderr is dropped: it may repeat the capability URL.
func (t *Target) rexCall(ctx context.Context, args ...string) ([]byte, error) {
	argv := append([]string{"--server", t.server, "--autostart=false", "--timeout", "3s"}, args...)
	cmd := exec.CommandContext(ctx, t.rex, argv...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, errors.New("Rex is unavailable or refused the request (is the session still open?)")
	}
	return out.Bytes(), nil
}

func (t *Target) api(ctx context.Context, method string, params any, result any) error {
	b, _ := json.Marshal(params)
	out, err := t.rexCall(ctx, "api", "call", method, string(b))
	if err != nil {
		return err
	}
	if result != nil && json.Unmarshal(out, result) != nil {
		return errors.New("Rex answered with an unexpected response")
	}
	return nil
}

func (t *Target) blocks(ctx context.Context) ([]block, error) {
	var res struct {
		Blocks []block `json:"blocks"`
	}
	err := t.api(ctx, "session.list_blocks", map[string]string{"session_id": t.session}, &res)
	return res.Blocks, err
}

// find adopts the placed terminal with label. If existing labels are
// duplicated, adopt the lowest id without closing any panels.
func (t *Target) find(ctx context.Context, label string) (block, bool, error) {
	all, err := t.blocks(ctx)
	if err != nil {
		return block{}, false, err
	}
	var match []block
	for _, b := range all {
		if b.Label == label && b.Placement == "placed" && b.Creator == terminalCreator {
			match = append(match, b)
		}
	}
	if len(match) == 0 {
		return block{}, false, nil
	}
	slices.SortFunc(match, func(a, b block) int { return strings.Compare(a.ID, b.ID) })
	return match[0], true, nil
}

func (t *Target) create(ctx context.Context, runtime, label, page string) (block, error) {
	if t.browser == "" {
		return block{}, errors.New("terminal-browser not found; install it to open Lavagna inside Rex")
	}
	all, err := t.blocks(ctx)
	if err != nil {
		return block{}, err
	}
	if !slices.ContainsFunc(all, func(b block) bool { return b.ID == t.anchor && b.Placement == "placed" }) {
		return block{}, errors.New("the agent's Rex pane is gone; start a new round from the agent's current pane")
	}
	env, err := t.prepareBrowser(runtime, label)
	if err != nil {
		return block{}, err
	}
	cmd := exec.CommandContext(ctx, t.browser, "config", "set", "render.fps", "30")
	cmd.Env = append(os.Environ(), env...)
	if cmd.Run() != nil {
		return block{}, errors.New("terminal-browser configuration failed")
	}
	argv := []string{"run", "--session", t.session, "--block", t.anchor, "--split", "right", "--ratio", "50",
		"--focus=false", "--label", label, "--shell=none", "--json", "--", "/usr/bin/env"}
	argv = append(argv, env...)
	argv = append(argv, t.browser, "open", page, "--no-merge")
	_, runErr := t.rexCall(ctx, argv...)
	// A failure after the block was created still leaves it to adopt.
	panel, found, err := t.find(ctx, label)
	switch {
	case found:
		return panel, nil
	case err != nil:
		return block{}, err
	case runErr != nil:
		return block{}, runErr
	}
	return block{}, errors.New("Rex did not place the Lavagna panel")
}

// prepareBrowser gives each page its own daemon and profile so Chromium's
// per-host zoom cannot cross conversations. HOME and XDG_STATE_HOME stay intact.
func (t *Target) prepareBrowser(runtime, label string) ([]string, error) {
	base := filepath.Join(t.home, ".local", "share", "lavagna", "rex-browser", label)
	env := []string{"TERMINAL_BROWSER_NO_TELEMETRY=1", "TERMINAL_BROWSER_PRESENT=animation"}
	for _, dir := range []struct{ name, path string }{
		{"TERMINAL_BROWSER_APPDATA", filepath.Join(base, "profiles")},
		{"TERMINAL_BROWSER_CONFIG_DIR", filepath.Join(base, "config")},
		{"XDG_DATA_HOME", filepath.Join(base, "data")},
		{"XDG_CACHE_HOME", filepath.Join(base, "cache")},
		{"XDG_RUNTIME_DIR", runtime},
	} {
		if err := os.MkdirAll(dir.path, 0o700); err != nil {
			return nil, fmt.Errorf("cannot prepare the Rex browser profile: %w", err)
		}
		env = append(env, dir.name+"="+dir.path)
	}
	return env, nil
}

type client struct {
	ID   string `json:"client_id"`
	Info struct {
		Kind     string `json:"kind"`
		Platform string `json:"platform"`
	} `json:"info"`
	Sessions []string `json:"session_ids"`
}

// focus focuses panel in the server and selects its session and window in the
// connected app, preferring the app that owns the block.
func (t *Target) focus(ctx context.Context, panel block) error {
	if err := t.api(ctx, "session.focus_block", map[string]string{"session_id": t.session, "block_id": panel.ID}, nil); err != nil {
		return err
	}
	var res struct {
		Clients []client `json:"clients"`
	}
	if err := t.api(ctx, "client.list", struct{}{}, &res); err != nil {
		return err
	}
	suitable := func(c client) bool {
		return c.Info.Kind == "app" && c.Info.Platform == "macos"
	}
	var chosen string
	for _, c := range res.Clients {
		if suitable(c) && c.ID == panel.Owner {
			chosen = c.ID
			break
		}
		if chosen == "" && suitable(c) && slices.Contains(c.Sessions, t.session) {
			chosen = c.ID
		}
	}
	if chosen == "" {
		return errors.New("no Rex app is connected to show the panel")
	}
	if err := t.api(ctx, "client.perform", map[string]any{
		"client_id": chosen,
		"action":    "session.select",
		"args":      map[string]string{"session_id": t.session, "window_id": panel.Window},
	}, nil); err != nil {
		return errors.New("cannot select the Rex window; check Remote Control in Rex Settings > Rex Server and retry from a live session")
	}
	return nil
}
