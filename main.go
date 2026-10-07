package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/taekwondodev/lavagna/internal/live"
)

const usage = `usage: lavagna check | round [DIR] | round --help [grammar] | feedback SUBMISSION [--question ID | --overview | --all] | feedback --help | close`

const feedbackHelp = `lavagna feedback SUBMISSION [--question ID | --overview | --all]
Read retained feedback before close. The reference is the round result's submission.
Default reprints the bounded deferred summary. --question ID returns that question's
choice or answer, messages and images; --overview returns Overview feedback; --all
returns the complete feedback record (at most 48 KiB). Selectors are mutually exclusive.
An unknown question, or a question with no feedback in that batch, is an error.
Read all relevant feedback and images before acting; counts do not contain the text.
`

func main() { os.Exit(runCommand(os.Args[1:])) }

func runCommand(args []string) int {
	if len(args) == 0 {
		return live.Usage(os.Stdout, usage)
	}
	switch args[0] {
	case "check":
		if len(args) == 1 {
			return live.Check(os.Getenv, os.Stdout)
		}
	case "close":
		if len(args) == 1 {
			return live.Close(os.Getenv, os.Stdout)
		}
	case "relay":
		if len(args) == 1 {
			return live.Relay(os.Stderr)
		}
	case "--help":
		if len(args) == 1 {
			fmt.Fprintln(os.Stdout, usage)
			return 0
		}
	case "round":
		if len(args) >= 2 && args[1] == "--help" {
			if len(args) == 2 {
				return live.RoundHelp(os.Stdout, "")
			}
			if len(args) == 3 {
				return live.RoundHelp(os.Stdout, args[2])
			}
			break
		}
		dir := ""
		for _, arg := range args[1:] {
			if strings.HasPrefix(arg, "-") || dir != "" {
				return live.Usage(os.Stdout, usage)
			}
			dir = arg
		}
		return live.PhaseRound(os.Getenv, os.Stdin, dir, os.Stdout, os.Stderr)
	case "feedback":
		if len(args) == 2 && args[1] == "--help" {
			fmt.Print(feedbackHelp)
			return 0
		}
		if len(args) < 2 {
			break
		}
		request := live.FeedbackRequest{Submission: args[1]}
		selected := false
		for i := 2; i < len(args); i++ {
			if selected {
				return live.Usage(os.Stdout, usage)
			}
			selected = true
			switch args[i] {
			case "--all":
				request.All = true
			case "--overview":
				request.Overview = true
			case "--question":
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return live.Usage(os.Stdout, "--question requires an ID")
				}
				i++
				request.Question = args[i]
			default:
				return live.Usage(os.Stdout, usage)
			}
		}
		return live.Feedback(os.Getenv, os.Stdout, request)
	}
	return live.Usage(os.Stdout, usage)
}
