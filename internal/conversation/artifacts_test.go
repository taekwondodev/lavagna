package conversation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactsPrivateImmutableAndScopedByLease(t *testing.T) {
	c := Conversation{dir: t.TempDir()}
	lease, err := Acquire(c)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := lease.StoreArtifact("feedback", "s-12345678", []byte("private")); err != nil {
		t.Fatal(err)
	}
	if err := lease.StoreArtifact("feedback", "s-12345678", []byte("replace")); err == nil {
		t.Fatal("overwrote immutable artifact")
	}
	got, err := lease.ReadArtifact("feedback", "s-12345678", 256)
	if err != nil || string(got) != "private" {
		t.Fatalf("%q %v", got, err)
	}
	info, err := os.Stat(filepath.Join(c.dir, artifactsDir, "feedback", "s-12345678.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	if _, err := lease.ReadArtifact("feedback", "s-12345678", 3); err == nil {
		t.Fatal("read beyond artifact bound")
	}
	if _, err := lease.ReadArtifact("feedback", "../lock", 256); err == nil {
		t.Fatal("accepted traversal")
	}
}
