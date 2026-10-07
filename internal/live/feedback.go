package live

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

type FeedbackRequest struct {
	Submission string
	Question   string
	Overview   bool
	All        bool
}

func Feedback(getenv func(string) string, out io.Writer, request FeedbackRequest) int {
	selectors := 0
	for _, set := range []bool{request.Question != "", request.Overview, request.All} {
		if set {
			selectors++
		}
	}
	if !submissionPattern.MatchString(request.Submission) || selectors > 1 {
		return invalid(out, "invalid feedback reference")
	}
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		return invalid(out, err.Error())
	}
	conversation.Sweep(c, time.Now())
	lease, err := conversation.Acquire(c)
	if errors.Is(err, conversation.ErrBusy) {
		return busy(out, c)
	}
	if err != nil {
		return failure(out, err)
	}
	defer lease.Release()
	if _, _, err := lease.Current(); err != nil {
		return failure(out, err)
	}
	b, err := lease.ReadArtifact("feedback", request.Submission, maxResultBytes)
	if err != nil {
		return invalid(out, "feedback not found in this conversation")
	}
	if request.All {
		// A record is returned as written, by whichever lavagna wrote it.
		if !json.Valid(b) {
			return failure(out, errors.New("stored feedback is invalid"))
		}
		fmt.Fprintf(out, "%s\n", b)
		return exitOK
	}
	var perQuestion phaseOutcomeLine
	if json.Unmarshal(b, &perQuestion) != nil || perQuestion.Submission != request.Submission || perQuestion.Questions == nil {
		return invalid(out, "this feedback record predates per-question feedback; read it with --all")
	}
	if request.Question != "" {
		question, ok := perQuestion.Questions[request.Question]
		if !ok || emptyPhaseQuestion(question) {
			return invalid(out, "question has no feedback in this batch")
		}
		return result(out, exitOK, struct {
			Question string `json:"question"`
			phaseQuestionLine
		}{request.Question, question})
	}
	if request.Overview {
		if perQuestion.Overview == nil {
			return result(out, exitOK, phaseQuestionLine{})
		}
		return result(out, exitOK, *perQuestion.Overview)
	}
	return result(out, exitOK, deferredSummary(perQuestion))
}

func emptyPhaseQuestion(q phaseQuestionLine) bool {
	return q.Choice == "" && q.Answer == nil && q.Messages == nil && q.Images == nil
}

func deferredSummary(line phaseOutcomeLine) phaseOutcomeLine {
	line.Deferred = true
	for id, q := range line.Questions {
		q.Messages = countIfList(q.Messages)
		q.Images = countIfList(q.Images)
		if answer, ok := q.Answer.(string); ok && answer != "" {
			q.Answer = true
		}
		line.Questions[id] = q
	}
	if line.Overview != nil {
		q := *line.Overview
		q.Messages = countIfList(q.Messages)
		q.Images = countIfList(q.Images)
		line.Overview = &q
	}
	return line
}

func countIfList(value any) any {
	switch values := value.(type) {
	case []string:
		if len(values) > 0 {
			return len(values)
		}
	case []any:
		if len(values) > 0 {
			return len(values)
		}
	}
	return value
}
