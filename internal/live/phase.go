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

type pendingQuestionArtifact struct {
	name string
	data []byte
}

type questionArtifact struct {
	Question  round.PhaseQuestion `json:"question"`
	Source    string              `json:"source,omitempty"`
	HTML      string              `json:"html,omitempty"`
	Resources []round.File        `json:"resources,omitempty"`
}

// PhaseRound presents one per-question batch, retaining each complete question
// version independently of the page's transport representation.
func PhaseRound(getenv func(string) string, src io.Reader, dir string, out, errw io.Writer) int {
	c, err := conversation.FromEnv(getenv)
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
	st, err := lease.Load()
	if err != nil {
		return failure(out, err)
	}
	if st.Questions == nil {
		st.Questions = map[string]conversation.QuestionEntry{}
	}
	prior := map[string][]string{}
	for id, entry := range st.Questions {
		prior[id] = entry.After
	}
	phase, errs := round.ParsePhaseKnown(in.Source, prior)
	if errs != nil {
		return invalid(out, errs...)
	}
	if errs = round.AttachPhaseFiles(&phase, files); errs != nil {
		return invalid(out, errs...)
	}
	wd, err := os.Getwd()
	if err != nil {
		return failure(out, err)
	}
	if errs = round.RenderPhase(&phase, round.Repository(wd)); errs != nil {
		return invalid(out, errs...)
	}
	ledger := map[string]conversation.QuestionEntry{}
	var pendingArtifacts []pendingQuestionArtifact
	var stored int64
	for _, q := range phase.Questions {
		entry := st.Questions[q.ID]
		entry.After = q.After
		if q.Planned {
			entry.Status = "planned"
			ledger[q.ID] = entry
			continue
		}
		entry.Version++
		entry.Status = "open"
		for {
			_, readErr := lease.ReadArtifact("question", fmt.Sprintf("%s-%d", q.ID, entry.Version), 6<<20)
			if errors.Is(readErr, fs.ErrNotExist) {
				break
			}
			if readErr != nil {
				return failure(out, readErr)
			}
			entry.Version++
		}
		data, err := json.Marshal(questionArtifact{Question: q, Source: q.Source, HTML: q.HTML, Resources: q.Resources})
		if err != nil {
			return failure(out, err)
		}
		// Count the exact serialized bytes StoreArtifact will retain, including
		// JSON overhead and base64-encoded resources.
		stored += int64(len(data))
		if stored > 6<<20 {
			return invalid(out, "question artifacts exceed the 6 MiB storage bound")
		}
		pendingArtifacts = append(pendingArtifacts, pendingQuestionArtifact{name: fmt.Sprintf("%s-%d", q.ID, entry.Version), data: data})
		ledger[q.ID] = entry
	}
	ln, origin, fresh, err := bind(c, st.Origin)
	if err != nil {
		return failure(out, err)
	}
	defer ln.Close()
	published := []string{}
	rollback := func() {
		for _, name := range published {
			if err := lease.RemoveArtifact("question", name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(errw, "lavagna: cannot remove unpublished question artifact %s: %v\n", name, err)
			}
		}
	}
	for _, artifact := range pendingArtifacts {
		if err := lease.StoreArtifact("question", artifact.name, artifact.data); err != nil {
			rollback()
			return failure(out, err)
		}
		published = append(published, artifact.name)
	}
	legacy := round.Round{Questions: []round.Question{}, Anchors: []string{}}
	for _, q := range phase.Questions {
		rq := round.Question{ID: q.ID, Options: []string{}}
		for _, option := range q.Options {
			rq.Options = append(rq.Options, option.ID)
		}
		if len(rq.Options) > 0 {
			legacy.Questions = append(legacy.Questions, rq)
		}
	}
	st = st.Step(conversation.RoundStarted{Origin: origin, Questions: ledger})
	if err := lease.Save(st); err != nil {
		rollback()
		return failure(out, err)
	}
	spec := roundSpec{Origin: origin, ID: st.Live, Token: conversation.Secret(16), FrameKey: conversation.Secret(16), Round: legacy, Previous: st.Previous, Images: c.Images(), Phase: &phase}
	srv := newRound(spec)
	h := listen(srv, ln)
	defer h.stop()
	fmt.Fprintf(errw, "lavagna · round %s · %s · Esc per interrompere\n", st.Live, origin.URL())
	go reveal(getenv, errw, origin.URL(), fresh, srv.seen)
	got := <-srv.accepted
	st = st.Step(conversation.BatchAccepted{Submission: got.submission})
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
	if err := lease.StoreArtifact("feedback", got.submission, encoded); err != nil {
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
	srv.returned(handOver(getenv, errw, c.Relay(), ln, spec, got.submission))
	return exitOK
}
