package live

import (
	"os"
	"testing"
)

// TestMain lets the test binary serve as the relay a returned call starts,
// since spawnRelay runs the current executable with the relay argument.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "relay" {
		os.Exit(Relay(os.Stderr))
	}
	os.Exit(m.Run())
}
