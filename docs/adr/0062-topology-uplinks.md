# ADR-0062: `uplinked_to` on the topology: a device-level line, drawn only where no port-level link joins the two devices

- **Status**: accepted (2026-10)
- **Concerns**: `web/src/topology/` (`model.ts`, `graph.ts`, `live.ts`, `TopologyPage.tsx`,
  `LivePanel.tsx`), `web/src/i18n/`, `web/src/styles/app.css`, `cmd/trica-e2e-server/api.go`,
  `docs/reference/ui.md`. Amends ADR 0024 (what the page maps), ADR 0037 (the PDF has an arrowhead
  where an uplink points) and ADR 0041 (an uplink is not live). Closes the "not drawn" consequence
  of ADR 0060. No Go production code, no schema change, no migration. Closes #172.

## Context

The UniFi collector (`api: network`, ADR 0060) writes `uplinked_to`: a device to the device it
reaches the network through, as a controller says it, with **no port** on either end. An inventory
fed only by UniFi has no cable at all, so the topology page said "No link between ports is recorded
yet." although the inventory knew who reaches the network through whom.

The page is built in the browser from the generic `GET /api/v1/relations?type=…` (ADR 0024: at most
8 types, each registered, a relation listed only when the caller can read both ends). There is no
server-side graph. `uplinked_to` is registered, so the endpoint already answers for it: the work is
in the page.

## Decision

1. **Read.** `uplinked_to` joins `LINK_TYPES` (four of the eight allowed), in the same bulk call as
   the cables. The UI is embedded in the server's binary, so the type is always registered; a
   development UI pointed at a server older than ADR 0060 would get a 400.
2. **A row with no ports.** `LinkKind` gains `uplinked_to`. In such a row `a` is the downstream
   device (`from`) and `b` the uplink device (`to`); a port's `name` is empty and its `id` is the
   device's own, never its `parent_id` (the existing device-of-a-port rule would map a device to its
   parent). An end that is not a device is counted in `skipped.ends`. The site of a device comes from
   its end's `site_id`.
3. **One line between two devices.** Port-level rows (`cable`, `cabled_to`, `neighbor_of`) are
   placed first. An uplink between two devices a **visible** port-level line already joins is not
   drawn: it is attached to that line (`Edge.uplinks`, `Graph.covered`) and shown in its details and
   in the table. Otherwise it is a line of its own, oriented from the downstream device to the
   uplink. This goes beyond the issue, which names only `cabled_to`: a `neighbor_of` line covers an
   uplink too, otherwise the same two devices would still get two lines. It is decided on the
   *filtered* rows on purpose: when the reader hides deduced links and the only cable between two
   devices is deduced, the observed uplink appears instead of leaving the pair unlinked. The table
   always lists every row; the "Show" button of a covered uplink opens the covering line.
4. **Told apart without colour.** The colour stays the confidence's. The line is dash-dot
   (`10 3 2 3`), 2 px wide, ends in a triangle on the uplink device, and its label says "uplink (port
   unknown)" (then the deduced level when there is one). The legend has an entry only while an
   uplink line is drawn. The table says "port unknown" in place of each port and "uplink (port
   unknown)" as the kind.
5. **Live (amends ADR 0041).** There is no port to measure, so an uplink line gets no live style:
   the "not measured" grey would erase its class colour. `rowLive` returns nothing for an uplink
   whatever the answer is keyed by. Its details say "An uplink names no port: there is nothing to
   measure."
6. **Filters.** Site and rack keep an uplink when one end matches, as for any link. A VLAN is a
   property of ports, so the VLAN filter drops uplinks. The class boxes apply as usual.
7. **Exports.** PNG and JPEG are Cytoscape's own image and carry the style. The vector PDF (amends
   ADR 0037, "no arrowheads") draws the dash-dot pattern, the label, and an arrowhead of two short
   strokes ending on the border of the uplink's box, after the boxes. There is no SVG or GraphML
   export; the inventory export already carries `uplinked_to` as a relation.

## Consequences

- An inventory with only UniFi data shows a topology. A pair of devices has one line whatever the
  number of sources that say how they are connected.
- A server-side map, if one lands (the isometric map of #150), reads its own relation types: it does
  not draw `uplinked_to` until it is taught to; that is a follow-up, not part of this page.
- The class filter has no separate "uplinks" toggle: an uplink is hidden with its class, or with a
  VLAN filter.
- Two uplinks in opposite directions between the same two devices are two lines.
- Port-level knowledge (which port, which cable) still needs another source (#171); once a cable
  exists between the two devices, the uplink goes under it without any change to the data.
- Tests: the API contract and a snapshot shared with the web tests
  (`internal/api/topology_uplinks_test.go`, `web/src/topology/testdata/uplinks-api.json`); unit tests
  of the model, the style and the PDF (`uplinks.test.ts`); the page in both languages with axe
  (`topology-uplinks.test.tsx`); Playwright (`web/e2e/topology-uplinks.spec.ts`).
