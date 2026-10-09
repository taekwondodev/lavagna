package live

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

func TestFeedbackSelectorsReadQuestionGroupedRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	getenv := func(k string) string {
		if k == "LAVAGNA_SESSION" {
			return "phase-feedback"
		}
		return ""
	}
	_, c, err := bound(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	line := phaseOutcomeLine{Lavagna: "feedback", Round: "r1", Submission: "s-abcdef12", Questions: map[string]phaseQuestionLine{
		"crash":     {Choice: "journal", Messages: []string{strings.Repeat("full text ", 230)}, Images: []string{"/tmp/crash.png"}},
		"retention": {Answer: "two days"},
	}, Overview: &phaseQuestionLine{Messages: []string{"clear"}}}
	encoded, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.StoreArtifact("feedback", line.Submission, encoded); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	read := func(req FeedbackRequest) string {
		var out bytes.Buffer
		if code := Feedback(getenv, &out, req); code != 0 {
			t.Fatalf("read code %d: %s", code, out.String())
		}
		return out.String()
	}
	var summary phaseOutcomeLine
	if err := json.Unmarshal([]byte(read(FeedbackRequest{Submission: line.Submission})), &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.Deferred || summary.Questions["crash"].Choice != "journal" || summary.Questions["crash"].Messages != float64(1) || summary.Questions["retention"].Answer != true {
		t.Fatalf("summary: %+v", summary)
	}
	var question struct {
		Question string   `json:"question"`
		Choice   string   `json:"choice"`
		Messages []string `json:"messages"`
		Images   []string `json:"images"`
	}
	if err := json.Unmarshal([]byte(read(FeedbackRequest{Submission: line.Submission, Question: "crash"})), &question); err != nil {
		t.Fatal(err)
	}
	if question.Question != "crash" || question.Choice != "journal" || len(question.Messages) != 1 || question.Images[0] != "/tmp/crash.png" {
		t.Fatalf("question read: %+v", question)
	}
	if got := read(FeedbackRequest{Submission: line.Submission, Overview: true}); !strings.Contains(got, `"messages":["clear"]`) {
		t.Fatalf("overview: %s", got)
	}
	var all phaseOutcomeLine
	if err := json.Unmarshal([]byte(read(FeedbackRequest{Submission: line.Submission, All: true})), &all); err != nil || len(all.Questions) != 2 {
		t.Fatalf("all: %+v, %v", all, err)
	}
	var out bytes.Buffer
	if code := Feedback(getenv, &out, FeedbackRequest{Submission: line.Submission, Question: "missing"}); code != exitInvalid {
		t.Fatalf("unknown question code %d: %s", code, out.String())
	}
}

func TestFeedbackReadsRecordsWrittenByOtherVersions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	getenv := func(k string) string {
		if k == "LAVAGNA_SESSION" {
			return "feedback-versions"
		}
		return ""
	}
	_, c, err := bound(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	// The record shape written before per-question feedback, byte for byte.
	old := `{"lavagna":"feedback","round":"r1","submission":"s-12345678","choices":{"storage":"db"},"comments":[{"anchor":null,"text":"è ok"}],"images":[]}`
	if err := lease.StoreArtifact("feedback", "s-12345678", []byte(old)); err != nil {
		t.Fatal(err)
	}
	lease.Release()

	var out bytes.Buffer
	if code := Feedback(getenv, &out, FeedbackRequest{Submission: "s-12345678", All: true}); code != exitOK || out.String() != old+"\n" {
		t.Fatalf("--all on an old record: %d %q", code, out.String())
	}
	for name, request := range map[string]FeedbackRequest{
		"question": {Submission: "s-12345678", Question: "storage"},
		"overview": {Submission: "s-12345678", Overview: true},
		"summary":  {Submission: "s-12345678"},
	} {
		out.Reset()
		if code := Feedback(getenv, &out, request); code != exitInvalid || !strings.Contains(out.String(), "--all") {
			t.Fatalf("%s on an old record: %d %s", name, code, out.String())
		}
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cache, "lavagna", c.Key, "state.json")
	newer := []byte(`{"format":99,"ledger":"not this binary's"}`)
	if err := os.WriteFile(statePath, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Feedback(getenv, &out, FeedbackRequest{Submission: "s-12345678", All: true}); code != exitError || !strings.Contains(out.String(), "lavagna close") {
		t.Fatalf("feedback on a newer format: %d %s", code, out.String())
	}
	if b, err := os.ReadFile(statePath); err != nil || !bytes.Equal(b, newer) {
		t.Fatalf("newer state was touched: %s %v", b, err)
	}
}
