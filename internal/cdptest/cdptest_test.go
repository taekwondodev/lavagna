package cdptest

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeBrowser(t *testing.T) (*Browser, *os.File) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return &Browser{t: t, w: w}, r
}

func TestShutdownLetsBrowserFinishCleanupWithoutKill(t *testing.T) {
	b, r := fakeBrowser(t)
	exited := make(chan error, 1)
	commands := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(r).ReadString(0)
		commands <- line
		const browserCleanupDelay = 20 * time.Millisecond
		time.Sleep(browserCleanupDelay)
		exited <- nil
	}()
	killed := false
	b.shutdown(exited, time.Second, func() {
		killed = true
		r.Close()
	})
	if killed {
		t.Fatal("killed a browser that quit on Browser.close")
	}
	if line := <-commands; !strings.Contains(line, `"method":"Browser.close"`) {
		t.Fatalf("expected Browser.close, got %q", line)
	}
}

func TestShutdownKillsUnresponsiveBrowserAfterGrace(t *testing.T) {
	b, _ := fakeBrowser(t)
	exited := make(chan error, 1)
	killed := false
	const grace = 20 * time.Millisecond
	started := time.Now()
	b.shutdown(exited, grace, func() {
		killed = true
		exited <- errors.New("browser killed")
	})
	if !killed {
		t.Fatal("did not kill a browser that ignored Browser.close")
	}
	if elapsed := time.Since(started); elapsed < grace {
		t.Fatalf("killed before the grace period: waited %s, want at least %s", elapsed, grace)
	}
}

func TestShutdownCleansGroupAfterBrowserCrash(t *testing.T) {
	b, _ := fakeBrowser(t)
	exited := make(chan error, 1)
	exited <- errors.New("browser crashed")
	killed := false
	b.shutdown(exited, time.Second, func() { killed = true })
	if !killed {
		t.Fatal("did not clean up the group after the browser crashed")
	}
}
