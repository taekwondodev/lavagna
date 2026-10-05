package live

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/taekwondodev/lavagna/internal/witness"
)

const (
	handoffGrace = 2 * time.Second
	settleGrace  = 2 * time.Second
	settlePoll   = 20 * time.Millisecond
)

const (
	relayListener = 3 + iota
	relaySocket
	relaySpec
)

const (
	handed   = 'L'
	released = 'N'
	kept     = 'K'
)

type relayed struct {
	Round      roundSpec
	Submission string
	Session    witness.Session
	Owner      relayOwner
}

func handOver(getenv func(string) string, errw io.Writer, sock string, ln net.Listener, spec roundSpec, submission string) bool {
	path := getenv("PI_SESSION_FILE")
	if path == "" {
		return false
	}
	session, err := witness.Open(path)
	if err != nil {
		return false
	}
	owner, err := findRelayOwner()
	if err == nil {
		err = spawnRelay(sock, ln, relayed{Round: spec, Submission: submission, Session: session, Owner: owner})
	}
	if err != nil {
		fmt.Fprintf(errw, "lavagna: cannot keep the page connected during the agent's turn (%v); live status is unavailable\n", err)
		return false
	}
	return true
}

func spawnRelay(sock string, ln net.Listener, r relayed) (err error) {
	defer func() {
		if err != nil {
			os.Remove(sock)
		}
	}()
	tcp, err := dupSocket(ln.(*net.TCPListener), "origin")
	if err != nil {
		return err
	}
	defer tcp.Close()
	os.Remove(sock)
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return err
	}
	ul.SetUnlinkOnClose(false)
	unix, err := dupSocket(ul, "relay")
	ul.Close()
	if err != nil {
		return err
	}
	defer unix.Close()
	specR, specW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer specW.Close()
	exe, err := os.Executable()
	if err != nil {
		specR.Close()
		return err
	}
	cmd := exec.Command(exe, "relay")
	cmd.ExtraFiles = []*os.File{tcp, unix, specR}
	err = cmd.Start()
	specR.Close()
	if err != nil {
		return err
	}
	cmd.Process.Release()
	specW.SetWriteDeadline(time.Now().Add(handoffGrace))
	return gob.NewEncoder(specW).Encode(r)
}

func dupSocket(c syscall.Conn, name string) (*os.File, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd, dupErr := -1, error(nil)
	err = rc.Control(func(s uintptr) {
		syscall.ForkLock.RLock()
		defer syscall.ForkLock.RUnlock()
		if fd, dupErr = syscall.Dup(int(s)); dupErr == nil {
			syscall.CloseOnExec(fd)
		}
	})
	if err = errors.Join(err, dupErr); err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func take(sock string) net.Listener {
	conn, err := net.DialTimeout("unix", sock, handoffGrace)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(handoffGrace))
	b := make([]byte, 1)
	oob := make([]byte, syscall.CmsgSpace(4))
	n, oobn, _, _, err := conn.(*net.UnixConn).ReadMsgUnix(b, oob)
	if err != nil || n != 1 || b[0] != handed {
		return nil
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) != 1 {
		return nil
	}
	fds, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) != 1 {
		return nil
	}
	f := os.NewFile(uintptr(fds[0]), "origin")
	defer f.Close()
	ln, err := net.FileListener(f)
	if err != nil {
		return nil
	}
	if _, err := conn.Write([]byte{kept}); err != nil {
		ln.Close()
		return nil
	}
	return ln
}

func Relay(errw io.Writer) int {
	tcp := os.NewFile(relayListener, "origin")
	sock := os.NewFile(relaySocket, "relay")
	specR := os.NewFile(relaySpec, "spec")
	var r relayed
	err := gob.NewDecoder(specR).Decode(&r)
	specR.Close()
	ln, lnErr := net.FileListener(tcp)
	hl, hlErr := net.FileListener(sock)
	sock.Close()
	if err = errors.Join(err, lnErr, hlErr); err != nil {
		fmt.Fprintln(errw, "lavagna: relay is internal to lavagna round:", err)
		return exitError
	}

	srv := newRound(r.Round)
	srv.gate.admitted = r.Submission
	srv.stage = returned
	page := listen(srv, ln)

	h := &handoff{origin: tcp, ln: ln, taken: make(chan struct{})}
	go h.serve(hl.(*net.UnixListener))

	stop := make(chan struct{})
	end := make(chan witness.End, 1)
	go func() {
		end <- r.Session.Follow(r.Submission, stop, func() { srv.advance(received) })
	}()

	select {
	case <-h.taken:
		close(stop)
	case <-r.Owner.follow(stop):
		close(stop)
		srv.unwitness()
		h.settle(srv)
		h.release()
	case e := <-end:
		close(stop)
		switch e {
		case witness.Unread:
			srv.advance(unread)
		case witness.UnreadAborted:
			srv.advance(unreadAborted)
		case witness.Answered:
			srv.advance(ended)
		case witness.Aborted:
			srv.advance(aborted)
		case witness.Unwitnessed, witness.Stopped:
			srv.unwitness()
		}
		h.settle(srv)
		h.release()
	}
	hl.Close()
	page.stop()
	return exitOK
}

type handoff struct {
	mu     sync.Mutex
	origin *os.File
	ln     net.Listener
	taken  chan struct{}
}

func (h *handoff) serve(hl *net.UnixListener) {
	for {
		conn, err := hl.AcceptUnix()
		if err != nil {
			return
		}
		h.give(conn)
		conn.Close()
	}
}

func (h *handoff) give(conn *net.UnixConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	conn.SetDeadline(time.Now().Add(handoffGrace))
	if h.origin == nil {
		conn.Write([]byte{released})
		return
	}
	if _, _, err := conn.WriteMsgUnix([]byte{handed}, syscall.UnixRights(int(h.origin.Fd())), nil); err != nil {
		return
	}
	b := make([]byte, 1)
	if n, err := conn.Read(b); err != nil || n != 1 || b[0] != kept {
		return
	}
	h.closeOrigin()
	close(h.taken)
}

func (h *handoff) settle(srv *server) {
	deadline := time.Now().Add(settleGrace)
	for !srv.watched() && time.Now().Before(deadline) {
		select {
		case <-h.taken:
			return
		case <-time.After(settlePoll):
		}
	}
}

func (h *handoff) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.origin != nil {
		h.closeOrigin()
	}
}

func (h *handoff) closeOrigin() {
	h.ln.Close()
	h.origin.Close()
	h.origin = nil
}
