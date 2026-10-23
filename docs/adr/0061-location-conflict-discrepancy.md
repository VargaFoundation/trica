# ADR-0061: a `location_conflict` discrepancy when the places of a device disagree on the site

- **Status**: accepted (2026-10), amended (2026-10, #180)
- **Concerns**: `internal/place` (`Conflicts`, `PlacedUnder`), `internal/reconcile` (the step
  `checkPlacements`, `ResolveOthers`), `web/src/pages/DiscrepanciesPage.tsx`,
  `web/src/edit/DeviceLocation.tsx`, `docs/reference/discrepancies.md` (new). Amends ADR 0058
  (Consequences). Builds on ADR 0005 (single writer), ADR 0004 and 0010 (discrepancies). No
  migration, no API or OpenAPI change, `pkg/sdk` untouched. Closes #169.

## Context

ADR 0058 lets a person place a device in a site, building or room (`located_in`), one place per
source. Two sources can say two sites. `place.Sites` then fails closed: the device has no site,
site-limited readers lose it, and the `site:` query finds it under both sites. Nothing told anyone.
A second, quieter case: a device mounted in a rack of site X and placed in a room of site Y is in X
(the rack wins), but the place is false, and nobody sees that either.

`internal/reconcile` already raises discrepancies from inside a batch (`value_mismatch`,
`unresolved_reference`) and `internal/deduce` raises the `cable_*` ones in a periodic pass. The
`discrepancies` table has no constraint on `kind`.

## Decision

1. **One kind, `location_conflict`, three reasons.** `reason: "sites"`: two or more open places lead
   to two or more distinct sites and the device is in no rack; it has no site. `reason: "rack"`: the
   device has an open `mounted_in` to racks of exactly one site S and a place leads to another known
   site; the rack wins, S is the site, a declaration is false. Same site, or a place that leads to no
   site, is not a conflict. `reason: "racks"` (amended, #180): the device has open `mounted_in` to racks
of two or more distinct known sites; it has no site (`place.Sites` fails closed), whatever its places
say, and `racks` wins over the other two reasons. A rack in no site next to a rack in X is not a
conflict (`Sites` gives X). One kind because the filter, the label and the fix (withdraw the stale
   place) are the same; the reason tells them apart.
2. **The check runs in the engine**, as a step `checkPlacements` between `resolveRelations` and
   `writeHealth`, in the transaction of the batch that changed the places. The conflict opens and
   closes with the change: idempotent by construction, and a preview carries a warning so a person
   sees it in the review before submitting. A periodic pass was rejected: it would lag, and could
   not warn a person at the moment they place the device.
3. **Which devices are checked**, and only these: the `from` of every `located_in` or `mounted_in`
   claim the batch changed, and the devices placed or mounted under an object whose parent changed
   in this batch (`place.PlacedUnder`): a room moved to another site, a rack moved. Without the
   second set a conflict created or ended by moving a room would go unseen until someone touched
   the device. A conflict that existed before this change is found when its device is next touched
   (no release carries `located_in`).
4. **The rule stays in `internal/place`.** `place.Conflicts` states the disagreement next to
   `place.Sites`, and a test checks over a whole inventory that they agree (`sites` conflict means no
   site, `rack` conflict means the rack's site, `racks` conflict means no site).
5. **Fingerprint** `location_conflict:<device>:<hash(reason|sorted sites)>`. Places changing inside
   the same sites refresh the details of the same row. A new site or a new reason is a new
   fingerprint: the engine resolves the other open rows of the device (`ResolveOthers`) and opens
   the new one, so a device has at most one open `location_conflict`. As for every kind, an
   accepted discrepancy with the same fingerprint never reopens; a third site does.
6. **Payload.** `object_id` is the device, `key` is `located_in` (`mounted_in` for `racks`), `declared` and `observed` are
   empty. `details` is the `place.Conflict` as JSON: the reason, the sites, each place with its
   type, its site and its sources, for `rack` the rack and its site, and for `racks` the `racks[]` (rack id, its site, its sources;
`sites` holds the sites of the racks only). `places` is always a list, never null. **Ids and sources only, no
   display name**: a reader allowed to see the device learns no name outside their grant. A `sites`
   conflict belongs to a device that has no site, so a site-limited reader does not see it either
   (fail closed, as for the device itself).
7. ~~Racks in two different sites are out of scope.~~ **Amended by #180:** they are the `racks`
   reason. `Conflicts` now also examines devices that are only mounted (it used to loop over the
   placed ones), and the engine's candidate selection already covered them.
8. **UI.** The discrepancies page shows the kind translated (the raw kind stays beside it), filters
   by a list of the known kinds (an unknown kind in the address stays selectable), and lists the
   places of a location conflict with their sites and sources. The device's Location section says
   when a conflict is open and links to the list.

## Consequences

- No migration, no new table, no API change: `Discrepancy.details` is already free-form.
- Every batch that touches a placement runs one more query set (two selects and the site walk) over
  the devices it touched, not the inventory.
- A conflict is resolved by withdrawing one of the places (each source can withdraw only its own
  claim) or by the rack or the room changing; trica never picks a winner among declarations.
- An accepted conflict with the same sites stays accepted; site-limited readers do not see a
  `sites` conflict.
- Racks in two sites open a `racks` conflict when their device is next touched (#180); one that
  predates it is found then. Racks that all have no site stay silent.

## Amendment (#180)

`racks` is a reason of its own rather than a use of `sites`: `sites` means "no rack, the places
disagree", and a mounted device may have no place at all. The fingerprint policy is unchanged, over
the reason and the sites of the racks. The person withdraws a `mounted_in`, so `key` is
`mounted_in`. `Sites` and `WithinSites` are not changed.
