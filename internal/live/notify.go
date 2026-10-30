package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/rex"
	"github.com/taekwondodev/lavagna/internal/round"
)

const notifyGrace = 10 * time.Second

type notice string

const (
	noNotice     notice = ""
	firstNotice  notice = "first"
	updateNotice notice = "update"
)

func noticeFor(prior conversation.Ledger, phase round.Phase) notice {
	switch {
	case len(phase.Questions)+len(phase.Replies)+len(phase.Settled) == 0:
		return noNotice
	case len(prior.Questions) == 0 && len(prior.Overview) == 0:
		return firstNotice
	}
	return updateNotice
}

func notifier(getenv func(string) string) string {
	return filepath.Join(getenv("HOME"), "Applications", "Lavagna.app", "Contents", "MacOS", "lavagna-notifier")
}

func announce(getenv func(string) string, errw io.Writer, n notice, page string, target *rex.Target) {
	if n == noNotice {
		return
	}
	helper := notifier(getenv)
	if _, err := os.Stat(helper); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyGrace)
	defer cancel()
	args := []string{string(n), page}
	if target != nil {
		args = append(args, "--rex", target.Encode())
	}
	err := exec.CommandContext(ctx, helper, args...).Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit) && exit.ExitCode() == 3:
		fmt.Fprintln(errw, "lavagna: notification not sent; allow Lavagna in System Settings > Notifications")
	case errors.As(err, &exit) && exit.ExitCode() == 4:
		fmt.Fprintln(errw, "lavagna: notification not sent; Lavagna notifications are off in System Settings > Notifications")
	default:
		fmt.Fprintf(errw, "lavagna: notification not sent (%v)\n", err)
	}
}
