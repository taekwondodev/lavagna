package live

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "relay" {
		os.Exit(Relay(os.Stderr))
	}
	os.Exit(m.Run())
}
