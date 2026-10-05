package round

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/taekwondodev/lavagna/internal/page"
)

//go:embed help.txt
var Help string

var reserved = strings.TrimSuffix(page.FrameAsset, "/")

const (
	maxFiles        = 32
	maxExcerptBytes = 64 << 10
	source          = "round.md"
)

var Types = map[string]string{
	".js":   "text/javascript; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

type File struct {
	Name string
	Body []byte
}

type Dir struct {
	Source []byte
	Files  []File
	Bytes  int
}

func (d Dir) Names() map[string]bool {
	names := map[string]bool{}
	for _, f := range d.Files {
		names[f.Name] = true
	}
	return names
}

func Load(dir string) (Dir, []string) {
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, "round: "+fmt.Sprintf(format, args...)) }
	type entry struct {
		name string
		size int64
	}
	var entries []entry
	total := int64(0)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			fail("%s: symlinks are not allowed", name)
			return nil
		case name == ".":
			if !d.IsDir() {
				return fmt.Errorf("%s is not a directory", dir)
			}
			return nil
		case name == reserved || strings.HasPrefix(name, reserved+"/"):
			fail("%s: the path is reserved for lavagna", name)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			fail("%s: not a regular file", name)
			return nil
		case name != source && Types[strings.ToLower(path.Ext(name))] == "":
			fail("%s: unsupported file (round.md plus .js, .css, .png, .jpg, .jpeg, .gif, .webp or .svg)", name)
			return nil
		}
		if len(entries) == maxFiles {
			return fmt.Errorf("more than %d files", maxFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		entries = append(entries, entry{name, info.Size()})
		return nil
	})
	if err != nil {
		return Dir{}, append(errs, "round: "+err.Error())
	}
	if total > MaxBytes {
		fail("%d bytes exceed the %d byte bound", total, MaxBytes)
	}
	if len(errs) > 0 {
		return Dir{}, errs
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Dir{}, []string{"round: " + err.Error()}
	}
	defer root.Close()
	d := Dir{Bytes: int(total)}
	found := false
	for _, e := range entries {
		body, err := readExactly(root, e.name, e.size)
		if err != nil {
			return Dir{}, []string{fmt.Sprintf("round: %s: %v", e.name, err)}
		}
		if e.name == source {
			d.Source, found = body, true
			continue
		}
		d.Files = append(d.Files, File{e.name, body})
	}
	slices.SortFunc(d.Files, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	if !found {
		return Dir{}, []string{"round: no round.md in " + dir}
	}
	return d, nil
}

func openRegular(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("not a regular file")
	}
	return f, nil
}

func readExactly(root *os.Root, name string, size int64) ([]byte, error) {
	f, err := openRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != size {
		return nil, errors.New("changed while being read")
	}
	return body, nil
}

func Repository(wd string) Excerpter {
	return func(name string, from, to int) (string, error) {
		top, err := gitRoot(wd)
		if err != nil {
			return "", err
		}
		root, err := os.OpenRoot(top)
		if err != nil {
			return "", err
		}
		defer root.Close()
		f, err := openRegular(root, name)
		if err != nil {
			return "", fmt.Errorf("cannot read %s under the git root", name)
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(nil, maxExcerptBytes)
		var lines []string
		size := 0
		n := 0
		for n < to && sc.Scan() {
			n++
			if n < from {
				continue
			}
			size += len(sc.Bytes()) + 1
			if size > maxExcerptBytes {
				return "", fmt.Errorf("more than %d bytes", maxExcerptBytes)
			}
			if !utf8.Valid(sc.Bytes()) {
				return "", fmt.Errorf("line %d is not UTF-8 text", n)
			}
			lines = append(lines, sc.Text())
		}
		if errors.Is(sc.Err(), bufio.ErrTooLong) {
			return "", fmt.Errorf("a line exceeds %d bytes", maxExcerptBytes)
		}
		if sc.Err() != nil {
			return "", sc.Err()
		}
		if n < to {
			return "", fmt.Errorf("%s has %d lines", name, n)
		}
		return strings.Join(lines, "\n"), nil
	}
}

func gitRoot(wd string) (string, error) {
	dir, err := filepath.Abs(wd)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("the working directory is not inside a git repository")
		}
		dir = parent
	}
}
