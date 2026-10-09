package witness

import (
	"encoding/json"
	"regexp"
	"slices"
)

// Hermes keeps its conversations in a database whose rows do not tell an
// interrupted turn apart, so its shell hooks report to lavagna instead. Each
// hook payload becomes at most one line of the conversation's events file,
// holding only submission ids and turn outcomes, never tool output.

var submissionID = regexp.MustCompile(`s-[0-9a-f]{8,64}`)

type hermesEvent struct {
	Event       string   `json:"event"`
	Submissions []string `json:"submissions,omitempty"`
	Outcome     string   `json:"outcome,omitempty"`
}

// HermesObservation reads one shell-hook payload. It returns the session the
// hook fired for and the events line to append, or ok false when the payload
// tells lavagna nothing.
func HermesObservation(payload []byte) (session string, line []byte, ok bool) {
	var p struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		Extra   struct {
			Result      json.RawMessage `json:"result"`
			Completed   *bool           `json:"completed"`
			Failed      *bool           `json:"failed"`
			Interrupted *bool           `json:"interrupted"`
		} `json:"extra"`
	}
	if json.Unmarshal(payload, &p) != nil || p.Session == "" {
		return "", nil, false
	}
	var e hermesEvent
	switch p.Event {
	case "post_tool_call":
		var result string
		if json.Unmarshal(p.Extra.Result, &result) != nil {
			result = string(p.Extra.Result)
		}
		ids := submissionID.FindAllString(result, -1)
		if len(ids) == 0 {
			return "", nil, false
		}
		slices.Sort(ids)
		e = hermesEvent{Event: "tool", Submissions: slices.Compact(ids)}
	case "on_session_end":
		// Exit paths fire a reduced legacy shape without the turn's outcome.
		x := p.Extra
		if x.Completed == nil || x.Failed == nil || x.Interrupted == nil {
			return "", nil, false
		}
		e = hermesEvent{Event: "end", Outcome: "completed"}
		if *x.Interrupted || *x.Failed {
			e.Outcome = "interrupted"
		}
	default:
		return "", nil, false
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", nil, false
	}
	return p.Session, b, true
}

// hermes reads the events file the Hermes hooks append.
type hermes struct{ turn }

func newHermes(submission string) *hermes { return &hermes{turn{submission: []byte(submission)}} }

func (w *hermes) entry(line []byte) verdict {
	var e hermesEvent
	if json.Unmarshal(line, &e) != nil {
		return unrecognized
	}
	switch e.Event {
	case "tool":
		for _, id := range e.Submissions {
			if w.receive([]byte(id)) {
				return received
			}
		}
		return watching
	case "end":
		switch e.Outcome {
		case "completed":
			return w.end(false)
		case "interrupted":
			return w.end(true)
		}
	}
	return unrecognized
}
