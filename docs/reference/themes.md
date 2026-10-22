# UI themes

A theme changes how the console looks (colours, type, corners, logo) without rebuilding it. It
is served before sign-in, so the sign-in page is themed too. Design and reasons:
[ADR 0054](../adr/0054-ui-themes.md).

Two themes are built in: `default` (the look as shipped) and `flat` (square corners, near-black
and white, a green signal). Without configuration, `default` is in force.

## Choosing one

In the server configuration:

```yaml
web:
  theme:
    builtin: flat          # or:
    # path: /etc/trica/theme   # a .zip file or a directory
    # reload: 1m               # how often a path is read again
```

Exactly one of `builtin` and `path`. An unknown key, both, none, an unknown built-in name or a
path that does not exist stops the start. A `path` that exists but holds a bad theme does not:
the built-in `default` serves until it loads. Afterwards a bad edit is logged and counted and
the previous theme stays. Chart: `theme.builtin`, or `theme.existingConfigMap` with an optional
`theme.archiveKey` ([chart README](../../charts/trica/README.md)).

On Kubernetes:

```sh
kubectl create configmap trica-theme --from-file=my-theme/      # the files of the theme as keys
# or
(cd my-theme && zip -r ../theme.zip .)
kubectl create configmap trica-theme --from-file=theme.zip      # then theme.archiveKey: theme.zip
```

A ConfigMap holds about 1 MiB: a logo and a font fit, several fonts do not (use an image or a
volume then). Kubernetes updates a mounted ConfigMap in place within a minute or two; the server
re-reads it, no restart.

## Checking one

```sh
trica theme check my-theme/        # a directory
trica theme check theme.zip
```

It runs the checks the server runs and lists every problem; exit status 1 if there is one.
Otherwise it prints the name, version, sha256 (the theme's identity) and the size of the
generated stylesheet.

## The archive

```
theme.json          required, at the root (or inside a single top-level directory)
logo.svg  logo-dark.svg
icon.svg
fonts/geist.woff2
theme.css           optional, free CSS
LICENSE  README.md  any text, never served
```

Allowed: `.css`, `.svg`, `.png`, `.jpg`/`.jpeg`, `.webp`, `.ico`, `.woff2`, `.txt`, `.md`,
`LICENSE*`. Anything else (a script, HTML) refuses the theme. Paths use letters, digits, `.`,
`_`, `-`, at most 3 levels deep.

| Limit | Value |
|---|---|
| Archive | 2 MiB |
| Entries | 64 |
| All files, decompressed | 4 MiB |
| One file | 1 MiB |
| `theme.json` | 64 KiB |
| One stylesheet | 128 KiB |

### theme.json

All keys are checked; an unknown one is refused.

| Key | Meaning |
|---|---|
| `schema` | `1` (required) |
| `name` | `a-z0-9-`, 40 characters at most, not `default` or `flat` (required) |
| `title`, `description` | Shown by the administration page (part 2). 64 and 280 characters at most |
| `version` | Any of `0-9A-Za-z.+-`, 32 characters at most (required) |
| `extends` | `default` or `flat`: start from that built-in. No other value, no chain |
| `colors` | `{ "<token>": { "light": "#rrggbb", "dark": "#rrggbb" } }`. Both modes together. Token = the CSS name without `--trica-` |
| `fonts.sans`, `fonts.mono`, `fonts.title` | A `font-family` list (letters, digits, spaces, commas, quotes, `-`, `_`) |
| `fonts.title_weight` | 100 to 900, step 100 |
| `fonts.faces` | At most 8: `{ "family", "file" (woff2), "weight", "style" }` |
| `radius.control`, `radius.surface` | A length (`4px`, `0.25rem`), 32px at most |
| `logo.light`, `logo.dark`, `logo.width` | An svg, png, webp or jpg; one file serves both modes if only one is given; width 10rem at most. With a logo, the drawn mark and its coloured ground are hidden |
| `signal_bar` | Width (8px at most) of the bar on the active sidebar entry, in the `signal` colour. Absent: none |
| `favicon` | svg, png or ico |
| `stylesheet` | A css file of the archive |

Colour tokens (each in `light` and `dark`): `surface raised sidebar surface-muted line
line-strong ink ink-muted link accent accent-hover accent-ink accent-soft accent-line focus
signal`; for each of `ok warn danger info declared neutral`: `-soft -line -ink -solid`; and the
topology canvas `graph-paper graph-ink graph-node graph-node-line graph-unresolved
graph-unresolved-line graph-declared graph-observed graph-deduced graph-util-normal
graph-util-warn graph-util-crit graph-down graph-util-unknown graph-health-ok graph-health-warn
graph-health-crit graph-group graph-group-line`; and the isometric map (ADR 0051) `iso-floor
iso-floor-line iso-label-bg iso-top iso-top-hi iso-south iso-east iso-neutral-top
iso-neutral-south iso-neutral-east iso-edge iso-neutral-edge iso-shadow`. The list is `web/src/styles/tokens.css`; the
Go test `TestDefaultsMatchTokensCSS` keeps the validator's copy in step.

A colour you do not restate stays the console's (or the base's, with `extends`). The result must
pass the contrast check in both modes (`internal/theme/contrast.json`: text 4.5:1, interface
parts 3:1); a failing pair is named in the refusal.

### The stylesheet

An escape hatch for what tokens do not cover. It is not a stable API: class names of the console
may change. Allowed: any CSS except `@import`, `@namespace`, `expression()`, `-moz-binding`,
`behavior`, `javascript:` and backslash escapes outside strings. Every `url(...)`, `image(...)`
and `image-set(...)` must name a file of the archive, relative to the stylesheet, of a served
type (no scheme, `/`, `?`, `#` or `..`). Only files the manifest or the stylesheet names are ever
served.

### An SVG

No `<script>`, `foreignObject`, `iframe`, `embed`, `object`, `set`, `animate`, event handler
attribute, DOCTYPE or entity; `href` only to `#id`; `url()` in a style only to `#id`.

## What is served

| Address | |
|---|---|
| `/theme/theme.css` | The generated stylesheet, linked last in the page. `no-cache`, ETag |
| `/theme/favicon` | The theme's icon, else the application's |
| `/theme/a/<sha256>/<file>` | A file the theme references, `immutable`, with `default-src 'none'; style-src 'unsafe-inline'; sandbox` |

All public. The page's own Content-Security-Policy does not change.

## Observing

Metrics `trica_ui_theme_info{name,version,sha256,source}` (1 for the theme in force) and
`trica_ui_theme_load_errors_total{source}`. Log lines carry `source=theme`.

## Not in this version (part 2)

A theme stored in the database and chosen by an administrator in the console, an admin API,
`url` + `sha256` sources, `locked`, notification between replicas, the chart's `url`, `sha256`
and `locked`.
