// Package theme reads, validates and renders UI themes (ADR 0054).
//
// A theme is a zip archive (or a directory with the same layout) holding a
// manifest, theme.json, and optionally a stylesheet, logos, a favicon and web
// fonts. Reading one is strict: every limit, file type and reference is
// checked, the colours are checked for contrast, and what comes out is a
// Theme that holds only validated bytes. The server serves a Theme's generated
// stylesheet and the files it references from /theme (internal/web); nothing
// else of an archive is ever served.
//
// The package has no dependency on the rest of trica, so that `trica theme
// check` and the server run the very same validation.
package theme
