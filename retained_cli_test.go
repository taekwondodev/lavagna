package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundDefersFeedbackAndReadsItLosslesslyBeforeClose(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=retained-feedback")
	c := startRound(t, environ, decision)
	round, token := c.view()
	texts := []string{strings.Repeat("<>&\x01🧭", 350), "Only this comment should be read when selected."}
	body, _ := json.Marshal(map[string]any{"round": round, "token": token, "submission": "s-1122334455667788", "choices": map[string]string{"storage": "file"}, "comments": []map[string]string{{"text": texts[0]}, {"text": texts[1]}}})
	if status := c.post(c.url+"send", c.origin, "application/json", string(body)); status != http.StatusAccepted {
		t.Fatalf("send %d", status)
	}
	lines, code := c.finish()
	if code != 0 || len(lines) != 1 || len(lines[0]) > 2048 || !strings.Contains(lines[0], `"deferred":true`) || !strings.Contains(lines[0], `"counts":{"choices":1,"comments":2,"images":0}`) {
		t.Fatalf("deferred exit %d: %q", code, lines)
	}
	lines, code = run(t, environ, "", "feedback", "s-1122334455667788")
	if code != 0 || !strings.Contains(lines[0], `"choice":"storage","value":"file"`) || !strings.Contains(lines[0], `"comment":2`) || strings.Contains(lines[0], texts[1]) {
		t.Fatalf("overview exit %d: %q", code, lines)
	}
	for i, want := range texts {
		var got strings.Builder
		for offset := 0; ; {
			lines, code = run(t, environ, "", "feedback", "s-1122334455667788", "--comment", fmt.Sprint(i+1), "--offset", fmt.Sprint(offset))
			if code != 0 || len(lines) != 1 || len(lines[0]) > 4096 {
				t.Fatalf("page exit %d, %q", code, lines)
			}
			var page struct {
				Text         string
				Next, Length int
			}
			if err := json.Unmarshal([]byte(lines[0]), &page); err != nil {
				t.Fatal(err)
			}
			got.WriteString(page.Text)
			if page.Length != len(want) {
				t.Fatalf("length %d, want %d", page.Length, len(want))
			}
			if page.Next == 0 {
				break
			}
			if page.Next <= offset {
				t.Fatal("page did not advance")
			}
			offset = page.Next
		}
		if got.String() != want {
			t.Fatal("comment text was changed or lost")
		}
	}
	lines, code = run(t, environ, "", "feedback", "s-1122334455667788", "--all")
	var full struct{ Comments []struct{ Text string } }
	if code != 0 || json.Unmarshal([]byte(lines[0]), &full) != nil || len(full.Comments) != 2 || full.Comments[0].Text != texts[0] || full.Comments[1].Text != texts[1] {
		t.Fatal("explicit full read lost feedback")
	}
	for _, args := range [][]string{{"--all", "--offset", "0"}, {"--comment", "0"}, {"--comment", "1", "--offset", "5"}, {"--comment", "3"}, {"--offset", "9999"}, {"--offset", "-1"}} {
		if lines, code := run(t, environ, "", append([]string{"feedback", "s-1122334455667788"}, args...)...); code != 2 {
			t.Fatalf("invalid selector %q: exit %d, %q", args, code, lines)
		}
	}
	for _, reference := range []string{"s-1122334455667788", "../state", "s-00000000"} {
		if _, code := run(t, env(t, "LAVAGNA_SESSION=foreign"), "", "feedback", reference); code != 2 {
			t.Fatalf("foreign reference %s: exit %d", reference, code)
		}
	}
	if _, code := run(t, environ, "", "close"); code != 0 {
		t.Fatal("close failed")
	}
	if _, code := run(t, environ, "", "feedback", "s-1122334455667788"); code != 2 {
		t.Fatal("feedback survived close")
	}
}

func TestRoundRecoversAfterSnapshotPublicationFails(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=snapshot-failure")
	dir := filepath.Dir(filepath.Dir(sendScreenshot(t, environ, screenshot(t))))
	if err := os.WriteFile(filepath.Join(dir, "artifacts", "round", "r2.json"), []byte("collision"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, code := run(t, environ, decision, "round")
	if code != 1 || !strings.Contains(lines[0], `"lavagna":"error"`) {
		t.Fatalf("publication should fail after reservation: %d, %q", code, lines)
	}
	next := startRound(t, environ, decision)
	round, _ := next.view()
	if round != "r3" {
		t.Fatalf("reserved failed round was not skipped: %s", round)
	}
	sendAndReturn(t, next, "s-4455667788990011")
	if _, code := run(t, environ, "", "close"); code != 0 {
		t.Fatal("close failed")
	}
}

func frameURL(t *testing.T, c *call) string {
	t.Helper()
	response, err := http.Get(c.url + "events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			var view struct{ Frame string }
			if err := json.Unmarshal([]byte(data), &view); err != nil {
				t.Fatal(err)
			}
			return c.origin + view.Frame
		}
	}
	t.Fatal("missing view")
	return ""
}

func getText(t *testing.T, url string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("get %s: status %d, %v", url, response.StatusCode, err)
	}
	return string(body)
}

func TestReuseKeepsAssetsAndExcerptsButNotDecisions(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=reuse")
	root := t.TempDir()
	for _, dir := range []string{".git", "round"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeFiles(t, root, map[string]string{"source.go": "frozen excerpt\n"})
	source := "# Capire\n## Prototype {ref=\"Prototype\"}\n::: excerpt source.go:1-1\n:::\n<img src=\"diagram.svg\" alt=\"diagram\">\n\n# Decidere\n## Old question {id=\"old\"}\n- [yes] Yes\n- [no] No\n"
	dir := filepath.Join(root, "round")
	writeFiles(t, dir, map[string]string{"round.md": source, "diagram.svg": "<svg>frozen asset</svg>"})
	cmd := command(environ, "", dir)
	cmd.Dir = root
	first := spawnCommand(t, cmd)
	before := getText(t, frameURL(t, first))
	r, token := first.view()
	first.post(first.url+"send", first.origin, "application/json", fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-2233445566778899","choices":{"old":"yes"}}`, r, token))
	if _, code := first.finish(); code != 0 {
		t.Fatalf("first exit %d", code)
	}
	if err := os.Remove(filepath.Join(root, "source.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "diagram.svg")); err != nil {
		t.Fatal(err)
	}
	newDecisions := "# Decidere\n## New question {id=\"new\"}\n- [ship] Ship\n- [wait] Wait\n"
	second := spawn(t, environ, newDecisions, "--reuse", "r1")
	frame := frameURL(t, second)
	if after := getText(t, frame); after != before || !strings.Contains(after, "frozen excerpt") {
		t.Fatal("reuse changed the rendered content")
	}
	if asset := getText(t, frame+"diagram.svg"); asset != "<svg>frozen asset</svg>" {
		t.Fatalf("asset %q", asset)
	}
	r, token = second.view()
	post := func(choice string) int {
		return second.post(second.url+"send", second.origin, "application/json", fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-3344556677889900","choices":%s,"comments":[{"anchor":"Prototype","text":"keep this"}]}`, r, token, choice))
	}
	if status := post(`{"old":"yes"}`); status != http.StatusBadRequest {
		t.Fatalf("inherited old decision: %d", status)
	}
	if status := post(`{"new":"ship"}`); status != http.StatusAccepted {
		t.Fatalf("new decision/retained anchor: %d", status)
	}
	if _, code := second.finish(); code != 0 {
		t.Fatalf("reuse exit %d", code)
	}
	for _, tc := range []struct{ source, base string }{{decision, "r1"}, {newDecisions, "../state"}, {newDecisions, "r999"}} {
		if _, code := run(t, environ, tc.source, "round", "--reuse", tc.base); code != 2 {
			t.Fatalf("invalid reuse %q: %d", tc.base, code)
		}
	}
	if _, code := run(t, environ, "", "close"); code != 0 {
		t.Fatal("close failed")
	}
	if _, code := run(t, environ, newDecisions, "round", "--reuse", "r1"); code != 2 {
		t.Fatal("snapshot survived close")
	}
	if _, err := os.Stat(filepath.Join(dir, "round.md")); err != nil {
		t.Fatal("close removed the source document", err)
	}
}
