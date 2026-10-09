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
	"github.com/taekwondodev/lavagna/internal/round"
)

// notifyGrace bounds the helper, which itself gives up waiting for consent.
const notifyGrace = 10 * time.Second

// notice is the macOS notification a call earns.
type notice string

const (
	noNotice     notice = ""
	firstNotice  notice = "first"
	updateNotice notice = "update"
)

// noticeFor classifies a validated call against the ledger it applies to.
// Only authored content counts, once per call: an empty call resumes waiting.
func noticeFor(prior conversation.Ledger, phase round.Phase) notice {
	switch {
	case len(phase.Questions)+len(phase.Replies)+len(phase.Settled) == 0:
		return noNotice
	case len(prior.Questions) == 0 && len(prior.Overview) == 0:
		return firstNotice
	}
	return updateNotice
}

// notifier is the helper make install builds from macos/notifier. It shows
// fixed text for the notice and brings page forward when it is clicked.
func notifier(getenv func(string) string) string {
	return filepath.Join(getenv("HOME"), "Applications", "Lavagna.app", "Contents", "MacOS", "lavagna-notifier")
}

// announce asks the helper to post n. Success means macOS accepted the
// request, not that it showed it: Focus and notification settings can still
// hold it back. Without the helper there is nothing to report.
func announce(getenv func(string) string, errw io.Writer, n notice, page string) {
	if n == noNotice {
		return
	}
	helper := notifier(getenv)
	if _, err := os.Stat(helper); err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), notifyGrace)
	defer cancel()
	err := exec.CommandContext(ctx, helper, string(n), page).Run()
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
