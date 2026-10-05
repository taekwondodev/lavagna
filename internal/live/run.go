package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
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

func Round(getenv func(string) string, src io.Reader, out, errw io.Writer) int {
	c, err := conversation.FromEnv(getenv)
	if err != nil {
		return invalid(out, err.Error())
	}
	conversation.Sweep(c, time.Now())
	b, err := io.ReadAll(io.LimitReader(src, round.MaxBytes+1))
	if err != nil {
		return failure(out, err)
	}
	r, errs := round.Parse(b)
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
	ln, origin, fresh, err := bind(st.Origin)
	if err != nil {
		return failure(out, err)
	}
	st.Origin = &origin
	st.Rounds++
	st.Live = fmt.Sprintf("r%d", st.Rounds)
	if err := lease.Save(st); err != nil {
		return failure(out, err)
	}

	srv := newRound(origin, st.Live, conversation.Secret(16), r, c.Images())
	hs := &http.Server{Handler: srv.handler(), ReadHeaderTimeout: 10 * time.Second}
	go hs.Serve(ln)
	fmt.Fprintf(out, "lavagna · round %s · %s · Esc per interrompere\n", st.Live, origin.URL())
	go reveal(getenv, errw, origin.URL(), fresh, srv.seen)

	got := <-srv.accepted
	code := result(out, exitOK, got.line())
	srv.returned()
	stop(hs, srv)
	st.Live = ""
	lease.Save(st)
	return code
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
		page = showClosed(*st.Origin)
	}
	if err := lease.Discard(); err != nil {
		return failure(out, err)
	}
	return result(out, exitOK, struct {
		Lavagna string `json:"lavagna"`
		Page    string `json:"page"`
	}{"closed", page})
}

func showClosed(origin conversation.Origin) string {
	ln, err := net.Listen("tcp", origin.Host())
	if err != nil {
		return "not-connected"
	}
	srv := newClose(origin)
	hs := &http.Server{Handler: srv.handler(), ReadHeaderTimeout: 10 * time.Second}
	go hs.Serve(ln)
	defer stop(hs, srv)
	select {
	case <-srv.seen:
		return "shown"
	case <-time.After(closeGrace):
		return "not-connected"
	}
}

func bind(recorded *conversation.Origin) (net.Listener, conversation.Origin, bool, error) {
	if recorded != nil {
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

func stop(hs *http.Server, srv *server) {
	srv.stop()
	ctx, cancel := context.WithTimeout(context.Background(), drainGrace)
	defer cancel()
	hs.Shutdown(ctx)
}
