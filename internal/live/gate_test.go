package live

import "testing"

func TestGateAdmit(t *testing.T) {
	open := gate{cap: "cap-a", round: "r2", token: "tok-2"}
	taken := open
	taken.admitted = "s-1"
	held := open
	held.paused = true

	cases := []struct {
		name     string
		gate     gate
		send     send
		verdict  verdict
		admitted string
	}{
		{"accept the first batch", open, send{"cap-a", "r2", "tok-2", "s-1"}, accept, "s-1"},
		{"duplicate of the admitted batch", taken, send{"cap-a", "r2", "tok-2", "s-1"}, duplicate, "s-1"},
		{"answered by another batch", taken, send{"cap-a", "r2", "tok-2", "s-2"}, answered, "s-1"},
		{"stale round", open, send{"cap-a", "r1", "tok-2", "s-1"}, stale, ""},
		{"stale token", open, send{"cap-a", "r2", "tok-1", "s-1"}, stale, ""},
		{"foreign conversation", open, send{"cap-b", "r2", "tok-2", "s-1"}, foreign, ""},
		{"paused after the deadline", held, send{"cap-a", "r2", "tok-2", "s-1"}, paused, ""},
		{"a stale tab while paused", held, send{"cap-a", "r1", "tok-2", "s-1"}, stale, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g, v := c.gate.admit(c.send)
			if v != c.verdict || g.admitted != c.admitted {
				t.Fatalf("got verdict %d admitted %q, want %d %q", v, g.admitted, c.verdict, c.admitted)
			}
		})
	}
}
