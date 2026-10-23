# ADR-0051: The topology map, four levels over one cached graph, links aggregated per pair of places

- **Status**: accepted (2026-10)
- **Concerns**: `internal/topomap` (new), `internal/graph` (`Cache.Recent`, `Edge.Confidence` and
  `Edge.Attrs`), `internal/api` (`GET /topology/map`), `internal/wiring` (the graph is kept current
  with or without MCP). Amends ADR 0024 (the topology page's source), ADR 0041 (the live overlay is
  read by the map server-side) and ADR 0011 (the graph has a second reader). Builds on ADR 0005
  (nothing here writes) and ADR 0047 (rack floor positions).

## Context

The topology page draws every device and cable of the inventory at once (ADR 0024). That works for
a lab and not for a fleet: thousands of nodes, no notion of where anything is, and the answer to
"what links Paris to Lyon" is a haystack. Operators think in places: sites, rooms, racks. The
inventory already says where things are (`parent_id`, `mounted_in`, `contains`, rack `floor_x`,
`floor_y`, `facing`, room `width_mm`, `depth_mm`) and what joins them (cables, adjacencies,
tunnels, uplinks); nothing combined the two, and the live overlay (ADR 0041) is keyed by interface.

## Decision

1. **One endpoint, `GET /topology/map?level=&id=`, one level per call.** Levels: `global` (the
   sites, the Internet, what is in no site), `site` (its rooms, an area for what is in the site
   outside rooms), `room` (its racks on the floor plan; a site id gives the "site floor": the racks
   in no room), `rack` (its devices with their mount), `unplaced` (devices placed nowhere). The
   server answers nodes and edges; the browser never aggregates. A level never lists more than 500
   nodes or 2000 edges (`truncated`).
2. **A device is in the place it is mounted in** (`mounted_in`, strongest confidence, then lowest
   rack id; ambiguity is counted in `notes`), else, for a device in no rack, the site, building or room it is `located_in` (ADR 0058: the rack wins), else the location that `contains` it, else its parent
   chain, else unplaced. A link belongs to the owner devices of its two ports.
3. **A link is drawn between the children of the lowest common ancestor.** What is outside the
   level is a *portal* node standing for the place where it lands (`portal:<id>`, or `portal:internet`);
   a link between two things inside one node is counted in that node's `internal_links` and not drawn.
4. **Edges aggregate per pair of places and medium** (`physical`, `tunnel`, `uplink`); the id is
   `a~b~medium` with `a < b`, and every directional value is in the a-to-b sense. An edge carries its
   links (`members`, capped at 50), the kinds that asserted them, the weakest link's confidence, the
   capacity (sum of each link's slowest known end; `capacity_partial` when one is unknown), the
   operational state and the live rates.
5. **One adjacency is one link.** The same two ports joined by a cable, by `cabled_to` and by
   `neighbor_of` count once, the strongest confidence winning (cable > `cabled_to` > `neighbor_of`
   for the kind). A `connects` cable counts only with exactly two ends.
6. **A LAG is not added to its members.** Live rates are the sum of the physical links' ports. A
   link between two `kind=lag` interfaces is dropped when a member-port link exists between their
   members (the aggregate repeats the members); a LAG-only adjacency (members unknown) is kept, with
   the aggregate's own rate. This deviates from "never use aggregated entries" so that a LAG seen
   only by LLDP is not lost.
7. **Tunnels and uplinks are derived.** A tunnel's peer is resolved against device management and
   primary addresses, names and short names, `ip_address` assignments and the public addresses of an
   Internet access; an ambiguous key is not guessed, an unresolved peer goes to the Internet. A
   device that `routes_via` a gateway or an Internet access gives an uplink (a gateway is folded into
   the access it routes via). Tunnels and uplinks have no measured rate.
8. **Rights are the existing ones, applied in memory.** `Index.AuthSite` reproduces the SQL
   `siteIDs` (`place.Graph`, ADR 0058: parents, current `mounted_in` and, for a device in no rack, its `located_in`, stopping at a site, depth 32, two sites fail closed;
   `TestAuthSiteEqualsSiteIDs` runs both on the same data). A node is listed only if the caller reads
   its type in its site; a link needs both ports and both owner devices; tunnels, gateways and
   Internet accesses have no site, so the Internet and uplinks go only to a caller who reads them
   everywhere. A caller never learns that a portal exists to a site they cannot read.
9. **Source: the graph cache, at most 10 s behind.** `graph.Cache.Recent(maxStale)` serves a
   snapshot invalidated by a commit for up to `maxStale`, then reloads: a burst of commits costs one
   reload. The index (placement, links, tunnels, uplinks) is built once per snapshot and memoised.
   The cache is invalidated by every commit and every `trica_changes` notification whether or not the
   MCP server is mounted (`mcp.graph_refresh` now governs both). The live rates come from the
   existing `live.Snapshot` and are looked up server-side.

## Alternatives rejected

- **Aggregating in the browser** from `/relations` and `/topology/live`: ships every cable to every
  viewer, repeats the rights logic client-side, and cannot cap its answer.
- **A SQL view / materialised table of links:** a second inventory to keep consistent with the
  engine (ADR 0005), and the graph cache already exists.
- **One call returning every level:** the payload grows with the fleet, not with the screen.
- **Summing every interface with a rate** (LAG included): double-counts every aggregate.

## Consequences

- The graph cache holds all objects, so memory grows with the inventory; measured at 2000 devices
  and about 3900 cables the index builds in 18 ms and a level answers in under 30 ms (STATUS row 107).
- A map is up to 10 s behind a commit; a UI that must show its own write refetches.
- The web page (a later PR) draws this payload and nothing else; its test data is recorded from the
  server (`TestTopologyMapRecordedForTheUI`).
- Not covered: a measured rate for tunnels and uplinks, a view per VLAN, path highlighting.

## Amendment: integration with `located_in` (ADR 0058) and `uplinked_to` (ADR 0062)

- The rights walk is no longer a Go copy of the SQL: `internal/place` holds both, `place.Sites`
  over PostgreSQL and `place.Graph.Site` over the snapshot, and `Index.AuthSite` calls the latter.
  A test proves they agree on a world with a placed access point, a racked device also
  `located_in` elsewhere and a device placed in two sites.
- A device in no rack is drawn in the place it is `located_in`; a rack, even one outside any site,
  always takes precedence, as for the rights.
- `uplinked_to` (the device-level line of ADR 0062) is drawn: see the next amendment.

## Amendment: `uplinked_to` on the map (#185)

- **A medium of its own, `uplinked_to`.** The name `uplink` already means an Internet access
  (`routes_via`), so the device uplink is the fourth medium, with a kind counter
  `kinds.uplinked_to`. It is aggregated per pair of places exactly like the other media (rules 3
  and 4): two devices in the same node add to that node's `internal_links`. Only device-to-device
  relations between devices that are not absent count.
- **Deduplication, decided per view (ADR 0062).** An uplink is dropped when a port-level link the
  caller can read (cable, `cabled_to` or `neighbor_of`, both ports and both devices readable)
  joins the same two devices. It is then neither drawn nor counted ("one adjacency is one link",
  rule 5). A caller who cannot read the interfaces therefore sees the uplink instead: this reveals
  nothing, since both devices are readable.
- **Rights.** Both devices must be readable (they are the only objects an uplink has: it names no
  port).
- **No live data.** There is no port to measure: the edge is `oper: "unknown"`, with no rates, no
  capacity, `capacity_partial` false and `measured` false, whatever the live values are keyed by.
- **Direction.** `toward` on an edge says which end the uplink devices are on: `a`, `b` or `both`
  (two uplinks in opposite directions). It is computed over every link, so the 50-member cap does
  not change it. Members of an uplink edge have empty ports, `kind: "uplinked_to"` and the same
  `toward` (`a` or `b`); they are oriented with the edge, like all members.
- **Drawn without relying on colour.** Dash-dot-dot (`10 3 2 3 2 3`; the tunnel already uses
  dash-dot), an arrowhead on each `toward` end, and a sideways shift of 8 px so that the line does
  not sit on a cable or a tunnel between the same two places. The colour stays the grey of every
  unmeasured line. The legend gets an entry only while such an edge is drawn; the table and the
  panel say "Uplink (port unknown)", and the panel "An uplink names no port: there is nothing to
  measure."
- **Amended by #195 and #196.** Every medium has a fixed sideways shift off the arc its pair
  shares: `physical` and `uplink` 0 (an uplink always ends at the Internet, a cable never does, so
  they never share a pair), `uplinked_to` +8 px, `tunnel` -8 px on the other side. Media that can
  share a pair are at least 8 px apart, more than half the 14 px hit target, so each line stays
  selectable. Measured lanes follow their line's shift. The tunnel's dash-dot (`10 4 2 4`) is now
  set by the scene, not the stylesheet, and the legend lists the tunnel only while one is drawn.
- The map has no export and no filter, so there is nothing to carry over.
