package round

import (
	"encoding/json"
	"errors"
)

const MaxSnapshotBytes = MaxBytes + 2<<20

type snapshot struct {
	Content  string    `json:"content"`
	Chapters []Chapter `json:"chapters"`
	Anchors  []string  `json:"anchors"`
	Files    []File    `json:"files"`
}

func EncodeSnapshot(r Round, files []File) ([]byte, error) {
	var chapters []Chapter
	for _, chapter := range r.Chapters {
		if chapter.ID != "decidere" {
			chapters = append(chapters, chapter)
		}
	}
	b, err := json.Marshal(snapshot{Content: r.Content, Chapters: chapters, Anchors: r.Anchors, Files: files})
	if err != nil {
		return nil, err
	}
	if len(b)+len(r.Decide) > MaxSnapshotBytes {
		return nil, errors.New("rendered content snapshot exceeds 6 MiB")
	}
	return b, nil
}

func DecodeSnapshot(b []byte) (Round, []File, error) {
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return Round{}, nil, err
	}
	if s.Content == "" {
		return Round{}, nil, errors.New("round has no reusable content")
	}
	return Round{Content: s.Content, Chapters: s.Chapters, Anchors: s.Anchors}, s.Files, nil
}

func Reuse(base, decisions Round) (Round, error) {
	if len(decisions.Chapters) != 1 || decisions.Chapters[0].ID != "decidere" || decisions.Content != "" {
		return Round{}, errors.New("reuse input must contain only # Decidere")
	}
	base.Decide = decisions.Decide
	base.Questions = decisions.Questions
	base.Chapters = append(base.Chapters, decisions.Chapters...)
	return base, nil
}
