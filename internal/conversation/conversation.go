package conversation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	lockName   = "lock"
	tombstone  = "swept-"
	imagesDir  = "images"
	relayName  = "relay.sock"
	idleAfter  = 7 * 24 * time.Hour
	sweepGrace = 2 * time.Second
	sweepRetry = 20 * time.Millisecond
)

var keyPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

var errSwept = errors.New("conversation directory swept while locking")

var errNoIdentity = errors.New("no conversation identity: set PI_SESSION_ID and PI_SESSION_FILE, or LAVAGNA_SESSION")

var ErrBusy = errors.New("another lavagna call is live in this conversation")

type Conversation struct {
	Key  string
	root string
	dir  string
}

func FromEnv(getenv func(string) string) (Conversation, error) {
	var identity string
	if id, file := getenv("PI_SESSION_ID"), getenv("PI_SESSION_FILE"); id != "" && file != "" {
		identity = "pi\x00" + id + "\x00" + file
	} else if s := getenv("LAVAGNA_SESSION"); s != "" {
		identity = "lavagna\x00" + s
	} else {
		return Conversation{}, errNoIdentity
	}
	sum := sha256.Sum256([]byte(identity))
	key := hex.EncodeToString(sum[:8])
	cache, err := os.UserCacheDir()
	if err != nil {
		return Conversation{}, fmt.Errorf("no cache directory for lavagna state: %w", err)
	}
	root := filepath.Join(cache, "lavagna")
	return Conversation{Key: key, root: root, dir: filepath.Join(root, key)}, nil
}

func (c Conversation) Images() string { return filepath.Join(c.dir, imagesDir) }

func (c Conversation) Relay() string { return filepath.Join(c.dir, relayName) }

type Origin struct {
	Port int    `json:"port"`
	Cap  string `json:"cap"`
}

func (o Origin) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/s/%s/", o.Port, o.Cap) }

func (o Origin) Host() string { return fmt.Sprintf("127.0.0.1:%d", o.Port) }

type State struct {
	Origin   *Origin  `json:"origin,omitempty"`
	Rounds   int      `json:"rounds"`
	Live     string   `json:"live,omitempty"`
	Accepted string   `json:"accepted,omitempty"`
	Previous *Outcome `json:"previous,omitempty"`
	Anchors  []string `json:"anchors,omitempty"`
}

type Lease struct {
	conv Conversation
	lock *os.File
}

func Acquire(c Conversation) (*Lease, error) {
	deadline := time.Now().Add(sweepGrace)
	for {
		if err := os.MkdirAll(c.dir, 0o700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(filepath.Join(c.dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
		if err == nil {
			err = hold(f)
		}
		swept := errors.Is(err, errSwept) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrBusy) && untouched(c.dir, time.Now())
		if swept && time.Now().Before(deadline) {
			time.Sleep(sweepRetry)
			continue
		}
		if err != nil {
			return nil, err
		}
		now := time.Now()
		if err := os.Chtimes(c.dir, now, now); err != nil {
			f.Close()
			return nil, err
		}
		return &Lease{conv: c, lock: f}, nil
	}
}

func hold(f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
	held, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if named, err := os.Stat(f.Name()); err != nil || !os.SameFile(held, named) {
		f.Close()
		return errSwept
	}
	return nil
}

func Sweep(c Conversation, now time.Time) {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), tombstone) {
			os.RemoveAll(filepath.Join(c.root, e.Name()))
			continue
		}
		if !e.IsDir() || e.Name() == c.Key || !keyPattern.MatchString(e.Name()) {
			continue
		}
		dir := filepath.Join(c.root, e.Name())
		if !untouched(dir, now) {
			continue
		}
		f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR, 0)
		if errors.Is(err, fs.ErrNotExist) {
			os.Remove(dir)
			continue
		}
		if err != nil || hold(f) != nil {
			continue
		}
		buried := filepath.Join(c.root, tombstone+Secret(8))
		if !untouched(dir, now) || os.Rename(dir, buried) != nil {
			f.Close()
			continue
		}
		f.Close()
		os.RemoveAll(buried)
	}
}

func untouched(dir string, now time.Time) bool {
	info, err := os.Stat(dir)
	return errors.Is(err, fs.ErrNotExist) || err == nil && now.Sub(info.ModTime()) >= idleAfter
}

func (l *Lease) Release() { l.lock.Close() }

func (l *Lease) Load() (State, error) { return Peek(l.conv) }

func Peek(c Conversation) (State, error) {
	var s State
	b, err := os.ReadFile(filepath.Join(c.dir, "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("conversation state %s: %w", c.dir, err)
	}
	return s, nil
}

func (l *Lease) Save(s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(l.conv.dir, "state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(l.conv.dir, "state.json"))
}

func (l *Lease) Discard() error {
	defer l.Release()
	entries, err := os.ReadDir(l.conv.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == lockName {
			continue
		}
		if err := os.RemoveAll(filepath.Join(l.conv.dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func Secret(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
