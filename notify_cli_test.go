package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeNotifier installs a helper where make install puts Lavagna.app. It
// records the argument count and arguments of each submission, then exits
// with code. It returns a reader of the recorded submissions.
func fakeNotifier(t *testing.T, environ []string, code int) func() []string {
	t.Helper()
	home := ""
	for _, kv := range environ {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	log := filepath.Join(home, "notified")
	helper := filepath.Join(home, "Applications", "Lavagna.app", "Contents", "MacOS", "lavagna-notifier")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s\\n' \"$#\" \"$*\" >> %q\nexit %d\n", log, code)
	writeFiles(t, filepath.Dir(helper), map[string]string{"lavagna-notifier": script})
	if err := os.Chmod(helper, 0o755); err != nil {
		t.Fatal(err)
	}
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.FieldsFunc(string(b), func(r rune) bool { return r == '\n' })
	}
}

// notified waits until want submissions are recorded, then confirms that no
// further one follows.
func notified(t *testing.T, read func() []string, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(read()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if got := read(); !slices.Equal(got, want) {
		t.Fatalf("notifications\n%q\nwant\n%q", got, want)
	}
}

func TestEachContentCallNotifiesOnceBeforeFeedback(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=notify")
	read := fakeNotifier(t, environ, 0)
	var want []string
	expect := func(c *call, kind string) {
		t.Helper()
		want = append(want, "2|"+kind+" "+c.url)
		notified(t, read, want)
	}

	hostile := "# Titolo \"; do shell script \"touch /tmp/lavagna-pwned\" $(touch /tmp/lavagna-pwned) {id=\"storage\"}\n## Decidere\n- [file] Un file\n- [db] Un database\n# Dopo {id=\"later\"}\n"
	first := startRound(t, environ, hostile)
	expect(first, "first")
	first.page()
	first.page()
	notified(t, read, want)
	first.answer("s-0000000000000001", `{"storage":{"choice":"file"}}`)
	notified(t, read, want)

	for _, src := range []string{
		"::: reply storage\nRisposta con http://127.0.0.1:1/s/x/ e `rm -rf ~`.\n:::\n",
		"::: reply\nSolo panoramica.\n:::\n",
		"# Dopo, rinominata {id=\"later\"}\n",
		strings.Replace(hostile, "Un database", "SQLite", 1),
		"::: settled storage file\nDeciso.\n:::\n",
		"::: reply later\nUna.\n:::\n# Uno {id=\"one\"}\n## Decidere\n- [a] A\n- [b] B\n# Due {id=\"two\"}\n## Decidere\n- [a] A\n- [b] B\n",
	} {
		c := startRound(t, environ, src)
		expect(c, "update")
		c.answer(fmt.Sprintf("s-%016x", len(want)), `{"storage":{"messages":["ok"]}}`)
		notified(t, read, want)
	}

	resumed := startRound(t, environ, "")
	resumed.page()
	notified(t, read, want)
	resumed.answer("s-00000000000000aa", `{"storage":{"messages":["ok"]}}`)
	if lines, code := run(t, environ, "::: reply ghost\nx\n:::\n", "round"); code != 2 {
		t.Fatalf("invalid call: %d %q", code, lines)
	}
	notified(t, read, want)
	if _, err := os.Stat("/tmp/lavagna-pwned"); err == nil {
		t.Fatal("authored text ran a command")
	}

	if lines, code := run(t, environ, "", "close"); code != 0 {
		t.Fatalf("close: %d %q", code, lines)
	}
	expect(startRound(t, environ, decision), "first")
}

func TestARefusedNotificationLeavesTheRoundUnchanged(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=refused")
	read := fakeNotifier(t, environ, 4)
	cmd := command(environ, decision)
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &call{t: t, cmd: cmd, out: bufio.NewReader(stdout)}
	t.Cleanup(c.esc)
	refusal := "lavagna: notification not sent; Lavagna notifications are off in System Settings > Notifications\n"
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stderr.String(), refusal) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	lines := strings.SplitAfter(stderr.String(), "\n")
	c.status(lines[0])
	if len(read()) != 1 || len(lines) != 3 || lines[1] != refusal {
		t.Fatalf("stderr %q after %q", stderr.String(), read())
	}
	got := c.answer("s-0123456789abcdef", `{"storage":{"choice":"db"}}`)
	if want := `{"lavagna":"feedback","round":"r1","submission":"s-0123456789abcdef","questions":{"storage":{"choice":"db"}}}`; got != want {
		t.Fatalf("outcome %s", got)
	}
	if strings.Contains(stderr.String()[len(lines[0]):], c.url) {
		t.Fatalf("diagnostics expose the page: %q", stderr.String())
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
