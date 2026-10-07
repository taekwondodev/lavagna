package conversation

import (
	"reflect"
	"strings"
	"testing"
)

var (
	fileOrDB   = []Option{{"file", "Un file per sessione"}, {"db", "Database locale"}}
	dayOrWeek  = []Option{{"day", "1 giorno"}, {"week", "7 giorni"}}
	atomicNone = []Option{{"atomic", "Rename atomico"}, {"none", "Nessuna protezione"}}
)

func whole(id string, options []Option) Version {
	return Version{Line: 1, ID: id, Title: "Titolo " + id, Options: options}
}

func apply(t *testing.T, l Ledger, c Call) Ledger {
	t.Helper()
	n, errs := l.Apply(c)
	if errs != nil {
		t.Fatalf("apply: %v", errs)
	}
	return n
}

func refuse(t *testing.T, l Ledger, c Call, want string) {
	t.Helper()
	before := l.clone()
	n, errs := l.Apply(c)
	if !strings.Contains(strings.Join(errs, "\n"), want) {
		t.Fatalf("want error %q, got %v", want, errs)
	}
	if !reflect.DeepEqual(n, before) || !reflect.DeepEqual(l, before) {
		t.Fatalf("invalid call changed the ledger:\n%+v\n%+v", n, before)
	}
}

func answered(l Ledger, submission string, choices map[string]string) Ledger {
	b := Batch{Submission: submission, Questions: map[string]Feedback{}}
	for id, choice := range choices {
		b.Questions[id] = Feedback{Choice: choice}
	}
	return l.Accept(b)
}

// The five-call walk of "What does the agent send on each call after the first?".
func TestApplyFollowsTheFiveCallWalk(t *testing.T) {
	l := apply(t, Ledger{}, Call{Phase: "Archivio delle sessioni", PhaseLine: 1, Questions: []Version{
		whole("storage", fileOrDB), whole("retention", dayOrWeek), whole("crash", atomicNone),
		{Line: 9, ID: "cleanup", Title: "Chi esegue la pulizia?", After: []string{"retention"}, Planned: true},
	}})
	if l.Title != "Archivio delle sessioni" || l.Round != 1 || !reflect.DeepEqual(l.Order, []string{"storage", "retention", "crash", "cleanup"}) {
		t.Fatalf("first call: %+v", l)
	}
	if q := l.Questions["cleanup"]; q.Status != Planned || q.Version != 0 || q.Round != 0 {
		t.Fatalf("planned question: %+v", q)
	}
	l = l.Accept(Batch{Submission: "s-1", Questions: map[string]Feedback{"storage": {Choice: "file"}, "retention": {Messages: []string{"1 giorno non è poco?"}}}})

	l = apply(t, l, Call{Replies: []Reply{{ID: "retention", Text: "Il giorno si conta dall'ultimo uso."}}})
	if l.Round != 1 || len(l.Questions["retention"].Thread) != 2 || l.Questions["retention"].Thread[1].Author != "agent" {
		t.Fatalf("reply-only call: %+v", l.Questions["retention"])
	}
	l = answered(l, "s-2", map[string]string{"storage": "file", "retention": "day"})

	l = apply(t, l, Call{Questions: []Version{whole("retention", []Option{{"day", "1 giorno"}, {"week", "7 giorni (consigliato)"}})}, Replies: []Reply{{ID: "retention", Text: "Ora consiglio 7 giorni."}}})
	if q := l.Questions["retention"]; l.Round != 1 || q.Version != 2 || q.Answer == nil || q.Answer.Choice != "day" || len(q.Thread) != 3 {
		t.Fatalf("replacement stays in r1 and keeps the surviving answer: round %d %+v", l.Round, q)
	}
	l = answered(l, "s-3", map[string]string{"storage": "file", "retention": "week", "crash": "atomic"})

	l = apply(t, l, Call{
		Settled:   []Settle{{ID: "storage", Why: "Nessuna dipendenza nuova."}, {ID: "retention", Why: "Copre il weekend."}},
		Questions: []Version{{ID: "cleanup", Title: "Chi esegue la pulizia?", After: []string{"retention"}, Options: []Option{{"sweep", "Lo sweep"}, {"manual", "Un comando"}}}},
	})
	if l.Round != 2 {
		t.Fatalf("a new question opens r2, got r%d", l.Round)
	}
	if q := l.Questions["crash"]; q.Status != Open || q.Round != 2 || !reflect.DeepEqual(q.Marks, []Marked{{1, Moved}}) || q.Answer == nil || q.Answer.Choice != "atomic" {
		t.Fatalf("unsettled crash moves into r2 with its answer: %+v", q)
	}
	for _, id := range []string{"storage", "retention"} {
		if q := l.Questions[id]; q.Status != Settled || q.Round != 1 || q.Marks != nil {
			t.Fatalf("settled %s stays in r1: %+v", id, q)
		}
	}
	if q := l.Questions["cleanup"]; q.Status != Open || q.Round != 2 || q.Version != 1 {
		t.Fatalf("planned question becomes open in r2: %+v", q)
	}
	want := []Decision{
		{Question: "storage", Title: "Titolo storage", Decision: "Un file per sessione", Round: 1, Why: "Nessuna dipendenza nuova.", Rejected: []string{"Database locale"}},
		{Question: "retention", Title: "Titolo retention", Decision: "7 giorni (consigliato)", Round: 1, Why: "Copre il weekend.", Rejected: []string{"1 giorno"}},
	}
	if !reflect.DeepEqual(l.Decisions, want) {
		t.Fatalf("decisions:\n%+v\nwant\n%+v", l.Decisions, want)
	}
	l = answered(l, "s-4", map[string]string{"crash": "atomic", "cleanup": "sweep"})

	l = apply(t, l, Call{
		Settled:   []Settle{{ID: "crash", Why: "Poche righe."}, {ID: "cleanup", Why: "Nessun comando."}},
		Questions: []Version{whole("confirm", []Option{{"yes", "Sì"}, {"fix", "Correggo"}})},
	})
	if l.Round != 3 || len(l.Decisions) != 4 || l.Questions["confirm"].Round != 3 {
		t.Fatalf("confirmation call: round %d, %d decisions", l.Round, len(l.Decisions))
	}
	if q := l.Questions["crash"]; q.Status != Settled || q.Round != 2 {
		t.Fatalf("crash settles in r2: %+v", q)
	}
}

func TestApplyRows(t *testing.T) {
	r1 := apply(t, Ledger{}, Call{Questions: []Version{whole("storage", fileOrDB), whole("crash", atomicNone)}})
	r1 = r1.Accept(Batch{Submission: "s-1", Questions: map[string]Feedback{"storage": {Choice: "db"}, "crash": {Answer: "Un journal"}}})

	t.Run("phase is valid only while the ledger is empty", func(t *testing.T) {
		refuse(t, r1, Call{Phase: "Altro", PhaseLine: 3}, "round.md:3: ::: phase is valid only in the first call")
	})
	t.Run("a bare title for a new id is planned and opens no round", func(t *testing.T) {
		l := apply(t, r1, Call{Questions: []Version{{ID: "later", Title: "Dopo", Planned: true}}})
		if l.Round != 1 || l.Questions["later"].Status != Planned || l.Questions["storage"].Round != 1 {
			t.Fatalf("%+v", l)
		}
	})
	t.Run("a bare title cannot replace a question with a body", func(t *testing.T) {
		refuse(t, r1, Call{Questions: []Version{{Line: 4, ID: "storage", Title: "Dove?", Planned: true}}}, "round.md:4: question storage already has a body")
	})
	t.Run("replacement drops an answer whose option is gone and keeps free text", func(t *testing.T) {
		l := apply(t, r1, Call{Questions: []Version{whole("storage", []Option{{"file", "File"}, {"sqlite", "SQLite"}}), whole("crash", []Option{{"rename", "Rename"}, {"none", "Niente"}})}})
		if l.Questions["storage"].Answer != nil || l.Questions["crash"].Answer == nil || l.Questions["crash"].Answer.Text != "Un journal" {
			t.Fatalf("answers after replacement: %+v %+v", l.Questions["storage"].Answer, l.Questions["crash"].Answer)
		}
		if l.Questions["storage"].Version != 2 || l.Round != 1 {
			t.Fatalf("replacement is a new version in the same round: %+v", l)
		}
	})
	t.Run("replacing a settled question reopens it in the current round", func(t *testing.T) {
		l := apply(t, r1, Call{Settled: []Settle{{ID: "storage", Why: "Serve SQL."}}})
		l = apply(t, l, Call{Questions: []Version{whole("next", fileOrDB)}})
		l = apply(t, l, Call{Questions: []Version{whole("storage", fileOrDB)}})
		q := l.Questions["storage"]
		if q.Status != Open || q.Round != 2 || !reflect.DeepEqual(q.Marks, []Marked{{1, Reopened}}) || q.Answer == nil || q.Answer.Choice != "db" {
			t.Fatalf("reopened question: %+v", q)
		}
		if len(l.Decisions) != 1 || !l.Decisions[0].Struck || l.Decisions[0].Decision != "Database locale" {
			t.Fatalf("decision row is struck through and kept: %+v", l.Decisions)
		}
	})
	t.Run("reply to a question, a closed-round question and the Overview", func(t *testing.T) {
		l := apply(t, r1, Call{Questions: []Version{whole("next", fileOrDB)}, Replies: []Reply{{ID: "storage", Text: "a"}, {Text: "b"}}})
		if got := l.Questions["storage"].Thread; len(got) != 1 || !reflect.DeepEqual(got[0], Message{Author: "agent", Round: 1, Text: "a"}) {
			t.Fatalf("reply is recorded in the round it was written: %+v", got)
		}
		if len(l.Overview) != 1 || l.Overview[0].Text != "b" {
			t.Fatalf("overview reply: %+v", l.Overview)
		}
	})
	t.Run("reply applies before new questions of the same call", func(t *testing.T) {
		refuse(t, r1, Call{Questions: []Version{whole("next", fileOrDB)}, Replies: []Reply{{Line: 7, ID: "next", Text: "x"}}}, `round.md:7: ::: reply names unknown question "next"`)
	})
	t.Run("settled with the recorded free-text answer rejects every option", func(t *testing.T) {
		l := apply(t, r1, Call{Settled: []Settle{{ID: "crash"}}})
		if d := l.Decisions[0]; d.Decision != "Un journal" || !reflect.DeepEqual(d.Rejected, []string{"Rename atomico", "Nessuna protezione"}) {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("settled with another option overrides the recorded answer", func(t *testing.T) {
		l := apply(t, r1, Call{Settled: []Settle{{ID: "storage", Option: "file"}}})
		if q := l.Questions["storage"]; q.Answer.Choice != "file" || l.Decisions[0].Decision != "Un file per sessione" {
			t.Fatalf("%+v %+v", q, l.Decisions)
		}
	})
	t.Run("settled without a recorded answer is invalid", func(t *testing.T) {
		l := apply(t, Ledger{}, Call{Questions: []Version{whole("storage", fileOrDB)}})
		refuse(t, l, Call{Settled: []Settle{{Line: 2, ID: "storage"}}}, "round.md:2: ::: settled storage: no recorded answer")
	})
	t.Run("settled with an option absent from the current version is invalid", func(t *testing.T) {
		refuse(t, r1, Call{Settled: []Settle{{Line: 2, ID: "storage", Option: "sqlite"}}}, `option "sqlite" is not in the current version`)
	})
	t.Run("settling again strikes the earlier row", func(t *testing.T) {
		l := apply(t, r1, Call{Settled: []Settle{{ID: "storage"}}})
		l = apply(t, l, Call{Settled: []Settle{{ID: "storage", Option: "file"}}})
		if len(l.Decisions) != 2 || !l.Decisions[0].Struck || l.Decisions[1].Struck {
			t.Fatalf("%+v", l.Decisions)
		}
	})
	t.Run("an unknown id after expiry asks for complete questions", func(t *testing.T) {
		for _, c := range []Call{{Replies: []Reply{{Line: 1, ID: "storage", Text: "x"}}}, {Settled: []Settle{{Line: 1, ID: "storage"}}}} {
			refuse(t, Ledger{}, c, "send complete questions")
		}
	})
	t.Run("one invalid element leaves every other element unapplied", func(t *testing.T) {
		refuse(t, r1, Call{Settled: []Settle{{ID: "storage"}}, Replies: []Reply{{Line: 5, ID: "ghost", Text: "x"}}}, "round.md:5:")
	})
	t.Run("no element changes nothing but the first call opens r1", func(t *testing.T) {
		if l := apply(t, r1, Call{}); !reflect.DeepEqual(l, r1) {
			t.Fatalf("%+v", l)
		}
		if l := apply(t, Ledger{}, Call{}); l.Round != 1 {
			t.Fatalf("first call round %d", l.Round)
		}
	})
}

func TestAcceptRecordsAnswersAndMessagesOnce(t *testing.T) {
	l := apply(t, Ledger{}, Call{Questions: []Version{whole("storage", fileOrDB), whole("crash", atomicNone)}})
	l = answered(l, "s-1", map[string]string{"storage": "db", "crash": "none"})
	l = apply(t, l, Call{Settled: []Settle{{ID: "storage"}}, Questions: []Version{whole("next", fileOrDB)}})
	batch := Batch{
		Submission: "s-2",
		Questions:  map[string]Feedback{"next": {Answer: "Dipende", Messages: []string{"perché?"}, Images: []string{"/i/1.png"}}, "storage": {Messages: []string{"rileggendo"}}},
		Overview:   Feedback{Messages: []string{"chiaro"}},
	}
	once := l.Accept(batch)
	if q := once.Questions["crash"]; q.Answer != nil {
		t.Fatalf("an open question absent from the batch is unanswered: %+v", q.Answer)
	}
	if q := once.Questions["next"]; q.Answer == nil || q.Answer.Text != "Dipende" || len(q.Thread) != 2 || q.Thread[1].Images[0] != "/i/1.png" {
		t.Fatalf("next: %+v", q)
	}
	if q := once.Questions["storage"]; q.Answer.Choice != "db" || len(q.Thread) != 1 || !reflect.DeepEqual(q.Thread[0], Message{Author: "user", Round: 2, Submission: "s-2", Text: "rileggendo"}) {
		t.Fatalf("a closed-round question keeps its answer and records the message: %+v", q)
	}
	if twice := once.Accept(batch); !reflect.DeepEqual(twice, once) {
		t.Fatal("a resent batch duplicated messages")
	}
	if len(l.Overview) != 0 || len(l.Questions["next"].Thread) != 0 {
		t.Fatal("Accept mutated the receiver")
	}
}

func TestArtifactsNameCurrentVersions(t *testing.T) {
	l := apply(t, Ledger{}, Call{Questions: []Version{whole("storage", fileOrDB), {ID: "later", Title: "Dopo", Planned: true}}})
	l = apply(t, l, Call{Questions: []Version{whole("storage", fileOrDB)}})
	if got := l.Artifacts(); !reflect.DeepEqual(got, map[string]bool{"storage-2": true}) {
		t.Fatalf("%v", got)
	}
}
