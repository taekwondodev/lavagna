package conversation

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var artifactID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func (l *Lease) StoreArtifact(kind, id string, data []byte) error {
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
	return os.Link(tmp, name)
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
