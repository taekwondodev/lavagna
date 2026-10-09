package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/harness"
	"github.com/taekwondodev/lavagna/internal/witness"
)

// maxHookPayload bounds a Hermes hook payload. A tool result that large holds
// no lavagna outcome, which stays below maxResultBytes.
const maxHookPayload = 4 << 20

// HermesHook appends one Hermes shell-hook payload to the events file of the
// conversation it concerns. Only a witness creates that file, so a session
// lavagna does not observe leaves no trace. It never fails Hermes: every
// payload exits 0 with no output.
func HermesHook(in io.Reader) int {
	// An oversized payload ends the hook without reading the rest: Hermes
	// writes stdin through communicate, which tolerates a closed pipe.
	payload, err := io.ReadAll(io.LimitReader(in, maxHookPayload+1))
	if err != nil || len(payload) > maxHookPayload {
		return exitOK
	}
	session, line, ok := witness.HermesObservation(payload)
	if !ok {
		return exitOK
	}
	c, err := conversation.For(harness.Harness{Kind: harness.Hermes, Session: session}.Identity())
	if err != nil {
		return exitOK
	}
	f, err := os.OpenFile(c.Events(), os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return exitOK
	}
	defer f.Close()
	f.Write(append(line, '\n'))
	return exitOK
}

// hermesHookEvents are the Hermes events lavagna's witness needs: the
// terminal's tool results and every turn's end.
var hermesHookEvents = []struct{ event, matcher string }{
	{"post_tool_call", "terminal"},
	{"on_session_end", ""},
}

type hermesHook struct {
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// HermesHooks registers lavagna's Hermes shell hooks, or removes them, through
// the hermes CLI so that lavagna never edits Hermes's YAML. It changes only
// lavagna's own entries, is idempotent, and does nothing without a Hermes
// home. Hermes asks for consent the first time each hook runs.
func HermesHooks(getenv func(string) string, out io.Writer, install bool) int {
	if info, err := os.Stat(harness.HermesHome(getenv)); err != nil || !info.IsDir() {
		return exitOK
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(out, "lavagna: Hermes hooks unchanged: %v\n", err)
		return exitError
	}
	command := shellWord(exe) + " hermes-hook"
	var changed []string
	// Each event is its own key, so a failure can follow earlier updates;
	// rerunning converges because every step is idempotent.
	failed := func(format string, args ...any) int {
		fmt.Fprintf(out, "lavagna: Hermes hooks not fully updated: "+format+"\n", args...)
		if len(changed) > 0 {
			fmt.Fprintf(out, "lavagna: %s already updated; run the command again to finish\n", strings.Join(changed, " and "))
		}
		return exitError
	}
	for _, h := range hermesHookEvents {
		key := "hooks." + h.event
		current, err := hermesConfig(key)
		if err != nil {
			return failed("%v", err)
		}
		var next []json.RawMessage
		for _, entry := range current {
			var e hermesHook
			if json.Unmarshal(entry, &e) == nil && ours(e.Command) {
				continue
			}
			next = append(next, entry)
		}
		if install {
			entry, _ := json.Marshal(hermesHook{Matcher: h.matcher, Command: command, Timeout: 10})
			next = append(next, entry)
		}
		if sameHooks(current, next) {
			continue
		}
		args := []string{"config", "unset", key}
		if len(next) > 0 {
			value, _ := json.Marshal(next)
			args = []string{"config", "set", key, string(value)}
		}
		if b, err := exec.Command("hermes", args...).CombinedOutput(); err != nil {
			return failed("%s: %v: %s", h.event, err, strings.TrimSpace(string(b)))
		}
		changed = append(changed, h.event)
	}
	switch {
	case len(changed) == 0:
	case install:
		fmt.Fprintf(out, "lavagna: registered Hermes hooks for %s; Hermes asks for consent when each first runs\n", strings.Join(changed, " and "))
	default:
		fmt.Fprintf(out, "lavagna: removed Hermes hooks for %s\n", strings.Join(changed, " and "))
	}
	return exitOK
}

// hermesConfig reads a hook list through the hermes CLI; an unset key is an
// empty list.
func hermesConfig(key string) ([]json.RawMessage, error) {
	cmd := exec.Command("hermes", "config", "get", "--json", key)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		if strings.Contains(string(b)+stderr.String(), "Config key not set") {
			return nil, nil
		}
		return nil, fmt.Errorf("hermes config get %s: %v: %s", key, err, strings.TrimSpace(stderr.String()))
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("hermes config get %s: not a hook list", key)
	}
	return entries, nil
}

// ours recognizes lavagna's hook from any install path.
func ours(command string) bool {
	return strings.HasSuffix(command, " hermes-hook") && strings.Contains(command, "lavagna")
}

func sameHooks(a, b []json.RawMessage) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// shellWord quotes a path for the shlex splitting Hermes applies to commands.
func shellWord(s string) string {
	if !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
