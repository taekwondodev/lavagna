package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/taekwondodev/lavagna/internal/live"
)

const usage = `usage: lavagna check | round [--reuse rN] [DIR] | round --help [grammar] | feedback SUBMISSION [--all | --comment N] [--offset N] | feedback --help | close`

const feedbackHelp = `lavagna feedback SUBMISSION [--all | --comment N] [--offset N]
Read retained feedback before close. The reference is the round result's submission.
Default: overview items (choice/value, image path, or comment index/anchor/bytes).
Comments are one-based; an omitted anchor means a general comment.
--offset N continues the overview at an item offset, or a selected comment at a
UTF-8 byte offset. Copy next into --offset; next:0 means complete.
--comment N returns exact text, offset, next and total byte length.
Overview pages stay below 4 KiB; comment text pages use at most 2 KiB of JSON.
--all returns the complete record (at most 48 KiB), without other selectors.
Use it when all feedback is needed; paging all comments adds calls and metadata.
Follow all overview pages and read relevant comments/images before deciding;
counts and delivery receipts do not mean the full feedback has been read.
Missing/foreign references and invalid offsets are errors, never empty feedback.
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
		var dir, reuse string
		for i := 1; i < len(args); i++ {
			switch {
			case args[i] == "--reuse" && reuse == "" && i+1 < len(args):
				i++
				reuse = args[i]
				if reuse == "" {
					return live.Usage(os.Stdout, "--reuse requires a round ID")
				}
			case !strings.HasPrefix(args[i], "-") && dir == "" && args[i] != "":
				dir = args[i]
			default:
				return live.Usage(os.Stdout, usage)
			}
		}
		return live.Round(os.Getenv, os.Stdin, dir, reuse, os.Stdout, os.Stderr)
	case "feedback":
		if len(args) == 2 && args[1] == "--help" {
			fmt.Print(feedbackHelp)
			return 0
		}
		if len(args) < 2 {
			break
		}
		request := live.FeedbackRequest{Submission: args[1]}
		if len(args) == 3 && args[2] == "--all" {
			request.All = true
			return live.Feedback(os.Getenv, os.Stdout, request)
		}
		seen := map[string]bool{}
		for i := 2; i < len(args); i += 2 {
			flag := args[i]
			if i+1 == len(args) || seen[flag] || flag != "--comment" && flag != "--offset" {
				return live.Usage(os.Stdout, usage)
			}
			seen[flag] = true
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 0 || flag == "--comment" && n == 0 {
				return live.Usage(os.Stdout, "comment must be positive; offset must be non-negative")
			}
			if flag == "--comment" {
				request.Comment = n
			} else {
				request.Offset = n
			}
		}
		return live.Feedback(os.Getenv, os.Stdout, request)
	}
	return live.Usage(os.Stdout, usage)
}
