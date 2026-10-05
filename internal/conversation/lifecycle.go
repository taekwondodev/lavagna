package conversation

import (
	"fmt"
	"slices"
)

type end string

const (
	endReturned    end = "returned"
	endUncertain   end = "uncertain"
	endInterrupted end = "interrupted"
)

type Outcome struct {
	Round      string `json:"round"`
	Submission string `json:"submission,omitempty"`
	End        end    `json:"end"`
}

type Event interface{ event() }

type RoundStarted struct {
	Origin  Origin
	Anchors []string
}

type BatchAccepted struct{ Submission string }

type BatchReturned struct{}

func (RoundStarted) event()  {}
func (BatchAccepted) event() {}
func (BatchReturned) event() {}

func (s State) Step(e Event) State {
	switch e := e.(type) {
	case RoundStarted:
		if s.Live != "" {
			outcome := endInterrupted
			if s.Accepted != "" {
				outcome = endUncertain
			}
			s.Previous = &Outcome{Round: s.Live, Submission: s.Accepted, End: outcome}
		}
		// Drafts can survive several interrupted rounds. Keep the authored anchor
		// names valid for resend, without making them pickable in newer content.
		s.Anchors = append(slices.Clone(s.Anchors), e.Anchors...)
		slices.Sort(s.Anchors)
		s.Anchors = slices.Compact(s.Anchors)
		s.Origin = &e.Origin
		s.Rounds++
		s.Live = fmt.Sprintf("r%d", s.Rounds)
		s.Accepted = ""
	case BatchAccepted:
		if s.Live != "" && s.Accepted == "" {
			s.Accepted = e.Submission
		}
	case BatchReturned:
		if s.Live != "" && s.Accepted != "" {
			s.Previous = &Outcome{Round: s.Live, Submission: s.Accepted, End: endReturned}
			s.Live, s.Accepted = "", ""
		}
	}
	return s
}
