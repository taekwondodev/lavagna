package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRexRoundRoutesThePageAndNotificationWithoutChangingFeedback(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("browser_override=%v", override), func(t *testing.T) {
			environ := env(t, "LAVAGNA_SESSION=rex-round")
			tools := t.TempDir()
			log := filepath.Join(tools, "rex-calls")
			writeFiles(t, tools, map[string]string{
				"rex": fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" >> %q
for arg in "$@"; do
  case "$arg" in
    session.list_blocks)
      printf '{"blocks":[{"block_id":"block:22222222-2222-4222-8222-222222222222","placement":"placed","creator_name":"com.superlogical.terminal"}'
      if [ -f %q ]; then printf ','; cat %q; fi
      printf ']}'; exit 0 ;;
    session.focus_block|client.perform) printf '{}'; exit 0 ;;
    client.list)
      printf '{"clients":[{"client_id":"client:33333333-3333-4333-8333-333333333333","info":{"kind":"app","platform":"macos"},"session_ids":["session:11111111-1111-4111-8111-111111111111"]}]}'; exit 0 ;;
    run)
      previous=''
      for value in "$@"; do
        if [ "$previous" = '--label' ]; then label=$value; fi
        previous=$value
      done
      printf '{"block_id":"block:11111111-1111-4111-8111-111111111111","label":"%%s","placement":"placed","creator_name":"com.superlogical.terminal","owner":"client:33333333-3333-4333-8333-333333333333","window_id":"window:44444444-4444-4444-8444-444444444444"}' "$label" > %q
      printf '{}'; exit 0 ;;
  esac
done
exit 1
`, log, filepath.Join(tools, "panel"), filepath.Join(tools, "panel"), filepath.Join(tools, "panel")),
				"terminal-browser": "#!/bin/sh\nexit 0\n",
				"open":             fmt.Sprintf("#!/bin/sh\nprintf 'FALLBACK\\n' >> %q\n", log),
			})
			for _, name := range []string{"rex", "terminal-browser", "open"} {
				if err := os.Chmod(filepath.Join(tools, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			browser := "BROWSER="
			if override {
				browser = "BROWSER=true"
			}
			environ = append(environ, browser, "PATH="+tools+":"+os.Getenv("PATH"), "REX_BIN_DIR="+tools,
				"REX_SERVER=unix:///tmp/lavagna-rex-test.sock",
				"REX_SESSION=session:11111111-1111-4111-8111-111111111111",
				"REX_BLOCK=block:22222222-2222-4222-8222-222222222222")
			read := fakeNotifier(t, environ, 0)
			c := startRound(t, environ, decision)
			c.page()
			deadline := time.Now().Add(5 * time.Second)
			for len(read()) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			lines := read()
			if len(lines) != 1 {
				t.Fatalf("notifications: %q", lines)
			}
			if override {
				if want := "2|first " + c.url; lines[0] != want {
					t.Fatalf("override notification: %q, want %q", lines[0], want)
				}
				if calls, _ := os.ReadFile(log); len(calls) != 0 {
					t.Fatalf("BROWSER override consulted Rex: %s", calls)
				}
			} else {
				prefix := "4|first " + c.url + " --rex "
				payload, ok := strings.CutPrefix(lines[0], prefix)
				if !ok || !json.Valid([]byte(payload)) || strings.Contains(payload, c.url) {
					t.Fatalf("notification lacks opaque Rex destination: %q", lines[0])
				}
				for time.Now().Before(deadline) {
					calls, _ := os.ReadFile(log)
					if strings.Contains(string(calls), "\nrun\n") {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				calls, _ := os.ReadFile(log)
				if !strings.Contains(string(calls), "\nrun\n") {
					t.Fatalf("Rex did not open the page despite an existing browser connection: %s", calls)
				}
				click := exec.Command(binary, "rex-click", c.url, payload)
				click.Env = environ
				if out, err := click.CombinedOutput(); err != nil || len(out) != 0 {
					t.Fatalf("notification callback: %v, %s", err, out)
				}
				bad := exec.Command(binary, "rex-click", c.url, "not-json")
				bad.Env = environ
				out, err := bad.CombinedOutput()
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 ||
					!strings.Contains(string(out), "cannot show the Rex panel") || strings.Contains(string(out), c.url) {
					t.Fatalf("invalid callback must fail safely: %v, %s", err, out)
				}
				calls, _ = os.ReadFile(log)
				if !strings.Contains(string(calls), "session.focus_block") || !strings.Contains(string(calls), "client.perform") ||
					strings.Count(string(calls), "\nrun\n") != 1 || strings.Contains(string(calls), "FALLBACK") {
					t.Fatalf("callback must focus without duplicate creation or browser fallback: %s", calls)
				}
			}
			got := c.answer("s-0123456789abcdef", `{"storage":{"choice":"file"}}`)
			want := `{"lavagna":"feedback","round":"r1","submission":"s-0123456789abcdef","questions":{"storage":{"choice":"file"}}}`
			if got != want {
				t.Fatalf("feedback: %s, want %s", got, want)
			}
		})
	}
}
