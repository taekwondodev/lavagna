package conversation

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const day = 24 * time.Hour

func inCache(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", "")
}

func conversationFor(t *testing.T, session string) Conversation {
	t.Helper()
	c, err := FromEnv(func(k string) string {
		if k == "LAVAGNA_SESSION" {
			return session
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func used(t *testing.T, session string, age time.Duration) Conversation {
	t.Helper()
	c := conversationFor(t, session)
	l, err := Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Save(State{Rounds: 1}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.Images(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Images(), "shot.png"), []byte("\x89PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	l.Release()
	setAge(t, c.dir, age)
	return c
}

func setAge(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	then := time.Now().Add(-age)
	if err := os.Chtimes(dir, then, then); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestSweepRemovesOnlyIdleConversations(t *testing.T) {
	inCache(t)
	own := used(t, "own", 30*day)
	stale := used(t, "stale", 7*day)
	fresh := used(t, "fresh", 7*day-time.Minute)
	live := used(t, "live", 30*day)
	held, err := os.OpenFile(filepath.Join(live.dir, lockName), os.O_RDWR, 0)
	if err != nil || hold(held) != nil {
		t.Fatal("cannot hold the live conversation's lock")
	}
	defer held.Close()
	foreign := filepath.Join(own.root, "notes")
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	setAge(t, foreign, 30*day)

	Sweep(own, time.Now())

	for path, want := range map[string]bool{own.dir: true, stale.dir: false, fresh.dir: true, live.dir: true, foreign: true} {
		if got := exists(path); got != want {
			t.Errorf("%s exists %v, want %v", path, got, want)
		}
	}
}

func TestSweepKeepsAConversationJustUsedAgain(t *testing.T) {
	inCache(t)
	resumed := used(t, "resumed", 30*day)
	l, err := Acquire(resumed)
	if err != nil {
		t.Fatal(err)
	}
	l.Release()
	Sweep(conversationFor(t, "other"), time.Now())
	if !exists(filepath.Join(resumed.Images(), "shot.png")) {
		t.Fatal("a conversation used again after 30 idle days was swept")
	}
}

func TestAcquireWaitsOutASweepOfItsConversation(t *testing.T) {
	inCache(t)
	resumed := used(t, "resumed", 30*day)
	sweeper, err := os.OpenFile(filepath.Join(resumed.dir, lockName), os.O_RDWR, 0)
	if err != nil || hold(sweeper) != nil {
		t.Fatal("cannot hold the lock as the sweeper")
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		os.RemoveAll(resumed.dir)
		sweeper.Close()
	}()
	l, err := Acquire(resumed)
	if err != nil {
		t.Fatalf("acquire during a sweep: %v, want the lease", err)
	}
	defer l.Release()
	if !exists(filepath.Join(resumed.dir, lockName)) {
		t.Fatal("the lease holds a lock that is no longer in the conversation directory")
	}
}

func TestAcquireDuringASweepNeverAnswersBusy(t *testing.T) {
	inCache(t)
	resumed := used(t, "resumed", 30*day)
	sweeper, err := os.OpenFile(filepath.Join(resumed.dir, lockName), os.O_RDWR, 0)
	if err != nil || hold(sweeper) != nil {
		t.Fatal("cannot hold the lock as the sweeper")
	}
	buried := filepath.Join(resumed.root, tombstone+"test")
	if err := os.Rename(resumed.dir, buried); err != nil {
		t.Fatal(err)
	}
	defer sweeper.Close()
	l, err := Acquire(resumed)
	if err != nil {
		t.Fatalf("acquire while the sweeper still holds the buried lock: %v, want the lease", err)
	}
	l.Release()
	Sweep(conversationFor(t, "other"), time.Now())
	if exists(buried) || !exists(resumed.dir) {
		t.Fatalf("after a sweep: tombstone exists %v, fresh conversation exists %v", exists(buried), exists(resumed.dir))
	}
}

func TestAcquireReportsALiveCallAsBusy(t *testing.T) {
	inCache(t)
	c := conversationFor(t, "live")
	first, err := Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	start := time.Now()
	if _, err := Acquire(c); err != ErrBusy {
		t.Fatalf("second acquire: %v, want ErrBusy", err)
	}
	if waited := time.Since(start); waited > sweepRetry*5 {
		t.Fatalf("a live call was reported busy after %v", waited)
	}
}
