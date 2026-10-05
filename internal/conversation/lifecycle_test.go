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

func TestStepRoundStarted(t *testing.T) {
	returned := &Outcome{Round: "r1", Submission: "s-1", End: endReturned}
	check(t, []transition{
		{"first round of the conversation", State{}, RoundStarted{Origin: recorded},
			State{Origin: &recorded, Rounds: 1, Live: "r1"}},
		{"after a returned round", State{Origin: &recorded, Rounds: 1, Previous: returned}, RoundStarted{Origin: recorded},
			State{Origin: &recorded, Rounds: 2, Live: "r2", Previous: returned}},
		{"after Esc during the user's turn", State{Origin: &recorded, Rounds: 2, Live: "r2", Previous: returned}, RoundStarted{Origin: recorded},
			State{Origin: &recorded, Rounds: 3, Live: "r3", Previous: &Outcome{Round: "r2", End: endInterrupted}}},
		{"after Esc between Accepted and Returned", State{Origin: &recorded, Rounds: 2, Live: "r2", Accepted: "s-2", Previous: returned}, RoundStarted{Origin: recorded},
			State{Origin: &recorded, Rounds: 3, Live: "r3", Previous: &Outcome{Round: "r2", Submission: "s-2", End: endUncertain}}},
		{"anchor names survive rounds for carried drafts", State{Anchors: []string{"Old"}}, RoundStarted{Origin: recorded, Anchors: []string{"New", "Old"}},
			State{Origin: &recorded, Rounds: 1, Live: "r1", Anchors: []string{"New", "Old"}}},
		{"after EADDRINUSE on the recorded port", State{Origin: &recorded, Rounds: 1, Previous: returned}, RoundStarted{Origin: fresh},
			State{Origin: &fresh, Rounds: 2, Live: "r2", Previous: returned}},
	})
}

func TestStepBatchAccepted(t *testing.T) {
	check(t, []transition{
		{"the live round accepts its batch", State{Origin: &recorded, Rounds: 1, Live: "r1"}, BatchAccepted{"s-1"},
			State{Origin: &recorded, Rounds: 1, Live: "r1", Accepted: "s-1"}},
		{"a round resolves once", State{Origin: &recorded, Rounds: 1, Live: "r1", Accepted: "s-1"}, BatchAccepted{"s-2"},
			State{Origin: &recorded, Rounds: 1, Live: "r1", Accepted: "s-1"}},
		{"no live round", State{Origin: &recorded, Rounds: 1}, BatchAccepted{"s-1"},
			State{Origin: &recorded, Rounds: 1}},
	})
}

func TestStepBatchReturned(t *testing.T) {
	check(t, []transition{
		{"the accepted batch is returned", State{Origin: &recorded, Rounds: 1, Live: "r1", Accepted: "s-1"}, BatchReturned{},
			State{Origin: &recorded, Rounds: 1, Previous: &Outcome{Round: "r1", Submission: "s-1", End: endReturned}}},
		{"nothing was accepted", State{Origin: &recorded, Rounds: 1, Live: "r1"}, BatchReturned{},
			State{Origin: &recorded, Rounds: 1, Live: "r1"}},
	})
}
