# ADR-0024: L2 topology view, a bulk relations endpoint, client-side PNG/PDF export

- **Status**: accepted (2026-10); the topology PDF is vector since ADR 0037
- **Concerns**: `web/`, `internal/api` (one endpoint), `cmd/trica-e2e-server`. Builds on ADR 0018 and 0019.

## Context

The topology page needs every link of the inventory at once. The API only returned relations one
object at a time (`GET /objects/{id}/relations`): 500 links between 200 devices would cost hundreds
of requests, and the rack page already shows that cost (finding #42). The schema has no
`connected_to` or `link` relation, and the cable deducer (`feat/deduce-cabling`) is not merged. The
page must also export figures without loosening the content security policy.

## Decision

**One bulk read: `GET /api/v1/relations?type=a,b`.** It returns the *current* relations
(`upper_inf(valid)`) of the given types, each with both ends described (`id`, `type`, `display`,
`parent_id`, `site_id`), as a normal page (`limit`, `offset`, ordered by id). `type` is required, must
be registered, at most 8: there is no "everything" query. A relation is listed only if the caller can
read *both* ends (type and site, as for objects); attributes go through the same secret redaction as
every other answer. It scans at most 5,000 candidates and says `truncated` past that, like the
other post-filtered lists. It reads only, `read` action. The OpenAPI snapshot and the generated
types are updated; the SDK and the reconcile engine are untouched.

**The page maps the schema it has** (the code wins over any plan): `cabled_to`, `connects` (cable to
two interfaces) and `neighbor_of` are the links; `mounted_in` and `member_of` feed the rack and VLAN
filters. A link's class is its confidence (`declared`, `observed`, `deduced`; unknown counts as
deduced); a cable's class is the weakest of its two ends, so a documented cable half-guessed is not
shown as documented. A cable without exactly two interface ends, and a link that does not join two
ports, is counted in a note, not drawn. The deducer is expected to write `level` and `evidence` on
`cabled_to`; the page shows them when present and is correct without them. That dependency is
named in the PR: until the deducer is merged the page has never seen real deduced data.

**Distinct without colour.** Declared is solid and thick, observed dotted, deduced dashed with the
level in its label; legend and table say it in words.

**Cytoscape 3.34.3 and cytoscape-elk 2.3.0, pinned**, with ELK (elkjs 0.9.3) for the layered layout.
Cytoscape is MIT, cytoscape-elk MIT, elkjs EPL-2.0 OR GPL-3.0-or-later: we use it under the EPL-2.0,
unmodified. ELK runs in the page (no worker, no script from elsewhere), which `script-src 'self'`
allows; at 200 devices it takes about 1.5 s. The whole feature is one lazily loaded file, so no
other page pays for its 1.9 MB. If ELK fails the graph falls back to a grid and says so.

**Exports are built in the page and change no policy.** The rack sheet is drawn on a canvas by our
own code (the SVG on screen cannot be rasterised under `img-src 'self'`: a `data:` or `blob:` image
of it would be refused), the topology by Cytoscape's image of the whole graph. Both are white, with
a fixed palette. The file is saved through a `blob:` link created by the page's own script; the e2e
specs download it under the real CSP and fail on any violation. Cytoscape adds an inline `<style>`
(one rule, `position: relative`) that the policy refuses; the page claims the id it checks for and
carries the rule in `app.css`, instead of allowing inline styles.

**The PDF is a picture.** One page, the image as a JPEG, written by a small dependency-free writer
(`web/src/export/pdf.ts`). Its text cannot be selected. A PDF library with vector output and fonts
would be a larger dependency and a font-licensing question for a feature whose purpose is "put it
in a document"; the PNG is the primary export.

## Consequences

- Topology works against any server that has `GET /relations`; the mock server grew the endpoint, a
  second site, VLANs and a 200-device fabric for the tests.
- A 5,000-candidate scan cap and a 10,000-row page cap bound what the page shows; both are said on
  the page.
- Chassis module ports are resolved through the module list, and patch-panel front/rear ports are not
  followed: a cable through a patch panel shows as two links. Neither is a schema change here.
- Deduced `level` and `evidence` are read by convention until the deducer lands.
