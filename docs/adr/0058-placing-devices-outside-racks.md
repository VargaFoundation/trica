# ADR-0058: A device that is in no rack is placed in a site, a building or a room by a declared `located_in` relation

- **Status**: accepted (2026-10)
- **Concerns**: `internal/place` (new), `internal/schema/builtin.yaml` (`located_in`),
  `internal/reconcile/engine.go`, `internal/api/access.go`, `internal/search/compile.go`,
  `web/src/edit/` (`batch.ts`, `rules.ts`, `useLocation.ts`, `PlaceInForm.tsx`, `DeviceLocation.tsx`),
  `web/src/pages/ObjectPage.tsx`, `web/src/inventory/Fleet.tsx`, `docs/reference/{tql,security,ui,collectors,mcp}.md`.
  Builds on ADR 0004 (claims per source), ADR 0005 (single writer), ADR 0036 (identity), ADR 0039
  (people declare the physical layout) and ADR 0057 (UniFi). `pkg/sdk` is untouched. Closes #161.

## Context

A device has no parent in the schema: its place is the rack it is mounted in, and its site is its
rack's site. Many devices are in no rack: the office's access points and door readers (ADR 0057),
a desktop NAS, a camera. Such a device has **no site**. Three things follow, all visible once a
UniFi estate is loaded:

- a grant limited to a site (`Grant.Sites`, the MCP `Guard`) refuses it, because an object with no
  site fails closed (`internal/api/access.go`);
- the query `site:paris` does not find it, and the object page shows no site;
- nothing says where it is: the fleet lists it under "Not in a rack".

The rule "the site of an object" exists in exactly two places today, both SQL: `Server.siteIDs`
(walk up through `parent_id` and open `mounted_in`) and the compiler's `site:` (the same rule walked
down from the site). Any new way to place an object has to enter both, and keep them equal.

## Decision

1. **A new declared relation, `located_in`**, from `device` to `site`, `building` or `room`. No
   attribute, no impact. A person says it (the object page's "Place in a site, building or room",
   a declaration file, an MCP proposal). A device mounted in a rack is where its rack is, whatever
   a `located_in` says: the rack wins; a place a person gave earlier stays in the history, is shown
   as ignored and can still be withdrawn.

2. **One package holds the rule: `internal/place`.** `place.Sites` (up) and `place.WithinSites`
   (down) are the only statements of "the site of an object": parent first, then an open
   `mounted_in`, then an open `located_in` **only when the object has no open `mounted_in`**. Two
   different sites still mean no site (fail closed); two places in the same site are fine. The API
   (`siteIDs`, hence REST, `Guard.SiteIDs`, `LoadObject`, `ObjectAt`) and the search compiler call
   it; a test checks that, over a whole inventory, the two directions agree object by object
   (`TestWithinSitesAgreesWithSites`).

3. **One place per source, enforced by the engine.** When a source claims `located_in` from a
   device D to a place P, the engine withdraws the same source's other `located_in` claims from D
   in the same batch. A move closes the old relation whoever sends it (the UI, an MCP proposal, a
   declaration file), the preview lists the `unrelate`, and the history keeps the closed relation
   (ADR 0004). Two different places from one device in one batch is a schema problem: a rejection
   for a person or token (`ErrRejected`), a warning for a collector. Claims of other sources are
   untouched: the relation stays open while any source claims it.

4. **Only declared claims.** A `located_in` claim whose effective confidence is not `declared` is a
   schema problem (a rejection for a person or token, a warning and the claim dropped for a
   collector). Collectors do not place devices in this story: the UniFi v1 API has no physical
   placement; claim priority works per (type, from, to), so an observed placement and a person's
   would be two relations and the device would end up with no site instead of the person's choice
   winning; and endpoints given by identity never create objects (`resolveEndpoint`), so a collector
   could not create the sites anyway. The trust gap that exists for every attribute remains: a token
   bound to a collector may send `declared`.

5. **The rule sets are code, not schema flags.** `place.DeclaredOnly` and `place.Exclusive` are
   hardcoded (both hold for `located_in` only). The alternative, a `single:` and `declared_only:`
   flag on `RelationType`, would add a surface to `/schema` and OpenAPI, and an overlay could weaken
   it. It stays an option if a second relation needs the same rules.

6. **Where it shows.** The device page has a "Location" section (the places, who said so, "Place
   in a site, building or room" / "Move to another place", "Remove from this place"; for a mounted
   device, the old places as "ignored while in a rack"). A site, building or room page lists the
   devices placed there ("Devices here"). The "Location" fact shows the place of a device in no rack.
   The fleet groups devices in no rack under their place, between the racks and "Not in a rack".

## Options not taken

- **`parents: [room, building, site]` on `device`.** Weak identity matching is scoped to the parent
  (`reconcile/engine.go` `bestMatch`: `parent_id IS NOT DISTINCT FROM`): a device given a room as
  parent would stop matching the batches of a collector that identifies it by name or `mgmt_ip`
  alone, and duplicates would appear. A parent cannot be withdrawn (a batch without a parent leaves
  it), and there is no claim per source.
- **`contains` from the place to the device.** It goes from any type to any type and has
  `impact: from_to`: a room failing would "take down" its access points.
- **UI-only `unrelations` for a move, no engine rule.** MCP and declaration-file moves would leave
  two places open, and the site would read as unknown.
- **A collector's placement as an observed claim.** See 4. A later path: a collector's suggested
  place as a proposal.

## Consequences

- No migration: the partial indexes on `relations (from_id)` and `(to_id)` where `upper_inf(valid)`
  cover the new SQL.
- A device whose sources disagree about the site (two people, two rooms of two sites) has no site:
  site-limited readers lose it and `site:` finds it under both sites (the same already holds for
  `mounted_in`). Nothing says so beyond that; a `location_conflict` discrepancy is a follow-up (#169).
- `objectAt` on a past date still uses today's site.
- Export and import carry `located_in` as any relation; an import is `declared`, so it round-trips.
- **PR #150 (topology map)** has its own Go copy of the rule (`internal/topomap/index.go`,
  `AuthSite`, which says it gives the same answer as `siteIDs`) and places a device with `contains`
  from a location. Whichever of the two PRs lands second must make `AuthSite` and `placeDevice`
  follow `located_in`, the rack taking precedence; until then the map refuses access to placed
  access points (safe, but not what the API says). A pure-Go `place.SiteInGraph` belongs in
  `internal/place` at that point, not before.
- The e2e mock server (`cmd/trica-e2e-server`) has no `located_in` in its schema, so the Playwright
  suite does not exercise the new controls: they are tested with the web unit tests and the API
  tests that replay the builders' batches.
