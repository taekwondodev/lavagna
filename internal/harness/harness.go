package harness

import (
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/taekwondodev/lavagna/internal/witness"
)

type Kind string

const (
	Explicit   Kind = "lavagna"
	Pi         Kind = "pi"
	ClaudeCode Kind = "claude"
	Hermes     Kind = "hermes"
)

var ErrNoIdentity = errors.New("no conversation identity: lavagna recognizes Pi, Claude Code and Hermes; another harness opts in by exporting LAVAGNA_SESSION, one stable value per conversation, from its configuration")

var errAmbiguous = errors.New("several harness identities and the call's owner matches none of them: export LAVAGNA_SESSION, one stable value per conversation, to choose")

type Harness struct {
	Kind Kind

	Session string

	File string
}

func (h Harness) Identity() string {
	if h.Kind == Pi {
		return "pi\x00" + h.Session + "\x00" + h.File
	}
	return string(h.Kind) + "\x00" + h.Session
}

func Resolve(getenv func(string) string, owner func() string) (Harness, error) {
	if s := getenv("LAVAGNA_SESSION"); s != "" {
		return Harness{Kind: Explicit, Session: s}, nil
	}
	var found []Harness
	if id, file := getenv("PI_SESSION_ID"), getenv("PI_SESSION_FILE"); id != "" && file != "" {
		found = append(found, Harness{Kind: Pi, Session: id, File: file})
	}
	if id := getenv("CLAUDE_CODE_SESSION_ID"); id != "" {
		found = append(found, Harness{Kind: ClaudeCode, Session: id})
	}
	if id := getenv("HERMES_SESSION_ID"); id != "" {
		found = append(found, Harness{Kind: Hermes, Session: id})
	}
	switch len(found) {
	case 0:
		return Harness{}, ErrNoIdentity
	case 1:
		return found[0], nil
	}
	kind := ownerKind(owner(), HermesHome(getenv))
	for _, want := range []Kind{kind, Pi} {
		for _, h := range found {
			if h.Kind == want {
				return h, nil
			}
		}
	}
	return Harness{}, errAmbiguous
}

func ownerKind(exe, hermes string) Kind {
	switch {
	case filepath.Base(exe) == "claude":
		return ClaudeCode
	case exe != "" && hermes != "" && strings.HasPrefix(exe, hermes+string(filepath.Separator)):
		return Hermes
	}
	return Pi
}

func (h Harness) Record(getenv func(string) string, events string) (string, witness.Format) {
	switch h.Kind {
	case Pi:
		return h.File, witness.PiSession
	case ClaudeCode:
		return claudeTranscript(getenv, h.Session), witness.ClaudeTranscript
	case Hermes:
		return events, witness.HermesEvents
	}
	return "", ""
}

func claudeTranscript(getenv func(string) string, session string) string {
	if session == "" || strings.ContainsAny(session, `*?[]\/`) {
		return ""
	}
	dir := getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home := getenv("HOME")
		if home == "" {
			return ""
		}
		dir = filepath.Join(home, ".claude")
	}
	matches, err := filepath.Glob(filepath.Join(dir, "projects", "*", session+".jsonl"))
	if err != nil || len(matches) != 1 {
		return ""
	}
	return matches[0]
}

func HermesHome(getenv func(string) string) string {
	if home := getenv("HERMES_HOME"); home != "" {
		return filepath.Clean(home)
	}
	if home := getenv("HOME"); home != "" {
		return filepath.Join(home, ".hermes")
	}
	return ""
}

func (h Harness) Deadline(getenv func(string) string, within time.Duration) time.Duration {
	var standard, ceiling time.Duration
	switch h.Kind {
	case ClaudeCode:

		standard = setting(getenv, "BASH_DEFAULT_TIMEOUT_MS", time.Millisecond, 2*time.Minute)
		ceiling = max(standard, setting(getenv, "BASH_MAX_TIMEOUT_MS", time.Millisecond, 10*time.Minute))
	case Hermes:
		standard = 3 * time.Minute
		ceiling = setting(getenv, "TERMINAL_MAX_FOREGROUND_TIMEOUT", time.Second, 10*time.Minute)
	default:
		return within
	}
	if within <= 0 {
		return min(standard, ceiling)
	}
	return min(within, ceiling)
}

func setting(getenv func(string) string, name string, unit, fallback time.Duration) time.Duration {
	n, err := strconv.ParseInt(strings.TrimSpace(getenv(name)), 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * unit
}
