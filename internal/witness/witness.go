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
	unreadAborted
	answered
	aborted
	unrecognized
)

const sessionVersion = 3

// reader turns one appended line of a harness record into a verdict about the
// returned submission.
type reader interface {
	entry(line []byte) verdict
}

// turn holds what every reader tracks: the submission to recognize and
// whether the agent already received it.
type turn struct {
	submission []byte
	received   bool
}

// receive marks the receipt the first time the submission appears in text the
// harness delivered to the agent. Matching the id rather than its JSON-quoted
// form survives harnesses that nest the output in another JSON string.
func (t *turn) receive(text []byte) bool {
	if t.received || !bytes.Contains(text, t.submission) {
		return false
	}
	t.received = true
	return true
}

// end is the verdict when the agent's turn ends, normally or interrupted.
func (t *turn) end(interrupted bool) verdict {
	switch {
	case !t.received && interrupted:
		return unreadAborted
	case !t.received:
		return unread
	case interrupted:
		return aborted
	}
	return answered
}

// pi reads a Pi session.
type pi struct{ turn }

func newPi(submission string) *pi { return &pi{turn{submission: []byte(submission)}} }

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

func (w *pi) entry(line []byte) verdict {
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
			if b.Text != nil && w.receive([]byte(*b.Text)) {
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
			return w.end(*m.StopReason == "aborted")
		default:
			return unrecognized
		}
	}
	return watching
}
