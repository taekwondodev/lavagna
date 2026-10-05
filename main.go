package main

import (
	"os"
	"strings"

	"github.com/taekwondodev/lavagna/internal/live"
)

const usage = `usage: lavagna check | round [DIR | --help] | close`

func main() {
	switch args := os.Args[1:]; {
	case len(args) == 1 && args[0] == "check":
		os.Exit(live.Check(os.Getenv, os.Stdout, os.Stderr))
	case len(args) == 1 && args[0] == "round":
		os.Exit(live.Round(os.Getenv, os.Stdin, "", os.Stdout, os.Stderr))
	case len(args) == 2 && args[0] == "round" && args[1] == "--help":
		os.Exit(live.RoundHelp(os.Stdout))
	case len(args) == 2 && args[0] == "round" && !strings.HasPrefix(args[1], "-"):
		os.Exit(live.Round(os.Getenv, os.Stdin, args[1], os.Stdout, os.Stderr))
	case len(args) == 1 && args[0] == "close":
		os.Exit(live.Close(os.Getenv, os.Stdout))
	default:
		os.Exit(live.Usage(os.Stdout, usage))
	}
}
