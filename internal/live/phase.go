package live

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/round"
)

// maxCallStored bounds the question artifacts one call writes.
const maxCallStored = 6 << 20

type questionArtifact struct {
	Question  round.PhaseQuestion `json:"question"`
	Source    string              `json:"source,omitempty"`
	HTML      string              `json:"html,omitempty"`
	Resources []round.File        `json:"resources,omitempty"`
}

// PhaseRound applies one call to the question ledger and waits for the next
// feedback batch. The call is validated whole before anything is written: it
// writes the new question versions, commits state.json, then deletes the
// versions the ledger no longer references. within declares how long the
// harness lets the call run; zero leaves the harness default.
func PhaseRound(getenv func(string) string, src io.Reader, dir string, within time.Duration, out, errw io.Writer) int {
	started := time.Now()
	caller, c, err := bound(getenv)
	if err != nil {
		return invalid(out, err.Error())
	}
	conversation.Sweep(c, time.Now())
	in, files, errs := input(src, dir)
	if errs != nil {
		return invalid(out, errs...)
	}
	lease, err := conversation.Acquire(c)
	if errors.Is(err, conversation.ErrBusy) {
		return busy(out, c)
	}
	if err != nil {
		return failure(out, err)
	}
	defer lease.Release()
	st, stale, err := lease.Current()
	if err != nil {
		return failure(out, err)
	}
	prior := map[string][]string{}
	for id, q := range st.Ledger.Questions {
		prior[id] = q.After
	}
	phase, errs := round.ParsePhaseKnown(in.Source, prior)
	if errs != nil {
		return invalid(out, errs...)
	}
	if errs = round.AttachPhaseFiles(&phase, files); errs != nil {
		return invalid(out, errs...)
	}
	ledger, errs := st.Ledger.Apply(call(phase))
	if errs != nil {
		return invalid(out, errs...)
	}
	kind := noticeFor(st.Ledger, phase)
	wd, err := os.Getwd()
	if err != nil {
		return failure(out, err)
	}
	if errs = round.RenderPhase(&phase, round.Repository(wd), recap(ledger)); errs != nil {
		return invalid(out, errs...)
	}
	written := map[string][]byte{}
	stored := 0
	for _, q := range phase.Questions {
		if q.Planned {
			continue
		}
		data, err := json.Marshal(questionArtifact{Question: q, Source: q.Source, HTML: q.HTML, Resources: q.Resources})
		if err != nil {
			return failure(out, err)
		}
		// Count the exact serialized bytes StoreArtifact will retain, including
		// JSON overhead and base64-encoded resources.
		stored += len(data)
		if stored > maxCallStored {
			return invalid(out, "question artifacts exceed the 6 MiB storage bound")
		}
		entry := ledger.Questions[q.ID]
		entry.Bytes = q.Size()
		ledger.Questions[q.ID] = entry
		written[conversation.ArtifactName(q.ID, entry.Version)] = data
	}
	if ledger.Stored() > conversation.PhaseBytes {
		return invalid(out, "the current versions of the phase's questions exceed the 16 MiB phase bound")
	}
	questions, err := present(lease, ledger, phase)
	if err != nil {
		return failure(out, err)
	}

	ln, origin, fresh, err := bind(c, st.Origin)
	if err != nil {
		return failure(out, err)
	}
	defer ln.Close()
	// Clear orphans of a call that died before its commit, so their names are
	// free. Stale data stays untouched until the fresh phase commits.
	committed := st.Ledger.Artifacts()
	if !stale {
		if err := lease.Prune("question", committed); err != nil {
			return failure(out, err)
		}
	}
	// rollback removes exactly the versions this call published.
	var published []string
	rollback := func() {
		for _, name := range published {
			lease.RemoveArtifact("question", name)
		}
	}
	for name, data := range written {
		if err := lease.StoreArtifact("question", name, data); err != nil {
			rollback()
			return failure(out, err)
		}
		published = append(published, name)
	}
	st = st.Step(conversation.CallStarted{Origin: origin, Ledger: ledger})
	if err := lease.Save(st); err != nil {
		rollback()
		return failure(out, err)
	}
	if stale {
		fmt.Fprintln(errw, "lavagna: conversation state from an older lavagna was discarded; the phase restarted empty")
		if err := lease.Purge(); err != nil {
			fmt.Fprintf(errw, "lavagna: cannot delete the old phase data (%v); close removes it\n", err)
		}
	}
	if err := lease.Prune("question", ledger.Artifacts()); err != nil {
		fmt.Fprintf(errw, "lavagna: cannot remove replaced question versions (%v); the next call retries\n", err)
	}

	spec := roundSpec{Origin: origin, ID: st.RoundID(), Call: st.Live, Token: conversation.Secret(16), FrameKey: conversation.Secret(16), Previous: st.Previous, Images: c.Images(), Ledger: st.Ledger, Questions: questions}
	srv := newRound(spec)
	h := listen(srv, ln)
	defer h.stop()
	fmt.Fprintf(errw, "lavagna · round %s · %s · Esc per interrompere\n", st.RoundID(), origin.URL())
	go reveal(getenv, errw, origin.URL(), fresh, srv.seen)
	go announce(getenv, errw, kind, origin.URL())
	got, ok := await(srv, caller.Deadline(getenv, within), started)
	if !ok {
		st = st.Step(conversation.CallPaused{})
		record(errw, lease, st)
		if result(out, exitOK, struct {
			Lavagna string `json:"lavagna"`
			Round   string `json:"round"`
		}{"waiting", st.RoundID()}) != exitOK {
			return exitError
		}
		handOver(errw, c.Relay(), ln, relayed{Round: spec, Paused: true})
		return exitOK
	}
	st = st.Step(conversation.BatchAccepted{Batch: got.ledgerBatch()})
	record(errw, lease, st)
	line := got.phaseLine()
	var full bytes.Buffer
	if err := encodeJSON(&full, line); err != nil {
		return failure(out, err)
	}
	encoded := bytes.TrimSuffix(full.Bytes(), []byte("\n"))
	if len(encoded) > maxResultBytes {
		return failure(out, fmt.Errorf("feedback exceeds %d bytes", maxResultBytes))
	}
	if err := retain(lease, got.submission, encoded); err != nil {
		return failure(out, err)
	}
	var response any = line
	if len(encoded) > 2048 {
		response = got.deferredPhaseLine()
	}
	if result(out, exitOK, response) != exitOK {
		return exitError
	}
	st = st.Step(conversation.BatchReturned{})
	record(errw, lease, st)
	// The relay serves the ledger with this batch, so a page reloaded during
	// the agent's turn shows the sent messages in their threads.
	spec.Ledger = st.Ledger
	srv.returned(handOver(errw, c.Relay(), ln, relayed{Round: spec, Submission: got.submission, Session: observe(getenv, caller, c)}))
	return exitOK
}

// await waits for the call's batch. With a deadline it pauses the call
// shortly before the harness would kill it, and ok is false; a batch admitted
// in the meantime is returned instead.
func await(srv *server, deadline time.Duration, started time.Time) (got batch, ok bool) {
	if deadline <= 0 {
		return <-srv.accepted, true
	}
	timer := time.NewTimer(time.Until(started.Add(early(deadline))))
	defer timer.Stop()
	select {
	case got = <-srv.accepted:
		return got, true
	case <-timer.C:
		if srv.pause() {
			return batch{}, false
		}
		return <-srv.accepted, true
	}
}

// early leaves the call a margin to return its outcome and hand the page over
// before the harness's deadline.
func early(deadline time.Duration) time.Duration {
	return deadline - min(15*time.Second, deadline/10)
}

// retain stores the feedback record. A batch resent under the same submission,
// as after a call that ended Uncertain, replaces the earlier record so later
// reads match the outcome just returned.
func retain(lease *conversation.Lease, submission string, encoded []byte) error {
	err := lease.StoreArtifact("feedback", submission, encoded)
	if errors.Is(err, fs.ErrExist) {
		err = lease.ReplaceArtifact("feedback", submission, encoded)
	}
	return err
}

func call(phase round.Phase) conversation.Call {
	c := conversation.Call{Phase: phase.Title, PhaseLine: phase.TitleLine}
	for _, e := range phase.Settled {
		c.Settled = append(c.Settled, conversation.Settle{Line: e.Line, ID: e.ID, Option: e.Option, Why: e.Text})
	}
	for _, q := range phase.Questions {
		v := conversation.Version{Line: q.StartLine, ID: q.ID, Title: q.Title, After: q.After, Planned: q.Planned}
		for _, o := range q.Options {
			v.Options = append(v.Options, conversation.Option{ID: o.ID, Label: o.Label})
		}
		c.Questions = append(c.Questions, v)
	}
	for _, e := range phase.Replies {
		c.Replies = append(c.Replies, conversation.Reply{Line: e.Line, ID: e.ID, Text: e.Text})
	}
	return c
}

func recap(l conversation.Ledger) []round.RecapRow {
	rows := []round.RecapRow{}
	for _, d := range l.Decisions {
		rows = append(rows, round.RecapRow{Question: d.Title, Decision: d.Decision, Round: fmt.Sprintf("r%d", d.Round), Why: d.Why, Rejected: d.Rejected, Struck: d.Struck})
	}
	return rows
}

// present lists every question of the phase in ledger order: this call's
// versions as rendered, earlier ones from their retained artifacts.
func present(lease *conversation.Lease, l conversation.Ledger, phase round.Phase) ([]round.PhaseQuestion, error) {
	sent := map[string]round.PhaseQuestion{}
	for _, q := range phase.Questions {
		sent[q.ID] = q
	}
	var out []round.PhaseQuestion
	for _, id := range l.Order {
		entry := l.Questions[id]
		if q, ok := sent[id]; ok && !q.Planned {
			out = append(out, q)
			continue
		}
		if entry.Status == conversation.Planned {
			out = append(out, round.PhaseQuestion{ID: id, Title: entry.Title, After: entry.After, Planned: true})
			continue
		}
		b, err := lease.ReadArtifact("question", conversation.ArtifactName(id, entry.Version), maxCallStored)
		if err != nil {
			return nil, fmt.Errorf("question %s: %w", id, err)
		}
		var a questionArtifact
		if err := json.Unmarshal(b, &a); err != nil {
			return nil, fmt.Errorf("question %s: stored version is invalid", id)
		}
		q := a.Question
		q.Title, q.After, q.HTML, q.Resources = entry.Title, entry.After, a.HTML, a.Resources
		out = append(out, q)
	}
	return out, nil
}
