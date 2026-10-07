package live

import (
	"testing"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/round"
)

// phaseSpec serves source as the first call of a phase, as PhaseRound would:
// parsed, applied to an empty ledger and rendered with its question resources.
func phaseSpec(t *testing.T, o conversation.Origin, source string, files []round.File) roundSpec {
	t.Helper()
	phase, errs := round.ParsePhase([]byte(source))
	if errs == nil {
		errs = round.AttachPhaseFiles(&phase, files)
	}
	var ledger conversation.Ledger
	if errs == nil {
		ledger, errs = conversation.Ledger{}.Apply(call(phase))
	}
	if errs == nil {
		errs = round.RenderPhase(&phase, nil, recap(ledger))
	}
	if errs != nil {
		t.Fatal(errs)
	}
	return roundSpec{Origin: o, ID: "r1", Call: "c1", Token: "tok-1", FrameKey: conversation.Secret(16), Images: t.TempDir(), Ledger: ledger, Questions: phase.Questions}
}
