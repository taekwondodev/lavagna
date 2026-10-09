package live

import "crypto/subtle"

type send struct {
	cap        string
	round      string
	token      string
	submission string
}

type verdict int

const (
	accept verdict = iota + 1
	duplicate
	answered
	stale
	foreign
	paused
)

type gate struct {
	cap      string
	round    string
	token    string
	admitted string
	// paused closes admission after the call's deadline; the next call
	// opens a new gate for the same round.
	paused bool
}

func (g gate) admit(s send) (gate, verdict) {
	switch {
	case !same(s.cap, g.cap):
		return g, foreign
	case !g.current(s.round, s.token):
		return g, stale
	case g.paused:
		return g, paused
	case g.admitted == "":
		g.admitted = s.submission
		return g, accept
	case g.admitted == s.submission:
		return g, duplicate
	default:
		return g, answered
	}
}

func (g gate) open(round, token string) bool {
	return g.current(round, token) && g.admitted == "" && !g.paused
}

func (g gate) current(round, token string) bool { return round == g.round && same(token, g.token) }

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
