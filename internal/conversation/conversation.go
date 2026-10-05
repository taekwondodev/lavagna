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
	"syscall"
)

const lockName = "lock"

var errNoIdentity = errors.New("no conversation identity: set PI_SESSION_ID and PI_SESSION_FILE, or LAVAGNA_SESSION")

var ErrBusy = errors.New("another lavagna call is live in this conversation")

type Conversation struct {
	Key string
	dir string
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
	return Conversation{Key: key, dir: filepath.Join(cache, "lavagna", key)}, nil
}

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
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(c.dir, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return &Lease{conv: c, lock: f}, nil
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
