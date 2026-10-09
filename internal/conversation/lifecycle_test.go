package conversation

import (
	"reflect"
	"testing"
)

var (
	recorded = Origin{Port: 4100, Cap: "cap-a"}
	fresh    = Origin{Port: 4200, Cap: "cap-b"}
)

type transition struct {
	name  string
	from  State
	event Event
	want  State
}

func check(t *testing.T, cases []transition) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.from.Step(c.event); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v previous %+v, want %+v previous %+v", got, got.Previous, c.want, c.want.Previous)
			}
		})
	}
}

func TestStepCallStarted(t *testing.T) {
	returned := &Outcome{Call: "c1", Round: "r1", Submission: "s-1", End: endReturned}
	r1 := Ledger{Round: 1}
	check(t, []transition{
		{"first call of the conversation", State{}, CallStarted{Origin: recorded, Ledger: r1},
			State{Format: 1, Origin: &recorded, Calls: 1, Live: "c1", Ledger: r1}},
		{"after a returned call", State{Format: 1, Origin: &recorded, Calls: 1, Previous: returned, Ledger: r1}, CallStarted{Origin: recorded, Ledger: r1},
			State{Format: 1, Origin: &recorded, Calls: 2, Live: "c2", Previous: returned, Ledger: r1}},
		{"after Esc during the user's turn, in the same round", State{Format: 1, Origin: &recorded, Calls: 2, Live: "c2", Previous: returned, Ledger: r1}, CallStarted{Origin: recorded, Ledger: r1},
			State{Format: 1, Origin: &recorded, Calls: 3, Live: "c3", Previous: &Outcome{Call: "c2", Round: "r1", End: endInterrupted}, Ledger: r1}},
		{"after Esc between Accepted and Returned", State{Format: 1, Origin: &recorded, Calls: 2, Live: "c2", Accepted: "s-2", Previous: returned, Ledger: r1}, CallStarted{Origin: recorded, Ledger: Ledger{Round: 2}},
			State{Format: 1, Origin: &recorded, Calls: 3, Live: "c3", Previous: &Outcome{Call: "c2", Round: "r1", Submission: "s-2", End: endUncertain}, Ledger: Ledger{Round: 2}}},
		{"after EADDRINUSE on the recorded port", State{Format: 1, Origin: &recorded, Calls: 1, Previous: returned, Ledger: r1}, CallStarted{Origin: fresh, Ledger: r1},
			State{Format: 1, Origin: &fresh, Calls: 2, Live: "c2", Previous: returned, Ledger: r1}},
	})
}

func TestStepBatchAccepted(t *testing.T) {
	batch := Batch{Submission: "s-1", Overview: Feedback{Messages: []string{"ok"}}}
	recordedBatch := Ledger{Round: 1, Overview: []Message{{Author: "user", Round: 1, Submission: "s-1", Text: "ok"}}}
	check(t, []transition{
		{"the live call accepts its batch into the ledger", State{Origin: &recorded, Calls: 1, Live: "c1", Ledger: Ledger{Round: 1}}, BatchAccepted{batch},
			State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: recordedBatch}},
		{"a call resolves once", State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: Ledger{Round: 1}}, BatchAccepted{Batch{Submission: "s-2"}},
			State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: Ledger{Round: 1}}},
		{"no live call", State{Origin: &recorded, Calls: 1}, BatchAccepted{batch},
			State{Origin: &recorded, Calls: 1}},
	})
}

func TestStepBatchReturned(t *testing.T) {
	check(t, []transition{
		{"the accepted batch is returned", State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: Ledger{Round: 1}}, BatchReturned{},
			State{Origin: &recorded, Calls: 1, Previous: &Outcome{Call: "c1", Round: "r1", Submission: "s-1", End: endReturned}, Ledger: Ledger{Round: 1}}},
		{"nothing was accepted", State{Origin: &recorded, Calls: 1, Live: "c1"}, BatchReturned{},
			State{Origin: &recorded, Calls: 1, Live: "c1"}},
	})
}

func TestStepCallPaused(t *testing.T) {
	paused := &Outcome{Call: "c1", Round: "r1", End: endPaused}
	check(t, []transition{
		{"the deadline comes before any batch", State{Origin: &recorded, Calls: 1, Live: "c1", Ledger: Ledger{Round: 1}}, CallPaused{},
			State{Origin: &recorded, Calls: 1, Previous: paused, Ledger: Ledger{Round: 1}}},
		{"a batch was accepted first", State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: Ledger{Round: 1}}, CallPaused{},
			State{Origin: &recorded, Calls: 1, Live: "c1", Accepted: "s-1", Ledger: Ledger{Round: 1}}},
		{"the resuming call keeps the round", State{Format: 1, Origin: &recorded, Calls: 1, Previous: paused, Ledger: Ledger{Round: 1}}, CallStarted{Origin: recorded, Ledger: Ledger{Round: 1}},
			State{Format: 1, Origin: &recorded, Calls: 2, Live: "c2", Previous: paused, Ledger: Ledger{Round: 1}}},
	})
}
