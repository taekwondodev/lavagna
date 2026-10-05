package live

import (
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/taekwondodev/lavagna/internal/conversation"
)

const (
	feedbackTextPage  = 2048
	feedbackPageBytes = 4096
)

type FeedbackRequest struct {
	Submission string
	Comment    int
	Offset     int
	All        bool
}

type feedbackItem struct {
	Choice  string  `json:"choice,omitempty"`
	Value   string  `json:"value,omitempty"`
	Image   string  `json:"image,omitempty"`
	Comment int     `json:"comment,omitempty"`
	Anchor  *string `json:"anchor,omitempty"`
	Bytes   int     `json:"bytes,omitempty"`
}

func Feedback(getenv func(string) string, out io.Writer, request FeedbackRequest) int {
	if !submissionPattern.MatchString(request.Submission) || request.Comment < 0 || request.Offset < 0 || request.All && (request.Comment != 0 || request.Offset != 0) {
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
	b, err := lease.ReadArtifact("feedback", request.Submission, maxResultBytes)
	if err != nil {
		return invalid(out, "feedback not found in this conversation")
	}
	var line feedbackLine
	if json.Unmarshal(b, &line) != nil || line.Submission != request.Submission {
		return failure(out, errors.New("stored feedback is invalid"))
	}
	if request.All {
		return result(out, exitOK, line)
	}
	if request.Comment == 0 {
		return feedbackOverview(out, line, request.Offset)
	}
	if request.Comment > len(line.Comments) {
		return invalid(out, "comment index out of range")
	}
	text := line.Comments[request.Comment-1].Text
	if request.Offset > len(text) || request.Offset < len(text) && !utf8.RuneStart(text[request.Offset]) {
		return invalid(out, "offset is not a UTF-8 boundary")
	}
	end := min(request.Offset+feedbackTextPage, len(text))
	for end > request.Offset && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	for encodedLen(text[request.Offset:end])-len(`""`) > feedbackTextPage {
		_, size := utf8.DecodeLastRuneInString(text[request.Offset:end])
		end -= size
	}
	next := 0
	if end < len(text) {
		next = end
	}
	return result(out, exitOK, struct {
		Lavagna    string `json:"lavagna"`
		Submission string `json:"submission"`
		Comment    int    `json:"comment"`
		Offset     int    `json:"offset"`
		Text       string `json:"text"`
		Next       int    `json:"next"`
		Length     int    `json:"length"`
	}{"feedback", line.Submission, request.Comment, request.Offset, text[request.Offset:end], next, len(text)})
}

func feedbackOverview(out io.Writer, line feedbackLine, offset int) int {
	keys := make([]string, 0, len(line.Choices))
	for key := range line.Choices {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	items := make([]feedbackItem, 0, len(keys)+len(line.Images)+len(line.Comments))
	for _, key := range keys {
		items = append(items, feedbackItem{Choice: key, Value: line.Choices[key]})
	}
	for _, image := range line.Images {
		items = append(items, feedbackItem{Image: image})
	}
	for i, comment := range line.Comments {
		items = append(items, feedbackItem{Comment: i + 1, Anchor: comment.Anchor, Bytes: len(comment.Text)})
	}
	if offset > len(items) {
		return invalid(out, "overview offset out of range")
	}
	page := struct {
		Lavagna    string         `json:"lavagna"`
		Round      string         `json:"round"`
		Submission string         `json:"submission"`
		Items      []feedbackItem `json:"items"`
		Offset     int            `json:"offset"`
		Next       int            `json:"next"`
		Total      int            `json:"total"`
	}{"feedback", line.Round, line.Submission, []feedbackItem{}, offset, 0, len(items)}
	for i := offset; i < len(items); i++ {
		page.Items = append(page.Items, items[i])
		page.Next = i + 1
		if encodedLen(page) >= feedbackPageBytes {
			page.Items = page.Items[:len(page.Items)-1]
			page.Next = i
			if len(page.Items) == 0 {
				return failure(out, errors.New("feedback item exceeds the output bound"))
			}
			return result(out, exitOK, page)
		}
	}
	page.Next = 0
	return result(out, exitOK, page)
}
