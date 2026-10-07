package live

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	c, err := conversation.FromEnv(getenv)
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

func TestFeedbackPagesExactUnicodeTextAndMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	getenv := func(k string) string {
		if k == "LAVAGNA_SESSION" {
			return "feedback-test"
		}
		return ""
	}
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := conversation.Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	text := string(bytes.Repeat([]byte("a"), 2047)) + "🧭" + "tail"
	line, err := json.Marshal(feedbackLine{Lavagna: "feedback", Round: "r1", Submission: "s-12345678", Comments: []comment{{Text: text}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.StoreArtifact("feedback", "s-12345678", line); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	var out bytes.Buffer
	Feedback(getenv, &out, FeedbackRequest{Submission: "s-12345678", Comment: 1})
	var first struct {
		Text   string `json:"text"`
		Next   int    `json:"next"`
		Length int    `json:"length"`
	}
	if err := json.Unmarshal(out.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.Text != string(bytes.Repeat([]byte("a"), 2047)) || first.Next != 2047 || first.Length != len(text) {
		t.Fatalf("first page %#v", first)
	}
	out.Reset()
	Feedback(getenv, &out, FeedbackRequest{Submission: "s-12345678", Comment: 1, Offset: first.Next})
	var second struct {
		Text string `json:"text"`
		Next int    `json:"next"`
	}
	if err := json.Unmarshal(out.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.Text != "🧭tail" || second.Next != 0 {
		t.Fatalf("second page %#v", second)
	}

	choices := map[string]string{}
	for i := range 150 {
		choices[fmt.Sprintf("q%039d", i)] = strings.Repeat("a", 40)
	}
	full := feedbackLine{Lavagna: "feedback", Round: "r2", Submission: "s-22334455", Choices: choices}
	for range 30 {
		anchor := strings.Repeat("界", 80)
		full.Comments = append(full.Comments, comment{Anchor: &anchor, Text: "ok"})
	}
	lease, err = conversation.Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(full)
	if err := lease.StoreArtifact("feedback", full.Submission, encoded); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	items := 0
	for offset := 0; ; {
		out.Reset()
		if code := Feedback(getenv, &out, FeedbackRequest{Submission: full.Submission, Offset: offset}); code != 0 || out.Len() > 4096 {
			t.Fatalf("overview exit %d, bytes %d", code, out.Len())
		}
		var page struct {
			Items       []feedbackItem
			Next, Total int
		}
		if err := json.Unmarshal(out.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != 180 {
			t.Fatalf("overview total %d", page.Total)
		}
		for _, item := range page.Items {
			if items < 150 && item.Choice != fmt.Sprintf("q%039d", items) || items >= 150 && item.Comment != items-149 {
				t.Fatalf("item %d: %#v", items, item)
			}
			items++
		}
		if page.Next == 0 {
			break
		}
		if page.Next <= offset {
			t.Fatal("overview did not advance")
		}
		offset = page.Next
	}
	if items != 180 {
		t.Fatalf("overview lost items: %d", items)
	}
}
