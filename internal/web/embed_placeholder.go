//go:build !webui

package web

import (
	"embed"
	"io/fs"
)

// Built reports whether the real web UI is embedded. It is false in the
// default build, which embeds a placeholder page so that `go build` and
// `make ci` need no Node. Build the UI with `make web-embed` and compile with
// `-tags webui` (the Dockerfile does both).
const Built = false

//go:embed placeholder
var assets embed.FS

func embedded() (fs.FS, error) { return fs.Sub(assets, "placeholder") }
