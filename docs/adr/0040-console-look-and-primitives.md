# ADR-0040: The console's look: own tokens and primitives, an operations-console style

- **Status**: accepted (2026-10)
- **Concerns**: `web/src/styles/tokens.css`, `web/src/styles/app.css`, `web/src/components/ui/`,
  `web/src/components/Layout.tsx`, `web/src/inventory/`, the object page, `/` and `/health`

## Context

The first UI (ADR 0018) was correct and accessible but plain: an object page was a list of
attributes, a server, a switch and a NAS looked the same, and nothing showed the health of the
physical estate at a glance. Operators compare it with operations consoles in the manner of
Temporal's UI. A design-system dependency would bring a component library, a build step for
styles and, often, web fonts, which ADR 0018 and the CSP (`font-src 'self'`, no inline style)
rule out. trica is also meant to be deployed by any organisation: the look and what it shows
cannot depend on one site's names.

## Decision

**Own tokens.** `tokens.css` declares every colour role as a `--trica-*` custom property, as
resolved hex literals, in three blocks: dark (the bare `:root`, the default: it holds without
JavaScript), light (`data-theme="light"`), and light again for the "system" choice when the
system prefers light. The palette is transposed from the Choregos console: Radix Colors steps
(slate, iris, green, amber, red, blue, plum; MIT, see NOTICE), a single iris accent, tinted round
pills for states. `tokens.test.ts` computes the WCAG contrast of every pair the stylesheet uses,
checks that the light and system blocks are identical, that `app.css` reads only declared tokens
and holds no literal colour, and that no font is loaded. Type is the system sans to read and the
system monospace for what is compared (identifiers, addresses, numbers, timestamps), per ADR 0018.

**Own primitives.** `components/ui/` holds plain accessible React components (pill, health pill,
card, stat, table wrapper, tabs with the ARIA keyboard pattern and the tab in the address,
breadcrumbs, empty state, alert, button link, icons) styled by classes in `app.css`. Icons are
inline SVG drawn in the repository on a 24-pixel grid, stroked with `currentColor`; no icon
library. No CSS framework, no new dependency.

**Shell.** A collapsible left sidebar (sections: monitor, inventory, review; the fold is kept in
`localStorage`) and a 48-pixel top bar with the breadcrumb trail, the search field, language,
theme and sign-out. A page sets its trail with `useCrumbs`; one that does not gets its section.
A navigation that only rewrites the page's own address (a tab, a filter) keeps the focus.

**What an object is, and how it is, comes from the schema only.** The kind (icon, family hue,
word) is read from the object's `type`, the device's `role` enum value, and for a custom type the
schema `layer`; the health from the `health` attribute (an absent object is absent). No name,
vendor or model is ever looked at. Attributes on the object page are grouped by their schema
description (identity, kind, unit), component tables take their columns from the type's attribute
order, and quantities are formatted from the schema's `unit` in the reader's locale. The overview
(`/`) and `/health` draw one tile per device from `GET /objects?type=device` and the `mounted_in`
relations, grouped by rack or by kind; a large group folds its healthy devices, never one that
needs attention.

**Topology.** On screen the graph reads the `--trica-graph-*` tokens and restyles when the theme
changes; exported pictures and PDFs keep the fixed white-paper palette (ADR 0024, ADR 0037).

## Consequences

- One stylesheet and one token file to change the look; the contrast test says whether a change
  still passes, in both themes.
- A role the UI does not know shows its own word and the generic device icon; a custom type gets
  its layer's icon. Adding an icon is a change in `web/src/inventory/kind.ts`.
- The overview reads at most 1,000 devices (the API's page limit) and says so when there are
  more; a larger estate needs a server-side summary, not built here.
- Racks are only recoloured: their drawing is unchanged (front images are another story).
