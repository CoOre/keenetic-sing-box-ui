package web

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

func Static() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}
