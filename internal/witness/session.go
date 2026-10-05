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
	poll      = 100 * time.Millisecond
	idle      = 30 * time.Minute
)

var errHeader = errors.New("unrecognized session header")

type End int

const (
	Stopped End = iota
	Unread
	Answered
	Unwitnessed
)

type Session struct {
	Path   string
	Offset int64
}

func Open(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	first, err := bufio.NewReader(io.LimitReader(f, maxHeader)).ReadBytes('\n')
	if err != nil || !header(first) {
		return Session{}, errHeader
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
	return Session{Path: path, Offset: offset}, nil
}

func (s Session) Follow(submission string, stop <-chan struct{}, read func()) End {
	return s.follow(newWitness(submission), poll, idle, stop, read)
}

func (s Session) follow(w *witness, poll, idle time.Duration, stop <-chan struct{}, read func()) End {
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
				case answered:
					return Answered
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
