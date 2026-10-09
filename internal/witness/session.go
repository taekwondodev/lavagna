package witness

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"time"
)

const (
	maxHeader = 64 << 10
	// maxLine bounds one record line; a longer one ends the observation.
	maxLine = 16 << 20
	poll    = 100 * time.Millisecond
	idle    = 30 * time.Minute
)

var errHeader = errors.New("unrecognized session header")

// Format names the record a harness appends while the agent works.
type Format string

const (
	PiSession        Format = "pi"
	ClaudeTranscript Format = "claude"
	HermesEvents     Format = "hermes"
)

type End int

const (
	Stopped End = iota
	Unread
	UnreadAborted
	Answered
	Aborted
	Unwitnessed
)

type Session struct {
	Path   string
	Format Format
	Offset int64
}

// Open starts observing a record after its last complete line. Only Pi
// sessions declare a version to check. The Hermes events file is created
// empty when no hook has written yet.
func Open(path string, format Format) (Session, error) {
	flag := os.O_RDONLY
	if format == HermesEvents {
		flag |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	if format == PiSession {
		first, err := bufio.NewReader(io.LimitReader(f, maxHeader)).ReadBytes('\n')
		if err != nil || !header(first) {
			return Session{}, errHeader
		}
	}
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return Session{}, err
	}
	offset := size
	tail := make([]byte, min(size, 1<<20))
	if _, err := f.ReadAt(tail, size-int64(len(tail))); err != nil {
		return Session{}, err
	}
	if i := bytes.LastIndexByte(tail, '\n'); i >= 0 {
		offset = size - int64(len(tail)) + int64(i) + 1
	}
	return Session{Path: path, Format: format, Offset: offset}, nil
}

func (s Session) Follow(submission string, stop <-chan struct{}, read func()) End {
	var r reader
	switch s.Format {
	case PiSession:
		r = newPi(submission)
	case ClaudeTranscript:
		r = newClaude(submission)
	case HermesEvents:
		r = newHermes(submission)
	default:
		return Unwitnessed
	}
	return s.follow(r, poll, idle, stop, read)
}

func (s Session) follow(w reader, poll, idle time.Duration, stop <-chan struct{}, read func()) End {
	f, err := os.Open(s.Path)
	if err != nil {
		return Unwitnessed
	}
	defer f.Close()
	pos := s.Offset
	var partial []byte
	buf := make([]byte, 64<<10)
	quiet := time.Now()
	for {
		n, err := f.ReadAt(buf, pos)
		if err != nil && err != io.EOF {
			return Unwitnessed
		}
		if n > 0 {
			pos += int64(n)
			quiet = time.Now()
			partial = append(partial, buf[:n]...)
			if len(partial) > maxLine && bytes.IndexByte(partial, '\n') < 0 {
				return Unwitnessed
			}
			for {
				i := bytes.IndexByte(partial, '\n')
				if i < 0 {
					break
				}
				line := partial[:i]
				partial = partial[i+1:]
				switch w.entry(line) {
				case watching:
				case received:
					read()
				case unread:
					return Unread
				case unreadAborted:
					return UnreadAborted
				case answered:
					return Answered
				case aborted:
					return Aborted
				case unrecognized:
					return Unwitnessed
				}
			}
			continue
		}
		if info, err := f.Stat(); err != nil || info.Size() < pos {
			return Unwitnessed
		}
		if named, err := os.Stat(s.Path); err != nil || !sameFile(f, named) {
			return Unwitnessed
		}
		if time.Since(quiet) >= idle {
			return Unwitnessed
		}
		select {
		case <-stop:
			return Stopped
		case <-time.After(poll):
		}
	}
}

func sameFile(f *os.File, named os.FileInfo) bool {
	held, err := f.Stat()
	return err == nil && os.SameFile(held, named)
}
