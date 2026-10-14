package web

import "io/fs"

// EmbeddedForTest exposes the embedded directory to the tests of this package.
func EmbeddedForTest() (fs.FS, error) { return embedded() }
