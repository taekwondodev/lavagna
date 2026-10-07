package conversation

import (
	"fmt"
	"slices"
)

// PhaseBytes bounds the stored current versions of every question in a phase.
const PhaseBytes = 16 << 20

type Status string

const (
	Planned Status = "planned"
	Open    Status = "open"
	Settled Status = "settled"
)

type Mark string

const (
	Moved    Mark = "moved"
	Reopened Mark = "reopened"
)

// Ledger is the phase's question ledger. Apply and Accept are its only
// transitions; both return a new ledger and never share slices with the old one.
type Ledger struct {
	Title     string              `json:"title,omitempty"`
	Round     int                 `json:"round"`
	Order     []string            `json:"order,omitempty"`
	Questions map[string]Question `json:"questions,omitempty"`
	Overview  []Message           `json:"overview,omitempty"`
	Decisions []Decision          `json:"decisions,omitempty"`
}

type Question struct {
	Title   string    `json:"title"`
	After   []string  `json:"after,omitempty"`
	Status  Status    `json:"status"`
	Round   int       `json:"round,omitempty"`
	Marks   []Marked  `json:"marks,omitempty"`
	Version int       `json:"version,omitempty"`
	Bytes   int       `json:"bytes,omitempty"`
	Options []Option  `json:"options,omitempty"`
	Answer  *Answer   `json:"answer,omitempty"`
	Thread  []Message `json:"thread,omitempty"`
}

// Marked records what happened to a question in a round it has left.
type Marked struct {
	Round int  `json:"round"`
	Mark  Mark `json:"mark"`
}

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Answer is the user's last sent choice or free-text answer.
type Answer struct {
	Choice string `json:"choice,omitempty"`
	Text   string `json:"text,omitempty"`
}

type Message struct {
	Author     string   `json:"author"`
	Round      int      `json:"round"`
	Submission string   `json:"submission,omitempty"`
	Text       string   `json:"text,omitempty"`
	Images     []string `json:"images,omitempty"`
}

// Decision is one row of the decisions table, copied when the question settles
// so it stays readable after a replacement.
type Decision struct {
	Question string   `json:"question"`
	Title    string   `json:"title"`
	Decision string   `json:"decision"`
	Round    int      `json:"round"`
	Why      string   `json:"why,omitempty"`
	Rejected []string `json:"rejected,omitempty"`
	Struck   bool     `json:"struck,omitempty"`
}

// Call holds one call's elements. Line numbers locate errors in round.md.
type Call struct {
	Phase     string
	PhaseLine int
	Settled   []Settle
	Questions []Version
	Replies   []Reply
}

type Settle struct {
	Line       int
	ID, Option string
	Why        string
}

// Version is one question as sent: a whole question, or a bare title when Planned.
type Version struct {
	Line    int
	ID      string
	Title   string
	After   []string
	Planned bool
	Options []Option
}

// Reply is an agent message; an empty ID addresses the Overview.
type Reply struct {
	Line int
	ID   string
	Text string
}

// Batch is one accepted feedback batch.
type Batch struct {
	Submission string
	Questions  map[string]Feedback
	Overview   Feedback
}

type Feedback struct {
	Choice   string
	Answer   string
	Messages []string
	Images   []string
}

const (
	agent = "agent"
	user  = "user"
)

// Apply validates c against the ledger and returns the ledger after it, in the
// order phase, settled, replacements, reply, new questions with the round
// advance. On any error the receiver is returned unchanged with every error.
// It stays one function by design: see docs/adr/0002-ledger-apply-stays-one-transition.md.
func (l Ledger) Apply(c Call) (Ledger, []string) {
	n := l.clone()
	if n.Questions == nil {
		n.Questions = map[string]Question{}
	}
	var errs []string
	fail := func(line int, format string, args ...any) {
		errs = append(errs, fmt.Sprintf("round.md:%d: ", line)+fmt.Sprintf(format, args...))
	}
	unknown := func(line int, element, id string) {
		fail(line, "%s names unknown question %q; after close, expiry or an upgrade the phase starts empty: send complete questions", element, id)
	}

	if c.PhaseLine != 0 {
		if len(n.Questions) > 0 {
			fail(c.PhaseLine, "::: phase is valid only in the first call of a phase")
		}
		n.Title = c.Phase
	}

	for _, s := range c.Settled {
		q, ok := n.Questions[s.ID]
		if !ok {
			unknown(s.Line, "::: settled", s.ID)
			continue
		}
		if q.Status == Planned {
			fail(s.Line, "::: settled %s: a planned question has no options yet", s.ID)
			continue
		}
		decision, choice := "", s.Option
		switch {
		case s.Option != "":
			i := slices.IndexFunc(q.Options, func(o Option) bool { return o.ID == s.Option })
			if i < 0 {
				fail(s.Line, "::: settled %s: option %q is not in the current version", s.ID, s.Option)
				continue
			}
			decision = q.Options[i].Label
		case q.Answer != nil && q.Answer.Choice != "":
			choice = q.Answer.Choice
			decision = q.label(choice)
		case q.Answer != nil && q.Answer.Text != "":
			decision = q.Answer.Text
		default:
			fail(s.Line, "::: settled %s: no recorded answer; name the option", s.ID)
			continue
		}
		var rejected []string
		for _, o := range q.Options {
			if o.ID != choice {
				rejected = append(rejected, o.Label)
			}
		}
		n.strike(s.ID)
		n.Decisions = append(n.Decisions, Decision{Question: s.ID, Title: q.Title, Decision: decision, Round: q.Round, Why: s.Why, Rejected: rejected})
		if s.Option != "" {
			q.Answer = &Answer{Choice: s.Option}
		}
		q.Status = Settled
		n.Questions[s.ID] = q
	}

	var fresh []Version
	for _, v := range c.Questions {
		q, ok := n.Questions[v.ID]
		switch {
		case !ok || q.Status == Planned && !v.Planned:
			fresh = append(fresh, v)
		case v.Planned && q.Status != Planned:
			fail(v.Line, "question %s already has a body; send it whole to replace it", v.ID)
		case v.Planned:
			q.Title, q.After = v.Title, v.After
			n.Questions[v.ID] = q
		default:
			q.Title, q.After, q.Options, q.Version = v.Title, v.After, v.Options, q.Version+1
			if q.Answer != nil && q.Answer.Choice != "" && q.label(q.Answer.Choice) == "" {
				q.Answer = nil
			}
			if q.Status == Settled {
				q.Marks = append(q.Marks, Marked{Round: q.Round, Mark: Reopened})
				q.Status, q.Round = Open, n.Round
				n.strike(v.ID)
			}
			n.Questions[v.ID] = q
		}
	}

	for _, r := range c.Replies {
		message := Message{Author: agent, Round: n.Round, Text: r.Text}
		if r.ID == "" {
			n.Overview = append(n.Overview, message)
			continue
		}
		q, ok := n.Questions[r.ID]
		if !ok {
			unknown(r.Line, "::: reply", r.ID)
			continue
		}
		q.Thread = append(q.Thread, message)
		n.Questions[r.ID] = q
	}

	advance := n.Round == 0 || slices.ContainsFunc(fresh, func(v Version) bool { return !v.Planned })
	if advance {
		old := n.Round
		n.Round++
		for id, q := range n.Questions {
			if q.Status == Open && q.Round == old && old > 0 {
				q.Marks = append(q.Marks, Marked{Round: old, Mark: Moved})
				q.Round = n.Round
				n.Questions[id] = q
			}
		}
	}
	for _, v := range fresh {
		q, ok := n.Questions[v.ID]
		if !ok {
			n.Order = append(n.Order, v.ID)
		}
		q.Title, q.After = v.Title, v.After
		if v.Planned {
			q.Status = Planned
		} else {
			q.Status, q.Round, q.Options, q.Version = Open, n.Round, v.Options, q.Version+1
		}
		n.Questions[v.ID] = q
	}

	if errs != nil {
		return l, errs
	}
	return n, nil
}

// Accept records one accepted batch: it replaces the recorded answers of the
// current round's open questions and appends the user's messages. A batch
// already recorded under the same submission changes nothing.
func (l Ledger) Accept(b Batch) Ledger {
	if l.received(b.Submission) {
		return l
	}
	n := l.clone()
	for id, q := range n.Questions {
		if q.Status != Open || q.Round != n.Round {
			continue
		}
		f := b.Questions[id]
		switch {
		case f.Choice != "":
			q.Answer = &Answer{Choice: f.Choice}
		case f.Answer != "":
			q.Answer = &Answer{Text: f.Answer}
		default:
			q.Answer = nil
		}
		n.Questions[id] = q
	}
	for id, f := range b.Questions {
		q, ok := n.Questions[id]
		if !ok {
			continue
		}
		q.Thread = append(q.Thread, n.posted(b.Submission, f)...)
		n.Questions[id] = q
	}
	n.Overview = append(n.Overview, n.posted(b.Submission, b.Overview)...)
	return n
}

func (l Ledger) posted(submission string, f Feedback) []Message {
	var out []Message
	for _, text := range f.Messages {
		out = append(out, Message{Author: user, Round: l.Round, Submission: submission, Text: text})
	}
	if len(f.Images) > 0 {
		out = append(out, Message{Author: user, Round: l.Round, Submission: submission, Images: slices.Clone(f.Images)})
	}
	return out
}

func (l Ledger) received(submission string) bool {
	from := func(m Message) bool { return m.Author == user && m.Submission == submission }
	if slices.ContainsFunc(l.Overview, from) {
		return true
	}
	for _, q := range l.Questions {
		if slices.ContainsFunc(q.Thread, from) {
			return true
		}
	}
	return false
}

// Stored is the total size of every question's current version.
func (l Ledger) Stored() int {
	total := 0
	for _, q := range l.Questions {
		total += q.Bytes
	}
	return total
}

// Artifacts names the question artifacts the ledger references.
func (l Ledger) Artifacts() map[string]bool {
	names := map[string]bool{}
	for id, q := range l.Questions {
		if q.Status != Planned {
			names[ArtifactName(id, q.Version)] = true
		}
	}
	return names
}

func ArtifactName(id string, version int) string { return fmt.Sprintf("%s-%d", id, version) }

func (q Question) label(option string) string {
	for _, o := range q.Options {
		if o.ID == option {
			return o.Label
		}
	}
	return ""
}

func (l *Ledger) strike(id string) {
	for i := range l.Decisions {
		if l.Decisions[i].Question == id {
			l.Decisions[i].Struck = true
		}
	}
}

func (l Ledger) clone() Ledger {
	n := l
	n.Order = slices.Clone(l.Order)
	n.Overview = cloneThread(l.Overview)
	n.Decisions = slices.Clone(l.Decisions)
	for i := range n.Decisions {
		n.Decisions[i].Rejected = slices.Clone(n.Decisions[i].Rejected)
	}
	n.Questions = make(map[string]Question, len(l.Questions))
	if l.Questions == nil {
		n.Questions = nil
	}
	for id, q := range l.Questions {
		q.After = slices.Clone(q.After)
		q.Marks = slices.Clone(q.Marks)
		q.Options = slices.Clone(q.Options)
		q.Thread = cloneThread(q.Thread)
		if q.Answer != nil {
			a := *q.Answer
			q.Answer = &a
		}
		n.Questions[id] = q
	}
	return n
}

func cloneThread(t []Message) []Message {
	out := slices.Clone(t)
	for i := range out {
		out[i].Images = slices.Clone(out[i].Images)
	}
	return out
}
