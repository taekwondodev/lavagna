package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestMinimalHelpIsSufficientForOneRound(t *testing.T) {
	lines, code := run(t, env(t), "", "round", "--help")
	help := strings.Join(lines, "\n")
	if code != 0 || len(help) > 1200 || !strings.Contains(help, "--help grammar") || !strings.Contains(help, "feedback --help") || !strings.Contains(help, "unanswered, not approved") || !strings.Contains(help, "only close ends the phase") {
		t.Fatalf("exit %d: %s", code, help)
	}
	start := strings.Index(help, "# Decidere\n")
	if start < 0 {
		t.Fatal("no runnable example")
	}
	example := strings.SplitN(help[start:], "\n\n", 2)[0]
	c := startRound(t, env(t, "LAVAGNA_SESSION=minimal"), example)
	round, token := c.view()
	body := fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-0123456789abcdef","choices":{"storage":"file"}}`, round, token)
	if status := c.post(c.url+"send", c.origin, "application/json", body); status != http.StatusAccepted {
		t.Fatalf("send %d", status)
	}
	output, code := c.finish()
	if code != 0 || len(output) != 1 || !json.Valid([]byte(output[0])) || !strings.Contains(output[0], `"storage":"file"`) {
		t.Fatalf("stdout must be one complete machine result: exit %d, %q", code, output)
	}
}

func TestCheckReturnsOnlyMachineReadiness(t *testing.T) {
	lines, code := run(t, env(t, "LAVAGNA_SESSION=ready"), "", "check")
	if code != 0 || len(lines) != 1 || lines[0] != `{"lavagna":"ready"}` {
		t.Fatalf("exit %d, %q", code, lines)
	}
}

func TestInvalidDiagnosticsStayBoundedAndActionable(t *testing.T) {
	lines, code := run(t, env(t, "LAVAGNA_SESSION=diagnostics"), "# Capire\n"+strings.Repeat("::: "+strings.Repeat("界", 1000)+"\n:::\n", 50), "round")
	if code != 2 || len(lines) != 1 || len(lines[0]) > 4096 {
		t.Fatalf("exit %d, lines %d, bytes %d", code, len(lines), len(lines[0]))
	}
	var got struct {
		Errors []string `json:"errors"`
		More   int      `json:"more"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Errors) != 8 || got.More != 42 || !strings.HasPrefix(got.Errors[0], "round.md:2: unknown block") || !strings.HasSuffix(got.Errors[0], "...") {
		t.Fatalf("diagnostics: %+v", got)
	}
}
