package conversation

import "fmt"

type end string

const (
	endReturned    end = "returned"
	endUncertain   end = "uncertain"
	endInterrupted end = "interrupted"
)

// Outcome is how a call ended. Calls, not rounds, key it: a reply-only call
// stays in its round.
type Outcome struct {
	Call       string `json:"call"`
	Round      string `json:"round"`
	Submission string `json:"submission,omitempty"`
	End        end    `json:"end"`
}

type Event interface{ event() }

// CallStarted commits a validated call and its ledger.
type CallStarted struct {
	Origin Origin
	Ledger Ledger
}

type BatchAccepted struct{ Batch Batch }

type BatchReturned struct{}

func (CallStarted) event()   {}
func (BatchAccepted) event() {}
func (BatchReturned) event() {}

func (s State) Step(e Event) State {
	switch e := e.(type) {
	case CallStarted:
		if s.Live != "" {
			outcome := endInterrupted
			if s.Accepted != "" {
				outcome = endUncertain
			}
			s.Previous = &Outcome{Call: s.Live, Round: s.RoundID(), Submission: s.Accepted, End: outcome}
		}
		s.Format = Format
		s.Origin = &e.Origin
		s.Ledger = e.Ledger
		s.Calls++
		s.Live = fmt.Sprintf("c%d", s.Calls)
		s.Accepted = ""
	case BatchAccepted:
		if s.Live != "" && s.Accepted == "" {
			s.Accepted = e.Batch.Submission
			s.Ledger = s.Ledger.Accept(e.Batch)
		}
	case BatchReturned:
		if s.Live != "" && s.Accepted != "" {
			s.Previous = &Outcome{Call: s.Live, Round: s.RoundID(), Submission: s.Accepted, End: endReturned}
			s.Live, s.Accepted = "", ""
		}
	}
	return s
}

// RoundID names the phase's current round, as results and the page show it.
func (s State) RoundID() string { return fmt.Sprintf("r%d", s.Ledger.Round) }
