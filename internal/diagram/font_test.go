package diagram

import (
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"testing"

	"github.com/taekwondodev/lavagna/internal/page"
)

func testFont(t *testing.T) *Font {
	t.Helper()
	b, err := fs.ReadFile(page.Assets, page.Font)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ParseFont(b)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestWidthMatchesChrome compares every label the font covers with the widths
// Chrome measured for SVG text in #14.
func TestWidthMatchesChrome(t *testing.T) {
	f := testFont(t)
	b, err := os.ReadFile("testdata/chrome-widths.json")
	if err != nil {
		t.Fatal(err)
	}
	var chrome struct {
		Rows []struct {
			Label               string
			Size, Weight, Width float64
		}
	}
	if err := json.Unmarshal(b, &chrome); err != nil {
		t.Fatal(err)
	}
	covered := 0
	for _, row := range chrome.Rows {
		missing := false
		for _, r := range row.Label {
			if _, ok := f.cmap[r]; !ok {
				missing = true
			}
		}
		if missing {
			continue
		}
		covered++
		if got := f.Width(row.Label, row.Size, row.Weight); math.Abs(got-row.Width) > 0.016 {
			t.Errorf("%q at %v px, weight %v: %.4f, Chrome %.4f", row.Label, row.Size, row.Weight, got, row.Width)
		}
	}
	// 20 of the 25 labels are covered, at 3 sizes and 4 weights.
	if covered != 240 {
		t.Errorf("compared %d covered rows, want 240", covered)
	}
}

func TestMissingGlyphIsOnePointOneEm(t *testing.T) {
	f := testFont(t)
	if got := f.Width("→", 12, 400); math.Abs(got-13.2) > 1e-9 {
		t.Errorf("→ at 12 px: %v, want 13.2", got)
	}
	// Kerning stops at the missing glyph instead of pairing its neighbours.
	if got, want := f.Width("Va→Te", 13, 650), f.Width("Va", 13, 650)+14.3+f.Width("Te", 13, 650); math.Abs(got-want) > 1e-9 {
		t.Errorf("Va→Te: %v, want %v", got, want)
	}
}
