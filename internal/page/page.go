package page

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"path"
	"strings"
)

//go:embed shell.html
var shell []byte

//go:embed sw.js
var Worker []byte

//go:embed assets
var assets embed.FS

var Assets = mustSub(assets, "assets")

// Build identifies the embedded page. The server sends it in the view and the
// shell carries it, so a tab built differently can reload.
var Build = build()

// Shell is the shell document, stamped with Build.
var Shell = bytes.Replace(shell, []byte("{{build}}"), []byte(Build), 1)

func build() string {
	h := sha256.New()
	h.Write(shell)
	h.Write(Worker)
	err := fs.WalkDir(assets, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, name)
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(b))
		h.Write(b)
		return err
	})
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

const (
	Font       = "fonts/AtkinsonHyperlegibleNext.ttf"
	FrameAsset = ".lavagna/"
)

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func ContentType(name string) string {
	switch path.Ext(name) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".ttf":
		return "font/ttf"
	default:
		return "text/plain; charset=utf-8"
	}
}

func Frame(content string, files []string) []byte {
	var styles, scripts strings.Builder
	for _, name := range files {
		ref := html.EscapeString("./" + (&url.URL{Path: name}).EscapedPath())
		switch strings.ToLower(path.Ext(name)) {
		case ".css":
			fmt.Fprintf(&styles, `<link rel="stylesheet" href="%s">`+"\n", ref)
		case ".js":
			fmt.Fprintf(&scripts, `<script src="%s"></script>`+"\n", ref)
		}
	}
	return fmt.Appendf(nil, `<!doctype html>
<html lang="it" class="frame">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<link rel="stylesheet" href="%[1]slavagna.css">
%[2]s<script src="%[1]sframe.js"></script>
</head>
<body>
<main class="document">
%[3]s
</main>
%[4]s</body>
</html>
`, FrameAsset, styles.String(), content, scripts.String())
}
