package live

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

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
