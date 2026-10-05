package witnesstest

import (
	"bytes"
	"embed"
	"slices"
	"testing"
)

//go:embed testdata/*.jsonl
var samples embed.FS

const Placeholder = "s-0123456789abcdef01234567"

func Sample(t testing.TB, name string) (before, after [][]byte) {
	t.Helper()
	b, err := samples.ReadFile("testdata/" + name + ".jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.SplitAfter(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	lines[len(lines)-1] = append(lines[len(lines)-1], '\n')
	offset := slices.IndexFunc(lines, func(l []byte) bool { return bytes.Contains(l, []byte(`"customType":"lavagna-witness-sample"`)) })
	if offset < 0 {
		t.Fatalf("%s has no offset entry", name)
	}
	return lines[:offset+1], lines[offset+1:]
}
