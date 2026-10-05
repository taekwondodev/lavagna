package witness

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taekwondodev/lavagna/internal/witnesstest"
)

func write(t *testing.T, path string, chunks ...[]byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, c := range chunks {
		if _, err := f.Write(c); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionFollowsWhatPiAppends(t *testing.T) {
	const quick = 5 * time.Millisecond
	cases := []struct {
		name   string
		sample string
		idle   time.Duration
		change func(t *testing.T, path string, after [][]byte)
		read   bool
		want   End
	}{
		{"answered", "answered", time.Minute, func(t *testing.T, path string, after [][]byte) {
			write(t, path, after...)
		}, true, Answered},
		{"entry written in two parts", "answered", time.Minute, func(t *testing.T, path string, after [][]byte) {
			write(t, path, after[0][:20])
			time.Sleep(4 * quick)
			write(t, path, after[0][20:])
			write(t, path, after[1:]...)
		}, true, Answered},
		{"retry after an error", "retry", time.Minute, func(t *testing.T, path string, after [][]byte) {
			write(t, path, after...)
		}, true, Unwitnessed},
		{"unread", "unread", time.Minute, func(t *testing.T, path string, after [][]byte) {
			write(t, path, after...)
		}, false, UnreadAborted},
		{"file shrinks", "answered", time.Minute, func(t *testing.T, path string, after [][]byte) {
			os.Truncate(path, 10)
		}, false, Unwitnessed},
		{"file replaced", "answered", time.Minute, func(t *testing.T, path string, after [][]byte) {
			os.Rename(path, path+".old")
			write(t, path, after...)
		}, false, Unwitnessed},
		{"silent past the idle bound", "answered", 20 * quick, func(t *testing.T, path string, after [][]byte) {}, false, Unwitnessed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, after := witnesstest.Sample(t, c.sample)
			for i := range after {
				after[i] = bytes.ReplaceAll(after[i], []byte(witnesstest.Placeholder), []byte("s-aaaaaaaaaaaaaaaaaaaaaaaa"))
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			write(t, path, before...)
			write(t, path, []byte(`{"type":"message","message":{"role":"user","content":"partial`))
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			write(t, path, []byte(`"}}`+"\n"))
			read := false
			done := make(chan End, 1)
			go func() {
				done <- s.follow(newWitness("s-aaaaaaaaaaaaaaaaaaaaaaaa"), quick, c.idle, nil, func() { read = true })
			}()
			time.Sleep(4 * quick)
			c.change(t, path, after)
			select {
			case got := <-done:
				if got != c.want || read != c.read {
					t.Fatalf("end %v, read %v; want %v, %v", got, read, c.want, c.read)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the witness kept watching")
			}
		})
	}
}

func TestSessionStopsWhenTheOriginIsTaken(t *testing.T) {
	before, _ := witnesstest.Sample(t, "answered")
	path := filepath.Join(t.TempDir(), "session.jsonl")
	write(t, path, before...)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	close(stop)
	if got := s.follow(newWitness(witnesstest.Placeholder), time.Millisecond, time.Minute, stop, func() {}); got != Stopped {
		t.Fatalf("end %v, want Stopped", got)
	}
}

func TestOpenRefusesAnUnknownSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	write(t, path, []byte(`{"type":"session","version":2,"id":"x"}`+"\n"))
	if _, err := Open(path); err == nil {
		t.Fatal("a version 2 session opened")
	}
}
