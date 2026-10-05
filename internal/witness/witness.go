package witness

import (
	"bytes"
	"encoding/json"
)

type verdict int

const (
	watching verdict = iota
	received
	unread
	answered
	unrecognized
)

const sessionVersion = 3

type witness struct {
	marker   []byte
	received bool
}

func newWitness(submission string) *witness {
	return &witness{marker: []byte(`"submission":"` + submission + `"`)}
}

func header(line []byte) bool {
	var h struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
	}
	return json.Unmarshal(line, &h) == nil && h.Type == "session" && h.Version == sessionVersion
}

type entry struct {
	Type    string `json:"type"`
	Message *struct {
		Role       string          `json:"role"`
		StopReason *string         `json:"stopReason"`
		Content    json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type string  `json:"type"`
	Text *string `json:"text"`
}

func (w *witness) entry(line []byte) verdict {
	var e entry
	if json.Unmarshal(line, &e) != nil || e.Type == "" {
		return unrecognized
	}
	if e.Type != "message" {
		return watching
	}
	m := e.Message
	if m == nil || m.Role == "" {
		return unrecognized
	}
	switch m.Role {
	case "toolResult":
		var content []block
		if json.Unmarshal(m.Content, &content) != nil {
			return unrecognized
		}
		for _, b := range content {
			if b.Type == "" || b.Type == "text" && b.Text == nil {
				return unrecognized
			}
			if !w.received && b.Text != nil && bytes.Contains([]byte(*b.Text), w.marker) {
				w.received = true
				return received
			}
		}
	case "assistant":
		if m.StopReason == nil {
			return unrecognized
		}
		switch *m.StopReason {
		case "toolUse", "deferred":
		case "stop", "aborted":
			if w.received {
				return answered
			}
			return unread
		default:
			return unrecognized
		}
	}
	return watching
}
