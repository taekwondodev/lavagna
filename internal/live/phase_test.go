package live

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

type lineChannel chan string

func (w lineChannel) Write(p []byte) (int, error) { w <- string(p); return len(p), nil }

func TestPhaseRoundRejectsStoredOverflowWithoutPublishingPartialArtifacts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	getenv := func(key string) string {
		if key == "LAVAGNA_SESSION" {
			return "phase-overflow-test"
		}
		return ""
	}
	source := "# Large {id=\"large\"}\n## Capire\n" + strings.Repeat("x", 3<<20) + "\n## Decidere\n- [a] A\n- [b] B\n"
	var out bytes.Buffer
	if code := PhaseRound(getenv, strings.NewReader(source), "", &out, io.Discard); code != exitInvalid || !strings.Contains(out.String(), "6 MiB storage bound") {
		t.Fatalf("overflow result code %d: %s", code, out.String())
	}
	conv, err := conversation.FromEnv(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	state, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	_, artifactErr := lease.ReadArtifact("question", "large-1", 6<<20)
	lease.Release()
	if state.Format != 0 || !errors.Is(artifactErr, fs.ErrNotExist) {
		t.Fatalf("failed input mutated state/artifacts: state=%+v artifact=%v", state, artifactErr)
	}

	// JSON stores []byte resources as base64. Their serialized size must count
	// toward the same 6 MiB artifact bound, alongside source and rendered HTML.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "large"), 0o700); err != nil {
		t.Fatal(err)
	}
	resource := bytes.Repeat([]byte{0}, (1750 << 10))
	if err := os.WriteFile(filepath.Join(dir, "large", "image.png"), resource, 0o600); err != nil {
		t.Fatal(err)
	}
	sourceWithResource := "# Large {id=\"large\"}\n## Capire\n" + strings.Repeat("x", 2<<20) + "\n<img src=\"large/image.png\" alt=\"\">\n## Decidere\n- [a] A\n- [b] B\n"
	if err := os.WriteFile(filepath.Join(dir, "round.md"), []byte(sourceWithResource), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := PhaseRound(getenv, strings.NewReader(sourceWithResource), dir, &out, io.Discard); code != exitInvalid || !strings.Contains(out.String(), "6 MiB storage bound") {
		t.Fatalf("resource serialization bound: %d %s", code, out.String())
	}
	lease, err = conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	_, artifactErr = lease.ReadArtifact("question", "large-1", 6<<20)
	lease.Release()
	if !errors.Is(artifactErr, fs.ErrNotExist) {
		t.Fatalf("over-bound resource artifact was published: %v", artifactErr)
	}
}

func TestPhaseRoundStoresPrivateQuestionVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	getenv := func(key string) string {
		switch key {
		case "HOME":
			return home
		case "LAVAGNA_SESSION":
			return "phase-artifact-test"
		case "BROWSER":
			return "true"
		}
		return ""
	}
	source := `# Crash handling {id="crash"}
## Capire
A partial write can corrupt the state.
## Decidere
- [atomic] Write atomically {recommended}
- [discard] Restart from scratch
# Later decision {id="later"}
`
	out := &bytes.Buffer{}
	status := make(lineChannel, 1)
	done := make(chan int, 1)
	go func() { done <- PhaseRound(getenv, strings.NewReader(source), "", out, status) }()
	first := <-status
	fields := strings.Fields(first)
	if len(fields) < 3 {
		t.Fatalf("status line %q", first)
	}
	urlText := ""
	for _, field := range fields {
		if strings.HasPrefix(field, "http://") {
			urlText = field
			break
		}
	}
	pageURL, err := url.Parse(urlText)
	if err != nil || urlText == "" {
		t.Fatalf("status URL %q: %v", urlText, err)
	}
	resp, err := http.Get(pageURL.String() + "events")
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, 1<<20)
	var view struct {
		Round string `json:"round"`
		Token string `json:"token"`
		Phase struct {
			Questions []struct {
				ID      string `json:"id"`
				Planned bool   `json:"planned"`
				Frame   string `json:"frame"`
			} `json:"questions"`
		} `json:"phase"`
	}
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			if err := json.Unmarshal([]byte(data), &view); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	resp.Body.Close()
	if view.Round != "r1" || len(view.Phase.Questions) != 2 || view.Phase.Questions[0].Frame == "" || !view.Phase.Questions[1].Planned {
		t.Fatalf("round event did not expose per-question frames and planned question: %+v", view)
	}
	body := fmt.Sprintf(`{"round":%q,"token":%q,"submission":"s-0123456789abcdef","questions":{"crash":{"choice":"atomic","messages":["keep the old copy"]}}}`, view.Round, view.Token)
	req, _ := http.NewRequest(http.MethodPost, pageURL.String()+"send", strings.NewReader(body))
	req.Header.Set("Origin", "http://"+pageURL.Host)
	req.Header.Set("Content-Type", "application/json")
	sent, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, sent.Body)
	sent.Body.Close()
	if sent.StatusCode != http.StatusAccepted {
		t.Fatalf("send status %d", sent.StatusCode)
	}
	if code := <-done; code != 0 {
		t.Fatalf("round exit %d: %s", code, out.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("result %q: %v", out.String(), err)
	}
	questions, ok := result["questions"].(map[string]any)
	if !ok || questions["crash"] == nil {
		t.Fatalf("question-grouped result: %s", out.String())
	}
	conv, err := conversation.FromEnv(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	state, err := lease.Load()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := lease.ReadArtifact("question", "crash-1", 6<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, plannedErr := lease.ReadArtifact("question", "later-0", 6<<20)
	lease.Release()
	if state.Format != 1 || state.Ledger.Questions["crash"].Version != 1 || state.Ledger.Questions["crash"].Status != "open" || state.Ledger.Questions["later"].Version != 0 || state.Ledger.Questions["later"].Status != "planned" {
		t.Fatalf("state ledger: %+v", state)
	}
	if !errors.Is(plannedErr, fs.ErrNotExist) {
		t.Fatalf("planned question unexpectedly has an artifact: %v", plannedErr)
	}
	if !bytes.Contains(artifact, []byte("partial write")) {
		t.Fatalf("artifact omitted source: %s", artifact)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(cache, "lavagna", conv.Key, "artifacts", "question", "crash-1.json")
	info, err := os.Stat(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode %o", info.Mode().Perm())
	}
	for _, directory := range []string{filepath.Dir(artifactPath), filepath.Join(cache, "lavagna", conv.Key)} {
		dirInfo, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Errorf("directory %s mode %o", directory, dirInfo.Mode().Perm())
		}
	}
}

func TestPhaseRoundRefusesCallsBeyondThePhaseBound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	getenv := func(key string) string {
		if key == "LAVAGNA_SESSION" {
			return "phase-bound-test"
		}
		return ""
	}
	conv, err := conversation.FromEnv(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	near := conversation.State{Format: conversation.Format, Ledger: conversation.Ledger{Round: 1, Order: []string{"big"}, Questions: map[string]conversation.Question{
		"big": {Title: "Big", Status: conversation.Open, Round: 1, Version: 1, Bytes: conversation.PhaseBytes - 1024, Options: []conversation.Option{{ID: "a", Label: "A"}, {ID: "b", Label: "B"}}},
	}}}
	if err := lease.Save(near); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	source := "# Next {id=\"next\"}\n## Capire\n" + strings.Repeat("x", 2048) + "\n## Decidere\n- [a] A\n- [b] B\n"
	var out bytes.Buffer
	if code := PhaseRound(getenv, strings.NewReader(source), "", &out, io.Discard); code != exitInvalid || !strings.Contains(out.String(), "16 MiB phase bound") {
		t.Fatalf("phase bound: %d %s", code, out.String())
	}
	lease, err = conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if got, err := lease.Load(); err != nil || got.Calls != 0 || len(got.Ledger.Questions) != 1 {
		t.Fatalf("refused call changed the ledger: %+v %v", got, err)
	}
	if _, err := lease.ReadArtifact("question", "next-1", 6<<20); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("refused call stored an artifact: %v", err)
	}
}

func TestRetainReplacesAResentSubmissionRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	conv, err := conversation.FromEnv(func(key string) string {
		if key == "LAVAGNA_SESSION" {
			return "retain-test"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(conv)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	for _, record := range []string{`{"round":"r1"}`, `{"round":"r2"}`} {
		if err := retain(lease, "s-12345678", []byte(record)); err != nil {
			t.Fatal(err)
		}
	}
	if b, err := lease.ReadArtifact("feedback", "s-12345678", maxResultBytes); err != nil || string(b) != `{"round":"r2"}` {
		t.Fatalf("resent record: %s %v", b, err)
	}
}
