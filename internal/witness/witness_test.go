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
			w := newPi(witnesstest.Placeholder)
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
	w := newPi("s-ffffffffffffffffffffffff")
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
		if v := newPi(witnesstest.Placeholder).entry([]byte(e)); v != unrecognized {
			t.Errorf("entry %s: verdict %v, want unrecognized", e, v)
		}
	}
}

func verdicts(r reader, lines [][]byte) []verdict {
	var got []verdict
	for _, l := range lines {
		got = append(got, r.entry(bytes.TrimSuffix(l, []byte("\n"))))
	}
	return got
}

// watchingBut is n verdicts, all watching except the ones at the given
// positions.
func watchingBut(n int, at map[int]verdict) []verdict {
	out := make([]verdict, n)
	for i, v := range at {
		out[i] = v
	}
	return out
}

func TestClaudeReadsRecordedTranscripts(t *testing.T) {
	_, transcript := witnesstest.Sample(t, "claude-answered")
	end := slices.IndexFunc(transcript, func(l []byte) bool { return bytes.Contains(l, []byte(`"stop_reason":"end_turn"`)) })
	cases := []struct {
		sample     string
		submission string
		want       []verdict
	}{
		{"claude-answered", witnesstest.Placeholder, watchingBut(len(transcript), map[int]verdict{0: received, end: answered})},
		{"claude-answered", "s-ffffffffffffffffffffffff", watchingBut(len(transcript), map[int]verdict{end: unread})},
		{"claude-interrupted", witnesstest.Placeholder, []verdict{received, aborted}},
	}
	for _, c := range cases {
		t.Run(c.sample+" "+c.submission, func(t *testing.T) {
			_, after := witnesstest.Sample(t, c.sample)
			if got := verdicts(newClaude(c.submission), after); !slices.Equal(got, c.want) {
				t.Fatalf("verdicts %v, want %v", got, c.want)
			}
		})
	}
}

func TestClaudeSeparatesInterruptionsAndSidechains(t *testing.T) {
	result := `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"{\"submission\":\"` + witnesstest.Placeholder + `\"}"}]}]}}`
	cases := []struct {
		name  string
		lines []string
		want  []verdict
	}{
		{"result in text blocks, then end", []string{result, `{"type":"assistant","message":{"stop_reason":"end_turn","content":[]}}`}, []verdict{received, answered}},
		{"interrupted before the result", []string{`{"type":"user","message":{"content":"[Request interrupted by user for tool use]"}}`}, []verdict{unreadAborted}},
		{"a sidechain ends no turn", []string{result, `{"type":"assistant","isSidechain":true,"message":{"stop_reason":"end_turn","content":[]}}`}, []verdict{received, watching}},
		{"a prompt is not an interruption", []string{`{"type":"user","message":{"content":"continua"}}`}, []verdict{watching}},
		{"a streaming entry without a stop reason", []string{`{"type":"assistant","message":{"stop_reason":null,"content":[]}}`}, []verdict{watching}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var lines [][]byte
			for _, l := range c.lines {
				lines = append(lines, []byte(l))
			}
			if got := verdicts(newClaude(witnesstest.Placeholder), lines); !slices.Equal(got, c.want) {
				t.Fatalf("verdicts %v, want %v", got, c.want)
			}
		})
	}
}

func TestClaudeRefusesShapesItDoesNotKnow(t *testing.T) {
	entries := []string{
		``,
		`not json`,
		`{"uuid":"x"}`,
		`{"type":"user"}`,
		`{"type":"assistant","message":{"stop_reason":"pause_turn","content":[]}}`,
		`{"type":"assistant","message":{"stop_reason":"stop_sequence","content":[]}}`,
		`{"type":"assistant","message":{"stop_reason":"max_tokens","content":[]}}`,
		`{"type":"assistant","message":{"stop_reason":"refusal","content":[]}}`,
		`{"type":"user","message":{"content":{"type":"text"}}}`,
		`{"type":"user","message":{"content":[{"text":"x"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text"}]}}`,
	}
	for _, e := range entries {
		if v := newClaude(witnesstest.Placeholder).entry([]byte(e)); v != unrecognized {
			t.Errorf("entry %s: verdict %v, want unrecognized", e, v)
		}
	}
}

func TestHermesObservationKeepsOnlyIDsAndOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		session string
		line    string
	}{
		{"tool result with a submission",
			`{"hook_event_name":"post_tool_call","tool_name":"terminal","tool_input":{"command":"lavagna round"},"session_id":"20261009_204921_feed48","cwd":"/w","profile":"default","extra":{"result":"{\"output\": \"{\\\"lavagna\\\":\\\"feedback\\\",\\\"submission\\\":\\\"s-0123456789abcdef01234567\\\",\\\"questions\\\":{}}\\n\", \"exit_code\": 0}","task_id":"t","tool_call_id":"c"}}`,
			"20261009_204921_feed48", `{"event":"tool","submissions":["s-0123456789abcdef01234567"]}`},
		{"completed turn",
			`{"hook_event_name":"on_session_end","tool_name":null,"tool_input":null,"session_id":"h","extra":{"completed":true,"failed":false,"interrupted":false,"turn_exit_reason":"text_response"}}`,
			"h", `{"event":"end","outcome":"completed"}`},
		{"interrupted turn",
			`{"hook_event_name":"on_session_end","session_id":"h","extra":{"completed":false,"failed":false,"interrupted":true}}`,
			"h", `{"event":"end","outcome":"interrupted"}`},
		{"failed turn",
			`{"hook_event_name":"on_session_end","session_id":"h","extra":{"completed":false,"failed":true,"interrupted":false}}`,
			"h", `{"event":"end","outcome":"interrupted"}`},
		{"tool result without a submission", `{"hook_event_name":"post_tool_call","session_id":"h","extra":{"result":"{\"output\": \"ok\"}"}}`, "", ""},
		{"legacy exit shape", `{"hook_event_name":"on_session_end","session_id":"h","extra":{"reason":"cli_close"}}`, "", ""},
		{"no session", `{"hook_event_name":"on_session_end","session_id":"","extra":{"completed":true,"failed":false,"interrupted":false}}`, "", ""},
		{"another event", `{"hook_event_name":"pre_tool_call","session_id":"h","extra":{}}`, "", ""},
		{"malformed", `{"hook_event_name":`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			session, line, ok := HermesObservation([]byte(c.payload))
			if ok != (c.line != "") || session != c.session || string(line) != c.line {
				t.Fatalf("HermesObservation = %q, %s, %v; want %q, %s", session, line, ok, c.session, c.line)
			}
		})
	}
}

func TestHermesReadsItsEvents(t *testing.T) {
	tool := `{"event":"tool","submissions":["` + witnesstest.Placeholder + `"]}`
	other := `{"event":"tool","submissions":["s-ffffffffffffffffffffffff"]}`
	cases := []struct {
		name  string
		lines []string
		want  []verdict
	}{
		{"answered", []string{other, tool, `{"event":"end","outcome":"completed"}`}, []verdict{watching, received, answered}},
		{"interrupted", []string{tool, `{"event":"end","outcome":"interrupted"}`}, []verdict{received, aborted}},
		{"unread", []string{`{"event":"end","outcome":"completed"}`}, []verdict{unread}},
		{"unknown", []string{`{"event":"end","outcome":"later"}`, `{"event":"pause"}`, `x`}, []verdict{unrecognized, unrecognized, unrecognized}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var lines [][]byte
			for _, l := range c.lines {
				lines = append(lines, []byte(l))
			}
			if got := verdicts(newHermes(witnesstest.Placeholder), lines); !slices.Equal(got, c.want) {
				t.Fatalf("verdicts %v, want %v", got, c.want)
			}
		})
	}
}
