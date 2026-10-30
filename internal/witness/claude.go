package witness

import (
	"encoding/json"
	"strings"
)

const interruption = "[Request interrupted by user"

type claude struct{ turn }

func newClaude(submission string) *claude { return &claude{turn{submission: []byte(submission)}} }

type claudeEntry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	Message     *struct {
		StopReason *string         `json:"stop_reason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeBlock struct {
	Type    string          `json:"type"`
	Text    *string         `json:"text"`
	Content json.RawMessage `json:"content"`
}

func (w *claude) entry(line []byte) verdict {
	var e claudeEntry
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		return unrecognized
	}

	if e.IsSidechain || e.Type != "user" && e.Type != "assistant" {
		return watching
	}
	if e.Message == nil {
		return unrecognized
	}
	if e.Type == "assistant" {
		if e.Message.StopReason == nil {
			return watching
		}
		switch *e.Message.StopReason {
		case "tool_use":
			return watching
		case "end_turn":
			return w.end(false)
		}
		return unrecognized
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		if strings.HasPrefix(text, interruption) {
			return w.end(true)
		}
		return watching
	}
	var blocks []claudeBlock
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return unrecognized
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_result":
			if w.receive(b.Content) {
				return received
			}
		case "text":
			if b.Text == nil {
				return unrecognized
			}
			if strings.HasPrefix(*b.Text, interruption) {
				return w.end(true)
			}
		case "":
			return unrecognized
		}
	}
	return watching
}
