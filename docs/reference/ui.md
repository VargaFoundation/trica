# Web UI

A single-page application in `web/` (React 19, TypeScript strict, Vite, react-router 7, TanStack
Query 5), served by `internal/web`. Structural choices: ADR 0018, ADR 0019, ADR 0024 and ADR 0040 (the look). This page is what a user, a
developer and an operator need; when it and the code disagree, the code wins.

## What it shows

| Address | Screen |
|---|---|
| `/` | The physical estate at a glance: one tile per device (kind icon, health, power draw, inlet and outlet temperatures, critical log events), grouped by rack (top of the rack first) or by kind, with counts per health and the total power; a large group folds its healthy devices. Then search examples and the other sections |
| `/search?q=<query>` | Results grouped by type, largest group first, with the count per type. A group that is only partly shown links to `/search?q=<query>&type=<type>` (the whole type, paged, `offset=`) |
| `/objects/<id>` | A header with the object's kind (icon and word from `type` and the device `role`), health, status and key facts (vendor, model, serial, addresses, uptime, power, rack and U), then tabs kept in the address (`?tab=`): **Overview** (attributes grouped by what the schema says they are, units formatted), **Components** (what sits inside, `parent:"<id>"`, one table per type with its health, worst first), **Relations**, **History**, **Provenance**. Provenance is the table of **attributes with their provenance** (source, confidence, first and last seen; other sources' claims behind a disclosure; a value with no provenance says so), relations and dependencies seen from this object (names and inverse names from the schema), timeline of changes in words (older changes on demand) |
| `/discrepancies?status=&kind=&offset=` | Declared against observed; open by default |
| `/proposals?status=&offset=` | Proposals waiting for a decision (pending by default); `/proposals/<id>` shows the preview of what it would change |
| `/health` | The equipment tiles grouped by kind, then the last run of each source, failing sources first |
| `/rooms`, `/rooms/<id>` | Every room; the floor plan of one room (SVG, racks on a grid of the room's tiles, 600 mm unless the room sets `tile_mm`; north is up, the bar on a rack is its front) with a table of its racks. Racks stand in the room directly or through a row |
| `/topology?site=&rack=&vlan=&classes=` | The layer-2 links: a layered graph (Cytoscape + ELK) of the devices that have a link, a legend, filters, and the same links as a table. See "Topology" below |
| `/racks/<id>` | Front and rear elevations of one rack (SVG, U 1 at the bottom) with free units, a table of what is mounted, and the devices mounted without a position. Opened from the floor plan, from a rack's object page, or from the room page |

Every address is a deep link: filters, the open type and the page are in the URL, and the
application answers any of them (the server returns the shell for client routes). The search field
reads its query from the address on `/search`.

The query is the language of `docs/reference/tql.md`: free text (exact first, typo-tolerant when
nothing matches) or `type:device vendor:arista os_version<4.20`.

### Racks and rooms: what the drawing relies on

From the schema as it is (`internal/schema/builtin.yaml`): the `mounted_in` relation carries
`position` (U of the lowest unit, 1 = bottom) and `face` (`front`, `rear`); the device's model
(`device_type`, through `instance_of`) carries `u_height` and `full_depth`; the rack carries
`u_height`, `floor_x`, `floor_y` (tiles east and south of the room's north-west corner) and
`facing`; the room carries `width_mm`, `depth_mm` and `tile_mm` (the side of a tile, 600 when not
set; ADR 0030).

- A **full-depth** device is drawn on its face and as a dashed shadow on the other; it blocks both.
- What the data does not say is shown, never hidden: no rack height means 42 U assumed (with a
  note), no device height means 1 U, no face means front, no position lists the device apart.
- A device that overlaps another on the same face, or sticks out of the rack, has a red outline, a
  `!` and a note in the table; two racks on one tile, or racks outside the room's size, are flagged.
- **Faces** (ADR 0042). A device shows its model's image of that face when the device-type
  library (or the operator's `devicetypes.extra_dir`) has one, stretched to its U box; else a
  generic face drawn for its `role`: drive bays for a server, rows of ports for a switch or a
  router, a few ports and a red band for a firewall, drive carriers for storage and NAS, outlets
  for a PDU, a display and a grille for a UPS, keystone jacks for a patch panel, a blank plate with
  grilles when the role is unknown. The other face of a full-depth device is its rear image,
  faded, when there is one, else the plain dashed block.
- **Health**: a strip on the left edge of each device, green ok, amber warning, red critical,
  grey unknown, with an amber or red outline for the last two. It is the worst of the device's
  `health` and of its parts' (`parts_health`: power supplies, fans, disks, modules the reader may
  read). Colour is never the only sign: the tooltip says name, range, model and health, the
  link's accessible name ends with the health when it is known, and the table has a Health column.
- Images are fetched with the session's token (`GET /api/v1/device-types/{slug}/{face}`, only
  that path is ever fetched with it), decoded with `createImageBitmap` and painted on a canvas
  inside the SVG: no `blob:` or `data:` URL, so `img-src 'self'` stands. A browser without
  `createImageBitmap`, or an image that does not load, gets the generic face.
- The elevation reads the rack's contents in one call (`GET /racks/{id}/contents`), whatever the
  number of devices.
- Known limits: at most 1000 devices per rack and 1000 racks per room are read (the page says when
  it is cut).

### Topology: what the graph relies on

The schema has no `connected_to` or `link` relation; the page reads what it has (code wins):

| Relation | Becomes | Class |
|---|---|---|
| `cabled_to` (interface to interface) | a link | the relation's own confidence |
| `connects` (cable to interface, `end` a or b) | one link per cable that has exactly two interface ends | the weakest of its two ends |
| `neighbor_of` (LLDP adjacency) | a link | the relation's own confidence |
| `mounted_in`, `member_of` (interface to VLAN) | not drawn: they feed the rack and VLAN filters | |

- **The canvas follows the theme** (the `--trica-graph-*` tokens); the exports stay on white
  paper with a fixed palette.
- **Three classes, never told apart by colour alone.** Declared: solid, thick, dark. Observed:
  dotted, blue. Deduced: dashed, amber, and its label says the level ("deduced, high"). The legend
  and the table say the same in words. An unknown confidence counts as deduced.
- **Deduced cables.** A deducer writes `cabled_to` with a `level` (`high`, `medium`, `low`) and an
  `evidence` (`lldp`, `lldp_one_sided`, `mac_table`) attribute (`internal/deduce`, ADR 0010). They
  are shown when present; without them the link is "deduced" with no level.
- **Nodes are devices.** A port on a chassis module belongs to the module's device. A link between
  two ports of one device is not drawn (counted in a note, kept in the table). A port whose device
  is not listed is a dashed grey node named by the start of its id. Several links between the same
  two devices of the same class (and level) are one line, labelled with their count.
- **Filters** (address: `site`, `rack`, `vlan`, `classes`): a link is kept if at least one end
  matches; the last class cannot be unticked; a value in the address that matches nothing is
  ignored.
- **Keyboard**: the canvas takes no focus; the table below it lists every link with links to both
  devices, both ports and the cable. Clicking a node opens its object page.
- **Size**: links are read 1000 per call, at most 10 000 per kind; the page says when it stopped.
  The ELK layout runs in the page: about 1.5 s for 200 devices and 520 links in Chromium.
- The page and its two libraries are a separate file (1.9 MB, 590 KB compressed), fetched when the
  page is opened.

### Exports

`Export PNG` and `Export PDF` on the rack elevation and on the topology. Built in the page, nothing
is sent anywhere, no change to the content security policy (the file is a `blob:` link made by
the page's own script).

- The rack sheet is drawn on a canvas, on white, with a fixed palette whatever the theme: both
  faces side by side, the title, and "Exported from trica on <date>". It carries the same model
  images (the bitmaps the page already fetched, so same origin and nothing new under the CSP),
  generic faces and health strips as the screen. The topology is Cytoscape's
  own image of the whole graph, on white, up to 16 million pixels (the ratio drops for a very large
  graph). The size does not depend on the screen: a Retina display gets the same file as any other
  (ADR 0038).
- The PDF is one page at the figure's size (1 CSS px = 0.75 pt, at most 200 inches a side).
  - **Topology: drawn as lines, boxes and text** (ADR 0037). The names and labels can be selected
    and searched, and the drawing stays sharp when zoomed. The font is Helvetica, which every PDF
    viewer has; characters outside Latin-1 are printed `?`. The boxes are sized for that font, so the
    page is close to the screen but not identical.
  - **Rack: a picture**, the same sheet as the PNG in a JPEG: its text cannot be selected.
- Files are named `trica-rack-<name>.png` and `trica-topology-<date>.png`; a failure is said in
  words next to the buttons.

## Declaring the physical layout

What collectors cannot see is entered on the pages that show it (ADR 0039). The controls appear
for a caller who holds `propose` (`GET /me`); the API checks every write again.

| Where | What |
|---|---|
| `/rooms` | **Add a site**, **Add a room** (in a site or building chosen from the schema's parents) |
| `/rooms/<id>` | **Add a rack** in the room, on a floor tile (click a free tile: its X and Y are filled in); **Edit this room** (name, size, tile) |
| `/racks/<id>` | **Place a device** (click a free unit: its U and face are filled in): a device the inventory has (search by name), or one no collector sees (a PDU, a UPS, a patch panel, a shelf: name or serial or asset tag, role, vendor, model, height); **Edit this rack** (name, height, floor tile, facing) |
| `/objects/<id>` of a device | **Rack placement**: where it is mounted and who said so; **Place in a rack** / **Move**, **Remove from this rack** |
| `/objects/<id>` of a site, building, room, row or rack | **Edit**: its attributes from the schema; emptying a field withdraws your own value only |

- **Every write is a batch** sent to `POST /batches`, never anything else: partial (it adds or
  withdraws your claims, it never withdraws by omission), attributed by the server to
  `user:<subject>` (or the token's source) at `declared` confidence.
- **Preview first.** "Preview the change" sends the batch to `POST /batches/preview` and shows the
  engine's answer in words: what it creates, the values and links it opens or closes, its warnings,
  and the page's own. "Submit" exists only under a preview of the form as it is; changing the form
  drops the preview. The answer is said: **applied** (200, the pages are read again), **sent for
  approval** (202, with a link to the proposal), or the refusal (400, 403, 409, 422) with the
  server's words.
- **Checked before asking**: the position (U 1 at the bottom; half units when the schema's
  `mounted_in.position` is a float) and the top unit inside the rack's height, a positive height, a
  whole floor tile. **Said, not blocked**: an overlap on the same face (a full-depth device counts
  on both), a tile already holding a rack, a tile outside the room, a device whose height nobody
  says (checked and drawn as 1 U), a "new" device the engine recognises as one it has.
- **Heights**: a device's height is its model's (`device_type.u_height`); when it has no known
  model, the form asks for it and stores it on the device (`device.u_height`).
- **Moving** places the device in the new rack and withdraws *your* placement in the racks it
  leaves. A placement another source said (a collector, a colleague) stays: the form says so
  before anything is sent.
- Fields, enum values, units and labels come from `GET /schema`; nothing is offered for an
  attribute the schema does not have.
- Keyboard: free units and free tiles are pointer shortcuts only; the **Place a device** and **Add
  a rack** buttons open the same forms. A form opens in the page, focus moves to its title, and
  the preview and the outcome take the focus when they arrive.

## Keyboard and accessibility

- `/` or Ctrl+K (Cmd+K on macOS) focuses the search field from anywhere. `/` is a character while
  typing in a field; Ctrl/Cmd+K works there too.
- A skip link is the first tab stop. On every navigation focus moves to the content and the page
  title changes, so a screen-reader user knows where they landed.
- Status is text first (`observed`, `deduced`, `declared`, `failing`); colour only reinforces it.
- Tables keep their semantics (captions, `scope`); below 40 rem each row becomes a card with the
  column title before each value.
- Light, dark and system themes (the selector sets `data-theme` on `<html>`; "system" follows
  `prefers-color-scheme`, and dark is the default when nothing says otherwise). Reduced motion is
  respected. The sidebar folds into a rail of icons; the names stay for a screen reader and as
  tooltips. Tabs follow the ARIA pattern (arrows, Home, End).
- Language: English or French, chosen by the selector, remembered in `localStorage`, otherwise
  from the browser. Object and attribute labels come from the schema (`label.fr`).
- Checked in the tests: axe-core (WCAG 2.0, 2.1, 2.2 A and AA rules and best practices) on every
  page and on the interactive states, and the contrast ratio of every colour pair of both themes
  computed from the tokens in `src/styles/tokens.css`. In a real browser (Playwright, below) axe
  also checks rendered contrast on every page, in English and French. **Not checked**: real screen
  readers, phone layout. Firefox and WebKit run the same browser tests on demand only (ADR 0038).

## Signing in and permissions

The API takes bearer credentials only. The UI asks for one (an API token `trica_<id>_<secret>` or
an OIDC access token), keeps it in memory and in `sessionStorage` (gone when the tab closes), and
sends it as `Authorization: Bearer`. A 401 drops it and returns to the form. Cookies are not
used (`credentials: "omit"`).

**Single sign-on** (ADR 0019) appears when the server is configured with an OIDC provider: the
browser is a public client doing the authorisation code flow with PKCE (S256), with no client
secret. The API still verifies the bearer token itself. The page learns the provider from
`GET /ui-config.json` (`{"oidc": {"issuer", "client_id", "scopes", "bearer"}}` or `{"oidc": null}`;
unauthenticated, `no-store`, nothing secret). The pasted-token form stays below the SSO button.

Register in the provider (Keycloak): a **public** client (client authentication off), standard flow
on, PKCE method `S256`, valid redirect URI **`https://<host>/oidc/callback`**, and the UI's origin
under *Web origins* (the page calls the discovery and token endpoints itself). `bearer` is
`access_token` (default) or `id_token`: the API checks the audience of whichever is sent, so with
Keycloak either add an audience mapper to the access token or choose `id_token`.

`GET /api/v1/me` returns the actions the caller holds (`read`, `propose`, `approve`, `admin`). The
UI shows **Approve** and **Reject** only to a caller who holds `approve`, only on pending
proposals, and asks for a confirmation. The decision is the API's: a 403 (a grant limited to a
type or site) or a 409 (someone decided first) is shown as the server wrote it. The declaring
controls (above) appear to a caller who holds `propose`. The UI never writes the inventory itself:
approving a proposal asks the engine to apply it, and a declaration is a batch the engine applies
or queues.

## The API client

`web/openapi.json` is a snapshot of `GET /api/v1/openapi.json`; `src/api/schema.gen.ts` is
generated from it by `openapi-typescript`. After changing an endpoint:

```
make web-api-update   # re-exports the snapshot from the Go API, regenerates the types
```

Three tests fail when the Go API, the snapshot, the generated types and the client disagree
(`TestWebOpenAPISnapshotIsCurrent`, `src/api/schema.test.ts`). Test fixtures
(`src/test/fixtures.ts`) are anonymised (192.0.2.0/24, 198.51.100.0/24, `sw-a1`) and typed by the
generated schemas; they are written by hand, not recorded from a server.

One set is recorded: `src/topology/testdata/deduced-api.json` holds what the real API returns
(`GET /relations` and `GET /objects`) over cables the real deducer wrote, ids renamed.
`TestDeducedCablesReachTheTopologyAsWritten` fails when it drifts, and
`src/topology/deduced.test.ts` runs the topology model on it. After a change to the deducer or to
those endpoints:

```
TRICA_UPDATE_TOPOLOGY=1 go test ./internal/api -run DeducedCablesReachTheTopology
```

## Build, test, run

| Command | What |
|---|---|
| `make web-install` | `npm ci` from the lockfile |
| `make web` | install, lint, typecheck, generated-types check, tests, build, Go tests on the build |
| `make web-lint`, `web-typecheck`, `web-test`, `web-build`, `web-audit` | One step each |
| `make web-e2e` | Playwright (Chromium) on every page against `cmd/trica-e2e-server`; needs `make web-build` and, once, `npm --prefix web run e2e:install` (the `e2e` CI job does both) |
| `make web-e2e-browsers` | The same specs in Chromium, Firefox and WebKit; needs `make web-build` and, once, `npm --prefix web run e2e:install-browsers` (the `browsers` job of the `e2e` workflow, on demand) |
| `npm --prefix web run dev` | Vite dev server; `/api` is proxied to `TRICA_API` (default `http://127.0.0.1:8080`) |

Node 20.19 or newer (CI and the image use 22.23.3). `make web-build` writes `internal/web/dist`.

## Serving it (`internal/web`)

`web.New(fsys)` / `web.Default()` return an `http.Handler` for the server to mount at `/`
(`trica server` does, from `internal/wiring`, with `web.oidc` of the server configuration; a
build without the `webui` tag serves a placeholder page and logs a warning at start);
`web.NewWithConfig` / `web.DefaultWithConfig(web.Config{OIDC: ...})`
also serve `GET /ui-config.json` and add the issuer's origin to `connect-src`. An invalid
configuration is an error at start-up.

| Request | Answer |
|---|---|
| `/`, `/index.html`, any extensionless path (`/objects/<id>`) | `index.html`, `Cache-Control: no-cache`, ETag |
| `/assets/<name>-<hash>.<ext>` | The file, `Cache-Control: public, max-age=31536000, immutable` |
| Another existing file (`/favicon.svg`) | The file, `no-cache`, ETag |
| `/api`, `/api/...`, a missing file with an extension, a missing `/assets/...` | 404, never the shell (a stale hashed asset must fail, not load HTML as a script) |
| `/ui-config.json` | The sign-in configuration, JSON, `no-store`, no authentication |
| Methods other than GET and HEAD | 405 |

Every response carries `Content-Security-Policy: default-src 'none'; script-src 'self'; style-src
'self'; img-src 'self'; font-src 'self'; connect-src 'self'; manifest-src 'self'; base-uri 'none';
form-action 'none'; frame-ancestors 'none'`, `X-Content-Type-Options: nosniff`, `X-Frame-Options:
DENY`, `Referrer-Policy: no-referrer`, a restrictive `Permissions-Policy` and same-origin
opener/resource policies (with SSO configured, `connect-src` also lists the issuer's origin). The application therefore uses no inline script or style, no web font
and no remote origin; the tests enforce it.

Two builds: without a tag the binary embeds `internal/web/placeholder/index.html` (a page that says
the UI is not built in; `web.Built` is false); `go build -tags webui` embeds the real build. The
Dockerfile builds the UI in a Node stage and compiles with the tag, so the image ships the UI on
distroless.

## Browser tests (`web/e2e`)

`make web-e2e` runs Playwright (Chromium; `make web-e2e-browsers` adds Firefox and WebKit, ADR 0038) against `cmd/trica-e2e-server`: the real `internal/web`
handler with the built UI, a mock of `/api/v1` over an invented inventory (it previews batches and
answers a submitted one "queued as proposal 12", applying nothing), and a fake
OpenID provider on one self-signed TLS origin (`https://127.0.0.1:18443`, `TRICA_E2E_PORT` changes
it). The specs cover every page in English (and a rack page in French) with `@axe-core/playwright`
(WCAG 2.0 to 2.2 A and AA, contrast included), the full SSO flow with the provider verifying PKCE,
forged and refused callbacks, the pasted-token fallback, the CSP and `/ui-config.json`, rack
drawing, and the render time of a 48 U rack with 96 devices (budget: the median of five renders
after a warm-up render, each from a reloaded room page, under one second). The
topology specs check every kind of link, the filters, the table as keyboard path, PNG and PDF
downloads of the rack and of the graph (signature, size, pixels inked, no content-security-policy
violation), axe in English and French, and the time to draw 200 devices (budget: under 8 s from the
menu click, of which layout under 6 s). `declare.spec.ts` places a declared PDU from a free unit
through preview to "sent for approval", refuses a position outside a rack before sending, and
adds a rack from a free floor tile, with axe on the open forms.

## Not in this version

Selectable text in the rack PDF (it is a picture; the topology's is vector), patch-panel front/rear ports in the topology, saved queries, history ("which device had this
address on 3 March").
