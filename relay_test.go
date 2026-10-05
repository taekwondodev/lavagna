package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/cdptest"
	"github.com/taekwondodev/lavagna/internal/witnesstest"
)

type piSession struct {
	t       *testing.T
	path    string
	appends [][]byte
}

func newPiSession(t *testing.T, sample string) *piSession {
	t.Helper()
	before, after := witnesstest.Sample(t, sample)
	s := &piSession{t: t, path: filepath.Join(t.TempDir(), "session.jsonl"), appends: after}
	if err := os.WriteFile(s.path, bytes.Join(before, nil), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

func (s *piSession) env() []string {
	return []string{"PI_SESSION_ID=" + filepath.Base(filepath.Dir(s.path)), "PI_SESSION_FILE=" + s.path}
}

func (s *piSession) append(submission string, n int) {
	s.t.Helper()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	defer f.Close()
	for _, line := range s.appends[:n] {
		if _, err := f.Write(bytes.ReplaceAll(line, []byte(witnesstest.Placeholder), []byte(submission))); err != nil {
			s.t.Fatal(err)
		}
	}
	s.appends = s.appends[n:]
}

type receiptEvent struct {
	Submission  string `json:"submission"`
	Stage       string `json:"stage"`
	Unwitnessed bool   `json:"unwitnessed"`
}

func receipts(t *testing.T, url string) <-chan receiptEvent {
	t.Helper()
	ch := make(chan receiptEvent, 32)
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			resp, err := http.Get(url + "events")
			if err != nil {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			sc := bufio.NewScanner(resp.Body)
			sc.Buffer(nil, 1<<20)
			name := ""
			for sc.Scan() {
				line := sc.Text()
				if n, ok := strings.CutPrefix(line, "event: "); ok {
					name = n
				} else if data, ok := strings.CutPrefix(line, "data: "); ok && name == "receipt" {
					var r receiptEvent
					json.Unmarshal([]byte(data), &r)
					ch <- r
				}
			}
			resp.Body.Close()
		}
	}()
	return ch
}

func awaitReceipt(t *testing.T, ch <-chan receiptEvent, want receiptEvent) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	var seen []receiptEvent
	for {
		select {
		case r := <-ch:
			if r == want {
				return
			}
			seen = append(seen, r)
		case <-deadline:
			t.Fatalf("no receipt %+v; saw %+v", want, seen)
		}
	}
}

func groupGone(t *testing.T, c *call) {
	t.Helper()
	for range 200 {
		if errors.Is(syscall.Kill(-c.cmd.Process.Pid, 0), syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("a process of call %d's group is still alive", c.cmd.Process.Pid)
}

func groupAlive(t *testing.T, c *call) {
	t.Helper()
	if err := syscall.Kill(-c.cmd.Process.Pid, 0); err != nil {
		t.Fatalf("no relay in call %d's group: %v", c.cmd.Process.Pid, err)
	}
}

func sendAndReturn(t *testing.T, c *call, submission string) {
	t.Helper()
	r, token := c.view()
	if code := c.post(c.url+"send", c.origin, "application/json", batch(r, token, submission)); code != http.StatusAccepted {
		t.Fatalf("send: status %d", code)
	}
	lines, code := c.finish()
	if code != 0 || !strings.Contains(last(lines), `"submission":"`+submission+`"`) {
		t.Fatalf("round: exit %d, output %q", code, lines)
	}
}

func TestRelayReportsTheAgentsTurn(t *testing.T) {
	type step struct {
		append      int
		stage       string
		unwitnessed bool
	}
	cases := []struct {
		sample string
		steps  []step
	}{
		{"answered", []step{{1, "received", false}, {1, "ended", false}}},
		{"unread", []step{{2, "unread-aborted", false}}},
		{"unrecognized", []step{{1, "received", false}, {1, "received", true}}},
		{"retry", []step{{1, "received", false}, {1, "received", true}}},
	}
	for i, tc := range cases {
		t.Run(tc.sample, func(t *testing.T) {
			pi := newPiSession(t, tc.sample)
			c := startRound(t, env(t, pi.env()...), decision)
			submission := "s-" + strings.Repeat("a", 23) + string(rune('0'+i))
			sendAndReturn(t, c, submission)
			groupAlive(t, c)

			ch := receipts(t, c.url)
			awaitReceipt(t, ch, receiptEvent{Submission: submission, Stage: "returned"})
			for _, s := range tc.steps {
				pi.append(submission, s.append)
				awaitReceipt(t, ch, receiptEvent{Submission: submission, Stage: s.stage, Unwitnessed: s.unwitnessed})
			}
			groupGone(t, c)
			unreachable(t, c.origin)
		})
	}
}

func TestReceiptsStopAtReturnedWithoutAWitness(t *testing.T) {
	unknown := newPiSession(t, "answered")
	if err := os.WriteFile(unknown.path, []byte(`{"type":"session","version":2,"id":"x"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"no session file":         {"LAVAGNA_SESSION=blind"},
		"unknown session version": unknown.env(),
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			c := startRound(t, env(t, vars...), decision)
			ch := receipts(t, c.url)
			sendAndReturn(t, c, "s-bbbbbbbbbbbbbbbbbbbbbbbb")
			awaitReceipt(t, ch, receiptEvent{Submission: "s-bbbbbbbbbbbbbbbbbbbbbbbb", Stage: "returned", Unwitnessed: true})
			groupGone(t, c)
			unreachable(t, c.origin)
		})
	}
}

func TestRelayHandsTheOriginOverWithoutFailingAPageRequest(t *testing.T) {
	pi := newPiSession(t, "next-round")
	environ := env(t, pi.env()...)
	first := startRound(t, environ, decision)
	url := first.url

	var mu sync.Mutex
	stage := "call to relay"
	var failures []string
	requests := 0
	at := func(s string) {
		mu.Lock()
		stage = s
		mu.Unlock()
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, path := range []string{"", "sw.js", "assets/lavagna.css"} {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				case <-time.After(2 * time.Millisecond):
				}
				resp, err := http.Get(url + path)
				mu.Lock()
				requests++
				if err != nil {
					failures = append(failures, stage+": "+err.Error())
				} else {
					if resp.StatusCode != http.StatusOK {
						failures = append(failures, stage+": "+path+" "+resp.Status)
					}
					resp.Body.Close()
				}
				mu.Unlock()
			}
		})
	}

	const handOffs = 20
	c := first
	for i := range handOffs {
		at("call to relay")
		sendAndReturn(t, c, fmt.Sprintf("s-%024x", i))
		groupAlive(t, c)
		time.Sleep(50 * time.Millisecond)
		at("relay to call")
		next := startRound(t, environ, decision)
		if next.url != url {
			t.Fatalf("hand-off %d serves %s, want %s", i, next.url, url)
		}
		groupGone(t, c)
		c = next
	}
	close(stop)
	wg.Wait()
	c.esc()
	if len(failures) > 0 || requests < handOffs {
		t.Fatalf("%d of %d page requests failed across %d hand-offs: %q", len(failures), requests, 2*handOffs, failures[:min(5, len(failures))])
	}
	t.Logf("%d page requests across %d hand-offs, none failed", requests, 2*handOffs)
}

func TestEscLeavesNoRelayAndTheNextCallRebinds(t *testing.T) {
	pi := newPiSession(t, "next-round")
	environ := env(t, pi.env()...)
	first := startRound(t, environ, decision)
	sendAndReturn(t, first, "s-dddddddddddddddddddddddd")
	groupAlive(t, first)

	second := startRound(t, environ, decision)
	groupGone(t, first)
	second.esc()
	groupGone(t, second)
	unreachable(t, second.origin)

	third := startRound(t, environ, decision)
	if third.url != first.url {
		t.Fatalf("the call after Esc serves %s, want the recorded origin %s", third.url, first.url)
	}
	sendAndReturn(t, third, "s-eeeeeeeeeeeeeeeeeeeeeeee")
	groupAlive(t, third)
	syscall.Kill(-third.cmd.Process.Pid, syscall.SIGKILL)
	groupGone(t, third)
	unreachable(t, third.origin)
}

func TestRelayStopsWhenTheHarnessQuitsMidTurn(t *testing.T) {
	pi := newPiSession(t, "answered")
	dir := t.TempDir()
	src := filepath.Join(dir, "round.md")
	finished := filepath.Join(dir, "finished")
	if err := os.WriteFile(src, []byte(decision), 0o600); err != nil {
		t.Fatal(err)
	}
	// Job control models the harness: a persistent parent outside the call's
	// process group. The shell survives the call, then quits mid-turn.
	cmd := exec.Command("bash", "-c", `set -m
"$1" round < "$2" &
child=$!
wait "$child" || exit
printf '%s' "$child" > "$3"
read -r hold
`, "harness", binary, src, finished)
	cmd.Env = env(t, pi.env()...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	out := bufio.NewReader(stdout)
	first, err := out.ReadString('\n')
	c := &call{t: t}
	c.status(first)
	if err != nil || c.url == "" {
		t.Fatalf("status %q: %v", first, err)
	}
	ch := receipts(t, c.url)
	r, token := c.view()
	const submission = "s-111111111111111111111111"
	if code := c.post(c.url+"send", c.origin, "application/json", batch(r, token, submission)); code != http.StatusAccepted {
		t.Fatalf("send: status %d", code)
	}
	var pid int
	for range 200 {
		b, _ := os.ReadFile(finished)
		pid, _ = strconv.Atoi(string(b))
		if pid > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("round did not return to the harness")
	}
	t.Cleanup(func() { syscall.Kill(-pid, syscall.SIGKILL) })
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	c.cmd = &exec.Cmd{Process: process}
	pi.append(submission, 1)
	awaitReceipt(t, ch, receiptEvent{Submission: submission, Stage: "received"})
	groupAlive(t, c)
	cmd.Process.Kill()
	cmd.Wait()
	awaitReceipt(t, ch, receiptEvent{Submission: submission, Stage: "received", Unwitnessed: true})
	groupGone(t, c)
	unreachable(t, c.origin)
}

func deliveryIs(p *cdptest.Page, text string) {
	p.WaitFor(`document.querySelector('#delivery').textContent === '` + text + `'`)
}

func TestPageKeepsGrillingOpenAfterTheAgentsTurn(t *testing.T) {
	cases := []struct {
		reason, turn, delivery string
	}{
		{"stop", "Grilling ancora aperto", "L’agente ha terminato il turno prima di chiudere la frontiera. Il tuo feedback è conservato."},
		{"aborted", "Turno interrotto", "Il turno dell’agente è stato interrotto. La frontiera non è stata chiusa e il tuo feedback è conservato."},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			const receivedText = "Letto dall’agente · l’agente lavora"
			pi := newPiSession(t, "answered")
			pi.appends[1] = bytes.ReplaceAll(pi.appends[1], []byte(`"stopReason":"stop"`), []byte(`"stopReason":"`+tc.reason+`"`))
			environ := env(t, pi.env()...)
			first, p, submission := sentAndReturned(t, environ)
			turnIs(p, "Inviato · consegnato al terminale")

			pi.append(submission, 1)
			deliveryIs(p, receivedText)
			turnIs(p, "L’agente lavora")
			p.Reload()
			roundShown(p, "1")
			deliveryIs(p, receivedText)

			pi.append(submission, 1)
			deliveryIs(p, tc.delivery)
			turnIs(p, tc.turn)
			p.WaitFor(`document.querySelector('#feedback-lead').textContent === 'Il grilling resta aperto. Questa pagina si aggiornerà al prossimo round.'`)
			groupGone(t, first)
			p.Reload()
			roundShown(p, "1")
			deliveryIs(p, tc.delivery)

			second := startRound(t, environ, nextRound)
			if second.url != first.url {
				t.Fatalf("round 2 serves %s, want the recorded origin %s", second.url, first.url)
			}
			roundShown(p, "2")
			turnIs(p, "Tocca a te")
			second.esc()
		})
	}
}

func sentAndReturned(t *testing.T, environ []string) (*call, *cdptest.Page, string) {
	t.Helper()
	c := startRound(t, environ, richRound)
	p := cdptest.Start(t).Open(c.url, 1280, 900)
	roundShown(p, "1")
	p.Click(`input[value="db"]`)
	p.Click("#send-feedback")
	lines, code := c.finish()
	if code != 0 {
		t.Fatalf("round 1: exit %d, output %q", code, lines)
	}
	var feedback struct{ Submission string }
	if err := json.Unmarshal([]byte(last(lines)), &feedback); err != nil {
		t.Fatal(err)
	}
	deliveryIs(p, returnedText)
	return c, p, feedback.Submission
}

func TestPageShowsATurnThatEndedBeforeReading(t *testing.T) {
	cases := []struct {
		reason, turn, delivery string
	}{
		{"stop", "Grilling ancora aperto", "Consegnato al terminale, ma il turno è terminato prima che l’agente lo leggesse."},
		{"aborted", "Turno interrotto", "Consegnato al terminale, ma il turno si è interrotto prima che l’agente lo leggesse."},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			pi := newPiSession(t, "unread")
			pi.appends[1] = bytes.ReplaceAll(pi.appends[1], []byte(`"stopReason":"aborted"`), []byte(`"stopReason":"`+tc.reason+`"`))
			c, p, submission := sentAndReturned(t, env(t, pi.env()...))
			pi.append(submission, 2)
			deliveryIs(p, tc.delivery)
			turnIs(p, tc.turn)
			groupGone(t, c)
		})
	}
}

func TestPageStopsClaimingTheAgentWorksWhenTheWitnessGivesUp(t *testing.T) {
	pi := newPiSession(t, "unrecognized")
	c, p, submission := sentAndReturned(t, env(t, pi.env()...))
	pi.append(submission, 1)
	deliveryIs(p, "Letto dall’agente · l’agente lavora")
	pi.append(submission, 1)
	deliveryIs(p, "Letto dall’agente · stato in tempo reale non disponibile")
	turnIs(p, "Letto dall’agente")
	groupGone(t, c)
	p.Reload()
	roundShown(p, "1")
	deliveryIs(p, "Letto dall’agente · stato in tempo reale non disponibile")
	turnIs(p, "Letto dall’agente")
}

func TestCloseTakesTheOriginFromTheRelay(t *testing.T) {
	pi := newPiSession(t, "close")
	environ := env(t, pi.env()...)
	c := startRound(t, environ, decision)
	ch := receipts(t, c.url)
	sendAndReturn(t, c, "s-ffffffffffffffffffffffff")
	pi.append("s-ffffffffffffffffffffffff", 2)
	awaitReceipt(t, ch, receiptEvent{Submission: "s-ffffffffffffffffffffffff", Stage: "received"})
	groupAlive(t, c)

	lines, code := run(t, environ, "", "close")
	if code != 0 || last(lines) != `{"lavagna":"closed","page":"shown"}` {
		t.Fatalf("close: exit %d, output %q", code, lines)
	}
	groupGone(t, c)
	unreachable(t, c.origin)
}
