# ADR-0054: UI themes: validated archives, two built-in looks, served before sign-in

- **Status**: accepted (2026-10). Part 1 is implemented; what part 2 adds is marked **(part 2)**.
- **Concerns**: `internal/theme` (new), `internal/web` (`/theme/...`), `internal/server`
  (`web.theme`), `internal/wiring`, `cmd/trica` (`trica theme check`), `web/src/styles`,
  `charts/trica` (`theme.*`). Extends ADR 0040 (the tokens); amends ADR 0018 (the font rule:
  the page still loads no web font of its own, a theme may ship one).

## Context

A deployment wants the console to look like its organisation: a palette, a logo, a typeface,
square corners instead of rounded. Until now the look was the one of ADR 0040, compiled into
the bundle. Changing it meant forking the web build.

The constraints that shape the answer:

- the console has a strict Content-Security-Policy (no inline style, no remote origin, `font-src
  'self'`), and a theme is content from outside the project: it must not widen what the page may
  load or run;
- the sign-in page must be themed too, so a theme is served **before** any credential;
- a theme is deployed like the rest: a file, a ConfigMap, later a database row;
- a bad theme must never take the server down, and never make the console unreadable.

## Decision

1. **A theme is a validated archive.** A zip (or a directory with the same layout) holding
   `theme.json` and optional files: css, svg, png, jpg, webp, ico, woff2, txt, md, `LICENSE*`.
   The manifest sets *tokens*: colours (a light and a dark value each, `#rrggbb`), font stacks
   and weight, the two radii, a logo per mode and its width, the width of the signal bar, a
   favicon, and optionally a stylesheet. The tokens are the contract; the stylesheet is the
   escape hatch and not a stable API (class names change). The format is in
   [`docs/reference/themes.md`](../reference/themes.md).

2. **Identity is a hash.** The sha256 of the archive's bytes. For a directory, the sha256 of the
   lines `<path> NUL <hex sha256 of the content> LF`, sorted by path, over the files read
   (hidden entries skipped). A file of the theme is served at `/theme/a/<sha>/<path>`, so it is
   immutable by construction and a page loaded before a swap still finds its files.

3. **Strict validation, all reasons at once.** Limits: archive 2 MiB, 64 entries, 4 MiB
   decompressed, 1 MiB a file, `theme.json` 64 KiB, css 128 KiB; sizes are counted on the bytes
   read, never on a header. Paths: letters, digits, `. _ -`, at most 3 levels, no `..`, no
   absolute path, no backslash, no case-insensitive duplicate. Refused: symlinks, encrypted or
   unusual-method entries. One top-level directory is stripped, macOS junk ignored. Every type
   is checked by content (magic bytes; an SVG by a sanitiser: no script, event handler,
   `foreignObject`, animation, external reference, DOCTYPE or entity). The manifest is decoded
   with unknown keys refused (a typo in a token name must not silently do nothing).
   The stylesheet is scanned: comments stripped, backslash escapes outside strings refused,
   `@import`, `@namespace`, `expression()`, `-moz-binding`, `behavior`, `javascript:` refused,
   and every `url()`, `image()` and `image-set()` argument must name a file of the archive (no
   scheme, absolute path, query, fragment or `..`) and is rewritten to its served address.

4. **The palette must read.** The effective palette (the console's defaults, the built-in it
   extends, then the theme's colours) is checked in both modes against the pairs of
   `internal/theme/contrast.json` (56 foreground/background pairs, 4.5:1 for text, 3:1 for
   interface parts). The web test `tokens.test.ts` reads the same file, so the console's own
   palette and the validator cannot drift. A theme that fails is refused, with each failing pair
   named.

5. **Served before sign-in, from the same origin, with the page's CSP unchanged.**
   `/theme/theme.css` is generated (`no-cache`, ETag, 304), `/theme/favicon`, and
   `/theme/a/<sha>/<file>` for the files the manifest or the stylesheet references (nothing
   else is served; `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline';
   sandbox`, immutable). `/theme/...` never answers with the application shell. The handler
   injects `<link rel="stylesheet" href="/theme/theme.css">` just before `</head>` of the
   in-memory `index.html` (after Vite's own stylesheet) and points the icon link at
   `/theme/favicon` (which serves the application's own icon when the theme ships none). A page
   with no `</head>` is an error at start. Fonts and images are `'self'`, so no directive of the
   page's CSP changes.

6. **Specificity beats order.** The generated blocks repeat `:root` (`:root:root`,
   `:root:root[data-theme="light"]`, and the `prefers-color-scheme` block with
   `:root:root:not(...):not(...)`): one class more than the matching `tokens.css` selector, so a
   theme wins on weight and not on its position in the page. A Go test pins the three
   specificities against `tokens.css`; `tokens.css` states that its three selectors must keep
   their shape.

7. **Two built-in themes, in the binary.** `default` is the look of ADR 0040, unchanged; `flat`
   is square corners, a near-black and white palette and a green signal, with the palette values
   of the Diametral Design System (MIT, credited in NOTICE) mapped onto every trica colour
   token, and no logo, no name and no font file of that organisation. They are manifests read by
   the same code as an archive. Their names are reserved: an archive cannot be called `default`
   or `flat`. A theme may `extends` a built-in (one level, no chains): the built-in's colours,
   fonts and radii are the base, the theme's own are laid over them.

8. **Declarative source.** `web.theme` holds exactly one of `builtin` and `path` (a zip or a
   directory, read again every `reload`, 1m by default; a Kubernetes ConfigMap mount works,
   its `..data` links are followed inside the directory, an escape is refused). Unknown keys,
   two sources and an unknown built-in name refuse the start. The chart's `theme.builtin` /
   `theme.existingConfigMap` / `theme.archiveKey` write it. Precedence: the configured source,
   else the built-in `default`. **(part 2)** a theme stored in PostgreSQL and chosen by an
   administrator, an admin API and page, `url` + `sha256` sources, `locked`, `NOTIFY` between
   replicas; its precedence over the configuration is part 2's to define.

9. **A bad theme never stops the server.** At start, a path that does not load leaves the
   built-in `default` in force; later, a refused read keeps the previous theme. The reason is
   logged (JSON, `source=theme`) and counted. Metrics:
   `trica_ui_theme_info{name,version,sha256,source}` (1 for the theme in force) and
   `trica_ui_theme_load_errors_total{source}`.

10. **Checked on a workstation.** `trica theme check <zip|dir>` runs the load-time checks and
    prints every violation, with the name, version and sha256 when it passes.

Three non-colour tokens support the manifest and are declared in `tokens.css` with the values
that reproduce today's look: `--trica-font-title` and `--trica-font-title-weight` (titles take
the sans stack at 600), `--trica-logo*` and `--trica-brand-*` (the drawn brand mark, replaced
by a logo file when a theme gives one), and `--trica-signal` / `--trica-signal-width` (a bar on
the active sidebar entry, drawn at width 0 by default, so `default` shows no change).

## Alternatives rejected

- **Free CSS in the configuration, or an uploaded stylesheet.** No contrast guarantee, no
  validation surface, and a stylesheet is where `@import` and `url(http://...)` come from.
- **A theme editor in the console.** A second product; a manifest in a repository is reviewable
  and versioned, which an operator's editor is not. Part 2's admin page selects and uploads, it
  does not design.
- **Loading the theme from the API after sign-in.** The sign-in page would be unthemed, and the
  page would flash.
- **Inlining the theme in `index.html`.** Needs `style-src 'unsafe-inline'` on the page.
- **Chained `extends`.** Cycles to detect for no use; one built-in base is enough.
- **A signed theme.** The source is already trusted configuration; the validation exists
  because a theme is easy to get wrong, not because the operator is an adversary. A `sha256`
  pin on a remote source is part 2.
- **Always drawing the active-entry bar.** Changes the default look for everybody; a token at
  width 0 keeps it unchanged.

## Consequences

- A deployment gets its look from a ConfigMap, with no rebuild; a theme error is a log line and
  a counter, not an outage.
- The console's CSS must read colours, radii, fonts and the brand mark through tokens only. The
  Vitest tests that read `app.css` already enforce it for colours.
- `contrast.json` is the single list of pairs the stylesheet relies on. A new pair in
  `app.css` is added there, and then every theme is checked against it too.
- A theme's stylesheet is arbitrary CSS inside the allowed profile: it may hide parts of the
  console. It cannot load anything from another origin or run script.
- The app's own `font-src 'self'` still holds; a theme's woff2 is served from `/theme/a/...`,
  same origin.

## What this does not decide

Part 2 above. Localising a theme's texts. A theme that changes layout or adds components.
