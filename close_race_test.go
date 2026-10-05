package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
)

func TestCloseDrainsSupersededRenderingBeforeAcknowledgingCleanup(t *testing.T) {
	environ := env(t, "LAVAGNA_SESSION=close-render-race")
	first := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(first.url, 1280, 900)
	settled(t, p)
	p.WaitFor(controlled)
	sendAndReturn(t, first, "s-0000000000000001")
	p.MustEval(`(() => {
	  const put = Cache.prototype.put;
	  Cache.prototype.put = async function(...args) {
	    if (!window.releaseWrite && String(args[0]).includes('/f/')) {
	      await new Promise(resolve => { window.releaseWrite = resolve; });
	    }
	    return put.apply(this, args);
	  };
	})()`, nil)
	second := startRound(t, environ, richRound)
	p.WaitFor(`Boolean(window.releaseWrite)`)
	sendAndReturn(t, second, "s-0000000000000002")
	third := startRound(t, environ, "# Decidere"+strings.SplitAfter(decision, "# Decidere")[1])
	roundShown(p, "3")
	sendAndReturn(t, third, "s-0000000000000003")

	cmd := exec.Command(binary, "close")
	cmd.Env = environ
	var output bytes.Buffer
	cmd.Stdout = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(10*time.Second, func() { cmd.Process.Kill() })
	defer timer.Stop()
	p.WaitFor(`!document.querySelector('#closed').hidden`)
	p.MustEval(`window.releaseWrite()`, nil)
	if err := cmd.Wait(); err != nil || strings.TrimSpace(output.String()) != `{"lavagna":"closed","page":"cleaned"}` {
		t.Fatalf("close %v: %s", err, output.String())
	}
	var remaining int
	p.MustEval(`(async () => (await caches.keys()).length + document.querySelectorAll('#document > *').length + Object.keys(localStorage).filter(k => k.startsWith('lavagna:')).length)()`, &remaining)
	if remaining != 0 {
		t.Fatalf("late rendering recreated %d retained objects", remaining)
	}
}
