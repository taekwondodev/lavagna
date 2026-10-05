package witness

import (
	"bytes"
	"slices"
	"testing"

	"github.com/taekwondodev/lavagna/internal/witnesstest"
)

func TestWitnessReadsRecordedPiSessions(t *testing.T) {
	cases := []struct {
		sample string
		want   []verdict
	}{
		{"answered", []verdict{received, answered}},
		{"next-round", []verdict{received, watching}},
		{"close", []verdict{received, watching, watching, answered}},
		{"unread", []verdict{watching, unreadAborted}},
		{"unrecognized", []verdict{received, unrecognized}},
		{"retry", []verdict{received, unrecognized, watching, watching, answered}},
	}
	for _, c := range cases {
		t.Run(c.sample, func(t *testing.T) {
			before, after := witnesstest.Sample(t, c.sample)
			if !header(bytes.TrimSuffix(before[0], []byte("\n"))) {
				t.Fatalf("header %s not recognized", before[0])
			}
			w := newWitness(witnesstest.Placeholder)
			var got []verdict
			for _, e := range after {
				got = append(got, w.entry(bytes.TrimSuffix(e, []byte("\n"))))
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("verdicts %v, want %v", got, c.want)
			}
		})
	}
}

func TestWitnessIgnoresAnotherSubmission(t *testing.T) {
	_, after := witnesstest.Sample(t, "answered")
	w := newWitness("s-ffffffffffffffffffffffff")
	var got []verdict
	for _, e := range after {
		got = append(got, w.entry(bytes.TrimSuffix(e, []byte("\n"))))
	}
	if want := []verdict{watching, unread}; !slices.Equal(got, want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
}

func TestWitnessRefusesShapesItDoesNotKnow(t *testing.T) {
	headers := []string{
		`{"type":"session","version":2,"id":"x"}`,
		`{"type":"session","id":"x"}`,
		`{"type":"message"}`,
		`not json`,
	}
	for _, h := range headers {
		if header([]byte(h)) {
			t.Errorf("header %s recognized", h)
		}
	}
	entries := []string{
		``,
		`{"id":"x"}`,
		`{"type":"message"}`,
		`{"type":"message","message":{"content":"x"}}`,
		`{"type":"message","message":{"role":"assistant","content":[]}}`,
		`{"type":"message","message":{"role":"assistant","stopReason":"pending","content":[]}}`,
		`{"type":"message","message":{"role":"assistant","stopReason":"error","content":[]}}`,
		`{"type":"message","message":{"role":"assistant","stopReason":"length","content":[]}}`,
		`{"type":"message","message":{"role":"toolResult","content":"text"}}`,
		`{"type":"message","message":{"role":"toolResult","content":[{"text":"x"}]}}`,
		`{"type":"message","message":{"role":"toolResult","content":[{"type":"text"}]}}`,
	}
	for _, e := range entries {
		if v := newWitness(witnesstest.Placeholder).entry([]byte(e)); v != unrecognized {
			t.Errorf("entry %s: verdict %v, want unrecognized", e, v)
		}
	}
}
