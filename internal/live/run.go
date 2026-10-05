package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/taekwondodev/lavagna/internal/conversation"
	"github.com/taekwondodev/lavagna/internal/round"
)

const (
	revealGrace = 2 * time.Second
	closeGrace  = 3 * time.Second
	drainGrace  = time.Second
	busyGrace   = 2 * time.Second
)

const (
	exitOK      = 0
	exitError   = 1
	exitInvalid = 2
	exitBusy    = 3
)

type feedbackLine struct {
	Lavagna    string            `json:"lavagna"`
	Round      string            `json:"round"`
	Submission string            `json:"submission"`
	Choices    map[string]string `json:"choices"`
	Comments   []comment         `json:"comments"`
	Images     []string          `json:"images"`
}

func (b batch) line() feedbackLine {
	return feedbackLine{
		Lavagna: "feedback", Round: b.round, Submission: b.submission,
		Choices: b.choices, Comments: b.comments, Images: b.images,
	}
}

func result(w io.Writer, code int, line any) int {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if enc.Encode(line) != nil {
		fmt.Fprintln(w, `{"lavagna":"error","message":"result encoding failed"}`)
		return exitError
	}
	return code
}

func invalid(w io.Writer, errs ...string) int {
	return result(w, exitInvalid, struct {
		Lavagna string   `json:"lavagna"`
		Errors  []string `json:"errors"`
	}{"invalid", errs})
}

func failure(w io.Writer, err error) int {
	return result(w, exitError, struct {
		Lavagna string `json:"lavagna"`
		Message string `json:"message"`
	}{"error", err.Error()})
}

func busy(w io.Writer, c conversation.Conversation) int {
	deadline := time.Now().Add(busyGrace)
	for {
		if st, err := conversation.Peek(c); err == nil && st.Live != "" {
			return result(w, exitBusy, struct {
				Lavagna string `json:"lavagna"`
				Round   string `json:"round"`
			}{"busy", st.Live})
		}
		if time.Now().After(deadline) {
			return failure(w, errors.New("another lavagna call holds this conversation"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func Usage(out io.Writer, usage string) int { return invalid(out, usage) }

func Check(getenv func(string) string, out, errw io.Writer) int {
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		fmt.Fprintln(errw, "lavagna:", err)
		return exitInvalid
	}
	conversation.Sweep(c, time.Now())
	fmt.Fprintln(out, "lavagna: conversation", c.Key)
	return exitOK
}

func RoundHelp(out io.Writer) int {
	io.WriteString(out, round.Help)
	return exitOK
}

func Round(getenv func(string) string, src io.Reader, dir string, out, errw io.Writer) int {
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		return invalid(out, err.Error())
	}
	conversation.Sweep(c, time.Now())
	in, files, errs := input(src, dir)
	if errs != nil {
		return invalid(out, errs...)
	}
	wd, err := os.Getwd()
	if err != nil {
		return failure(out, err)
	}
	in.Excerpt = round.Repository(wd)
	r, errs := round.Parse(in)
	if errs != nil {
		return invalid(out, errs...)
	}
	lease, err := conversation.Acquire(c)
	if errors.Is(err, conversation.ErrBusy) {
		return busy(out, c)
	}
	if err != nil {
		return failure(out, err)
	}
	defer lease.Release()

	st, err := lease.Load()
	if err != nil {
		return failure(out, err)
	}
	ln, origin, fresh, err := bind(c, st.Origin)
	if err != nil {
		return failure(out, err)
	}
	st = st.Step(conversation.RoundStarted{Origin: origin, Anchors: r.Anchors})
	if err := lease.Save(st); err != nil {
		return failure(out, err)
	}

	spec := roundSpec{
		Origin: origin, ID: st.Live, Token: conversation.Secret(16), FrameKey: conversation.Secret(16),
		Round: r, Files: files, Previous: st.Previous, Images: c.Images(),
	}
	srv := newRound(spec)
	for _, anchor := range st.Anchors {
		srv.anchors[anchor] = true
	}
	h := listen(srv, ln)
	fmt.Fprintf(out, "lavagna · round %s · %s · Esc per interrompere\n", st.Live, origin.URL())
	go reveal(getenv, errw, origin.URL(), fresh, srv.seen)

	got := <-srv.accepted
	st = st.Step(conversation.BatchAccepted{Submission: got.submission})
	record(errw, lease, st)
	code := result(out, exitOK, got.line())
	if code != exitOK {
		return code
	}
	st = st.Step(conversation.BatchReturned{})
	record(errw, lease, st)
	srv.returned(handOver(getenv, errw, c.Relay(), ln, spec, got.submission))
	h.stop()
	return code
}

func input(src io.Reader, dir string) (round.Input, []round.File, []string) {
	if dir != "" {
		d, errs := round.Load(dir)
		if errs != nil {
			return round.Input{}, nil, errs
		}
		return round.Input{Source: d.Source, Files: d.Names(), Budget: round.MaxBytes - d.Bytes}, d.Files, nil
	}
	b, err := io.ReadAll(io.LimitReader(src, round.MaxBytes+1))
	if err != nil {
		return round.Input{}, nil, []string{"round.md: " + err.Error()}
	}
	return round.Input{Source: b, Budget: round.MaxBytes - len(b)}, nil, nil
}

func record(errw io.Writer, lease *conversation.Lease, st conversation.State) {
	if err := lease.Save(st); err != nil {
		fmt.Fprintf(errw, "lavagna: cannot record the delivery stage (%v); the next round cannot tell the page how this one ended\n", err)
	}
}

func Close(getenv func(string) string, out io.Writer) int {
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		return invalid(out, err.Error())
	}
	conversation.Sweep(c, time.Now())
	lease, err := conversation.Acquire(c)
	if errors.Is(err, conversation.ErrBusy) {
		return busy(out, c)
	}
	if err != nil {
		return failure(out, err)
	}
	page := "not-connected"
	if st, err := lease.Load(); err == nil && st.Origin != nil {
		st.Live = ""
		lease.Save(st)
		page = showClosed(c, *st.Origin)
	}
	if err := lease.Discard(); err != nil {
		return failure(out, err)
	}
	return result(out, exitOK, struct {
		Lavagna string `json:"lavagna"`
		Page    string `json:"page"`
	}{"closed", page})
}

func showClosed(c conversation.Conversation, origin conversation.Origin) string {
	ln := take(c.Relay())
	if ln == nil {
		var err error
		if ln, err = net.Listen("tcp", origin.Host()); err != nil {
			return "not-connected"
		}
	}
	srv := newClose(origin)
	h := listen(srv, ln)
	defer h.stop()
	select {
	case <-srv.seen:
		return "shown"
	case <-time.After(closeGrace):
		return "not-connected"
	}
}

func bind(c conversation.Conversation, recorded *conversation.Origin) (net.Listener, conversation.Origin, bool, error) {
	if recorded != nil {
		if ln := take(c.Relay()); ln != nil {
			return ln, *recorded, false, nil
		}
		ln, err := net.Listen("tcp", recorded.Host())
		if err == nil {
			return ln, *recorded, false, nil
		}
		if !errors.Is(err, syscall.EADDRINUSE) {
			return nil, conversation.Origin{}, false, err
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, conversation.Origin{}, false, err
	}
	origin := conversation.Origin{Port: ln.Addr().(*net.TCPAddr).Port, Cap: conversation.Secret(32)}
	return ln, origin, true, nil
}

func reveal(getenv func(string) string, errw io.Writer, url string, fresh bool, seen <-chan struct{}) {
	if !fresh {
		select {
		case <-seen:
			return
		case <-time.After(revealGrace):
		}
	}
	cmd := exec.Command("open", url)
	if browser := strings.Fields(getenv("BROWSER")); len(browser) > 0 {
		cmd = exec.Command(browser[0], append(browser[1:], url)...)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(errw, "lavagna: cannot open the page (%v); open %s\n", err, url)
		return
	}
	go cmd.Wait()
}

type holding struct {
	srv    *server
	hs     *http.Server
	ln     net.Listener
	served chan struct{}
	mu     sync.Mutex
	open   map[net.Conn]struct{}
}

type connKey struct{}

func listen(srv *server, ln net.Listener) *holding {
	h := &holding{srv: srv, ln: ln, served: make(chan struct{}), open: map[net.Conn]struct{}{}}
	page := srv.handler()
	h.hs = &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer h.done(r.Context().Value(connKey{}).(net.Conn))
			page.ServeHTTP(w, r)
			w.(http.Flusher).Flush()
		}),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context { return context.WithValue(ctx, connKey{}, c) },
		ConnState: func(c net.Conn, st http.ConnState) {
			switch st {
			case http.StateNew:
				h.mu.Lock()
				h.open[c] = struct{}{}
				h.mu.Unlock()
			case http.StateClosed, http.StateHijacked:
				h.done(c)
			}
		},
	}
	h.hs.SetKeepAlivesEnabled(false)
	go func() {
		h.hs.Serve(ln)
		close(h.served)
	}()
	return h
}

func (h *holding) done(c net.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.open, c)
}

func (h *holding) answered() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.open) == 0
}

func (h *holding) stop() {
	h.srv.stop()
	h.ln.Close()
	<-h.served
	deadline := time.Now().Add(drainGrace)
	for !h.answered() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	h.hs.Close()
}
