package page

import (
	"embed"
	"io/fs"
	"path"
)

//go:embed shell.html
var Shell []byte

//go:embed sw.js
var Worker []byte

//go:embed assets
var assets embed.FS

var Assets = mustSub(assets, "assets")

const Font = "fonts/AtkinsonHyperlegibleNext.ttf"

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
