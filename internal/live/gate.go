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
)

type gate struct {
	cap      string
	round    string
	token    string
	admitted string
}

func (g gate) admit(s send) (gate, verdict) {
	switch {
	case !same(s.cap, g.cap):
		return g, foreign
	case s.round != g.round || !same(s.token, g.token):
		return g, stale
	case g.admitted == "":
		g.admitted = s.submission
		return g, accept
	case g.admitted == s.submission:
		return g, duplicate
	default:
		return g, answered
	}
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
