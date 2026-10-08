package round

import (
	"reflect"
	"testing"
)

func TestHighlightColoursTokensPerLine(t *testing.T) {
	for _, c := range []struct {
		name, file, text string
		want             []string
	}{
		{"go", "store/store.go", "if err := os.WriteFile(p, b, 0o644); err != nil { // tronca\n\treturn \"a<b\\\"\" }",
			[]string{
				`<span class="hl-k">if</span> err := os.<span class="hl-f">WriteFile</span>(p, b, <span class="hl-n">0o644</span>); err != <span class="hl-l">nil</span> { <span class="hl-c">// tronca</span>`,
				"\t" + `<span class="hl-k">return</span> <span class="hl-s">&#34;a&lt;b\&#34;&#34;</span> }`,
			}},
		{"block comment and raw string across lines", "a.go", "/* uno\ndue */ x := `tre\nquattro`",
			[]string{
				`<span class="hl-c">/* uno</span>`,
				`<span class="hl-c">due */</span> x := <span class="hl-s">` + "`tre</span>",
				`<span class="hl-s">quattro` + "`</span>",
			}},
		{"python triple quotes and hash comments", "x.py", "def f():\n    \"\"\"doc\n    \"\"\" # nota",
			[]string{
				`<span class="hl-k">def</span> <span class="hl-f">f</span>():`,
				`    <span class="hl-s">&#34;&#34;&#34;doc</span>`,
				`<span class="hl-s">    &#34;&#34;&#34;</span> <span class="hl-c"># nota</span>`,
			}},
		{"unterminated string stops at the line end", "a.js", "const s = 'a\nlet",
			[]string{`<span class="hl-k">const</span> s = <span class="hl-s">&#39;a</span>`, `<span class="hl-k">let</span>`}},
		{"base name without extension", "Makefile", "install: # x",
			[]string{`install: <span class="hl-c"># x</span>`}},
		{"sql keywords in upper case", "q.sql", "SELECT 1 -- x",
			[]string{`<span class="hl-k">SELECT</span> <span class="hl-n">1</span> <span class="hl-c">-- x</span>`}},
		{"numbers keep multibyte runes whole", "a.go", "x := 3é + 1.5",
			[]string{`x := <span class="hl-n">3é</span> + <span class="hl-n">1.5</span>`}},
		{"unknown language is only escaped", "notes.txt", "if <x> // y\n",
			[]string{"if &lt;x&gt; // y", ""}},
	} {
		if got := highlight(c.file, c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}
