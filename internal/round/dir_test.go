package round

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadReadsTheRoundDirectory(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"round.md": "# Capire\ntesto\n", "demo.js": "x", "stile.css": "y", "img/flusso.svg": "<svg/>", "a-c.js": "", "a/b.js": ""}
	for i := range maxFiles - len(files) {
		files[fmt.Sprintf("img/%02d.png", i)] = "p"
	}
	write(t, dir, files)
	d, errs := Load(dir)
	if errs != nil {
		t.Fatal(errs)
	}
	if string(d.Source) != files["round.md"] || len(d.Files) != maxFiles-1 {
		t.Fatalf("source %q and %d files, want round.md and %d files", d.Source, len(d.Files), maxFiles-1)
	}
	var order []string
	for _, f := range d.Files {
		order = append(order, f.Name)
	}
	if !slices.IsSorted(order) || order[0] != "a-c.js" || order[1] != "a/b.js" {
		t.Fatalf("files in %q, want path order", order)
	}
	names := d.Names()
	if !names["img/flusso.svg"] || !names["demo.js"] || names["round.md"] {
		t.Fatalf("names %v", names)
	}
	total := 0
	for _, body := range files {
		total += len(body)
	}
	if d.Bytes != total {
		t.Fatalf("bytes %d, want %d", d.Bytes, total)
	}
}

func TestLoadRefusesWhatTheBoundsExclude(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  []string
	}{
		{"too many files", func(t *testing.T, dir string) {
			files := map[string]string{"round.md": "# Capire"}
			for i := range maxFiles {
				files[fmt.Sprintf("%02d.js", i)] = ""
			}
			write(t, dir, files)
		}, []string{"round: more than 32 files"}},
		{"too many bytes", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"round.md": "# Capire", "grande.png": strings.Repeat("x", MaxBytes-len("# Capire")+1)})
		}, []string{fmt.Sprintf("round: %d bytes exceed the %d byte bound", MaxBytes+1, MaxBytes)}},
		{"symlink", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"round.md": "# Capire"})
			if err := os.Symlink("/etc/hosts", filepath.Join(dir, "hosts.css")); err != nil {
				t.Fatal(err)
			}
		}, []string{"round: hosts.css: symlinks are not allowed"}},
		{"symlinked directory", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"round.md": "# Capire"})
			if err := os.Symlink(t.TempDir(), filepath.Join(dir, "fuori")); err != nil {
				t.Fatal(err)
			}
		}, []string{"round: fuori: symlinks are not allowed"}},
		{"reserved path", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"round.md": "# Capire", ".lavagna/frame.js": "", ".nascosto.js": ""})
		}, []string{"round: .lavagna: the path is reserved for lavagna"}},
		{"unsupported file", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"round.md": "# Capire", "note.txt": "", "altro/round.md": ""})
		}, []string{
			"round: altro/round.md: unsupported file (round.md plus .js, .css, .png, .jpg, .jpeg, .gif, .webp or .svg)",
			"round: note.txt: unsupported file (round.md plus .js, .css, .png, .jpg, .jpeg, .gif, .webp or .svg)",
		}},
		{"no round.md", func(t *testing.T, dir string) {
			write(t, dir, map[string]string{"demo.js": ""})
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(t, dir)
			want := c.want
			if want == nil {
				want = []string{"round: no round.md in " + dir}
			}
			if _, errs := Load(dir); !reflect.DeepEqual(errs, want) {
				t.Fatalf("errors\n%q\nwant\n%q", errs, want)
			}
		})
	}
}

func TestRepositoryReadsExcerptsUnderTheGitRoot(t *testing.T) {
	top := t.TempDir()
	write(t, top, map[string]string{".git/HEAD": "", "src/main.go": "uno\ndue\ntre\nquattro\n", "src/bin.dat": "a\n\xff\n"})
	outside := t.TempDir()
	write(t, outside, map[string]string{"segreto.go": "chiave\n"})
	if err := os.Symlink(filepath.Join(outside, "segreto.go"), filepath.Join(top, "src", "link.go")); err != nil {
		t.Fatal(err)
	}
	excerpt := Repository(filepath.Join(top, "src"))
	if got, err := excerpt("src/main.go", 2, 3); err != nil || got != "due\ntre" {
		t.Fatalf("excerpt %q, %v", got, err)
	}
	for _, c := range []struct {
		path     string
		from, to int
		want     string
	}{
		{"src/main.go", 3, 9, "src/main.go has 4 lines"},
		{"src/link.go", 1, 1, "cannot read src/link.go under the git root"},
		{"src/bin.dat", 1, 2, "line 2 is not UTF-8 text"},
	} {
		if _, err := excerpt(c.path, c.from, c.to); err == nil || err.Error() != c.want {
			t.Errorf("%s:%d-%d: error %v, want %q", c.path, c.from, c.to, err, c.want)
		}
	}
	write(t, top, map[string]string{"src/riga.go": strings.Repeat("x", maxExcerptBytes) + "\n", "src/righe.go": strings.Repeat(strings.Repeat("x", 1023)+"\n", 65)})
	if _, err := excerpt("src/riga.go", 1, 1); err == nil || err.Error() != "a line exceeds 65536 bytes" {
		t.Errorf("an oversized line: %v", err)
	}
	if _, err := excerpt("src/righe.go", 1, 64); err != nil {
		t.Errorf("64 KiB of lines: %v", err)
	}
	if _, err := excerpt("src/righe.go", 1, 65); err == nil || err.Error() != "more than 65536 bytes" {
		t.Errorf("65 KiB of lines: %v", err)
	}
	if err := syscall.Mkfifo(filepath.Join(top, "src", "pipe.go"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := excerpt("src/pipe.go", 1, 1); err == nil || err.Error() != "cannot read src/pipe.go under the git root" {
		t.Errorf("a FIFO: %v", err)
	}
	if _, err := Repository(outside)("segreto.go", 1, 1); err == nil || err.Error() != "the working directory is not inside a git repository" {
		t.Errorf("outside a repository: %v", err)
	}
}
