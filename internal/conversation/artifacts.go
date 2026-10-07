package conversation

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var artifactID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

// StoreArtifact publishes a new artifact; an existing one is fs.ErrExist.
func (l *Lease) StoreArtifact(kind, id string, data []byte) error {
	return l.publish(kind, id, data, os.Link)
}

// ReplaceArtifact atomically publishes an artifact over an existing one.
func (l *Lease) ReplaceArtifact(kind, id string, data []byte) error {
	return l.publish(kind, id, data, os.Rename)
}

func (l *Lease) publish(kind, id string, data []byte, place func(tmp, name string) error) error {
	if !artifactID.MatchString(kind) || !artifactID.MatchString(id) {
		return errors.New("invalid artifact reference")
	}
	dir := filepath.Join(l.conv.dir, artifactsDir, kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	name := filepath.Join(dir, id+".json")
	f, err := os.CreateTemp(dir, ".artifact-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return place(tmp, name)
}

func (l *Lease) RemoveArtifact(kind, id string) error {
	if !artifactID.MatchString(kind) || !artifactID.MatchString(id) {
		return errors.New("invalid artifact reference")
	}
	return os.Remove(filepath.Join(l.conv.dir, artifactsDir, kind, id+".json"))
}

// Prune deletes every artifact of kind not named in keep, including leftovers
// of a call that died before its commit.
func (l *Lease) Prune(kind string, keep map[string]bool) error {
	if !artifactID.MatchString(kind) {
		return errors.New("invalid artifact reference")
	}
	dir := filepath.Join(l.conv.dir, artifactsDir, kind)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && keep[id] {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}

func (l *Lease) ReadArtifact(kind, id string, limit int) ([]byte, error) {
	if !artifactID.MatchString(kind) || !artifactID.MatchString(id) {
		return nil, errors.New("invalid artifact reference")
	}
	f, err := os.Open(filepath.Join(l.conv.dir, artifactsDir, kind, id+".json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, errors.New("artifact exceeds its storage bound")
	}
	return b, nil
}
