package harness

import (
	"errors"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

func environ(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestResolveBindsTheHarnessThatOwnsTheCall(t *testing.T) {
	pi := map[string]string{"PI_SESSION_ID": "p", "PI_SESSION_FILE": "/s/p.jsonl"}
	with := func(base map[string]string, more ...string) map[string]string {
		out := map[string]string{"HOME": "/Users/u"}
		for k, v := range base {
			out[k] = v
		}
		for i := 0; i < len(more); i += 2 {
			out[more[i]] = more[i+1]
		}
		return out
	}
	cases := []struct {
		name  string
		vars  map[string]string
		owner string // empty: the owner must not be consulted
		want  Harness
		err   error
	}{
		{"explicit wins over every harness", with(pi, "LAVAGNA_SESSION", "x", "CLAUDE_CODE_SESSION_ID", "c", "HERMES_SESSION_ID", "h"), "", Harness{Kind: Explicit, Session: "x"}, nil},
		{"pi", with(pi), "", Harness{Kind: Pi, Session: "p", File: "/s/p.jsonl"}, nil},
		{"claude code", with(nil, "CLAUDE_CODE_SESSION_ID", "c"), "", Harness{Kind: ClaudeCode, Session: "c"}, nil},
		{"hermes", with(nil, "HERMES_SESSION_ID", "h"), "", Harness{Kind: Hermes, Session: "h"}, nil},
		{"pi id without file", with(nil, "PI_SESSION_ID", "p"), "", Harness{}, ErrNoIdentity},
		{"nothing", with(nil), "", Harness{}, ErrNoIdentity},
		{"claude code inside pi", with(pi, "CLAUDE_CODE_SESSION_ID", "c"), "claude", Harness{Kind: ClaudeCode, Session: "c"}, nil},
		{"pi inside claude code", with(pi, "CLAUDE_CODE_SESSION_ID", "c"), "node", Harness{Kind: Pi, Session: "p", File: "/s/p.jsonl"}, nil},
		{"hermes inside pi", with(pi, "HERMES_SESSION_ID", "h"), "/Users/u/.hermes/tools/python-3/bin/python3", Harness{Kind: Hermes, Session: "h"}, nil},
		{"hermes under its own home", with(pi, "HERMES_SESSION_ID", "h", "HERMES_HOME", "/opt/hermes/"), "/opt/hermes/bin/python3", Harness{Kind: Hermes, Session: "h"}, nil},
		{"unrecognized owner without pi", with(nil, "CLAUDE_CODE_SESSION_ID", "c", "HERMES_SESSION_ID", "h"), "node", Harness{}, errAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			owner := func() string {
				if c.owner == "" {
					t.Fatal("owner consulted without competing identities")
				}
				return c.owner
			}
			got, err := Resolve(environ(c.vars), owner)
			if !errors.Is(err, c.err) || got != c.want {
				t.Fatalf("Resolve = %+v, %v; want %+v, %v", got, err, c.want, c.err)
			}
		})
	}
}

func TestIdentityKeepsKeysFromBeforeAdapters(t *testing.T) {
	cases := []struct {
		h   Harness
		key string
	}{
		{Harness{Kind: Pi, Session: "abc", File: "/tmp/s.jsonl"}, "22da109a12426f36"},
		{Harness{Kind: Explicit, Session: "other-harness"}, "1dc045312c527761"},
	}
	for _, c := range cases {
		conv, err := conversation.For(c.h.Identity())
		if err != nil {
			t.Fatal(err)
		}
		if conv.Key != c.key {
			t.Errorf("%s key %s, want %s", c.h.Kind, conv.Key, c.key)
		}
	}
	a, _ := conversation.For(Harness{Kind: ClaudeCode, Session: "same"}.Identity())
	b, _ := conversation.For(Harness{Kind: Hermes, Session: "same"}.Identity())
	if a.Key == b.Key {
		t.Fatal("one session id binds the same conversation in two harnesses")
	}
}

func TestDeadlineFollowsTheHarnessLimit(t *testing.T) {
	cases := []struct {
		name   string
		kind   Kind
		vars   map[string]string
		within time.Duration
		want   time.Duration
	}{
		{"pi waits without limit", Pi, nil, 0, 0},
		{"pi honors a declared deadline", Pi, nil, time.Hour, time.Hour},
		{"explicit waits without limit", Explicit, nil, 0, 0},
		{"claude code default", ClaudeCode, nil, 0, 2 * time.Minute},
		{"claude code configured default", ClaudeCode, map[string]string{"BASH_DEFAULT_TIMEOUT_MS": "300000"}, 0, 5 * time.Minute},
		{"claude code declared", ClaudeCode, nil, 8 * time.Minute, 8 * time.Minute},
		{"claude code clamps to its maximum", ClaudeCode, nil, time.Hour, 10 * time.Minute},
		{"claude code configured maximum", ClaudeCode, map[string]string{"BASH_MAX_TIMEOUT_MS": "1800000"}, time.Hour, 30 * time.Minute},
		{"claude code ceiling is at least the default", ClaudeCode, map[string]string{"BASH_DEFAULT_TIMEOUT_MS": "900000"}, time.Hour, 15 * time.Minute},
		{"claude code ignores a malformed setting", ClaudeCode, map[string]string{"BASH_DEFAULT_TIMEOUT_MS": "soon"}, 0, 2 * time.Minute},
		{"hermes default", Hermes, nil, 0, 3 * time.Minute},
		{"hermes clamps to its maximum", Hermes, nil, time.Hour, 10 * time.Minute},
		{"hermes configured maximum", Hermes, map[string]string{"TERMINAL_MAX_FOREGROUND_TIMEOUT": "60"}, 5 * time.Minute, time.Minute},
		{"hermes default under a low maximum", Hermes, map[string]string{"TERMINAL_MAX_FOREGROUND_TIMEOUT": "60"}, 0, time.Minute},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Harness{Kind: c.kind}).Deadline(environ(c.vars), c.within); got != c.want {
				t.Fatalf("Deadline = %v, want %v", got, c.want)
			}
		})
	}
}
