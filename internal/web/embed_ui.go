//go:build webui

package web

import (
	"embed"
	"io/fs"
)

// Built reports whether the real web UI is embedded: true under -tags webui.
const Built = true

// dist is the output of `make web` (vite build, outDir ../internal/web/dist).
// It is not committed: compiling with -tags webui before building it is an
// error, on purpose.
//
//go:embed all:dist
var assets embed.FS

func embedded() (fs.FS, error) { return fs.Sub(assets, "dist") }
