# Discrepancies

A discrepancy is a first-class gap: what is declared and what is observed differ, an observation
cannot be tied to the inventory, or two declarations contradict each other. They are listed by
`GET /discrepancies` (filters `status`, `kind`, `object`) and on the **Discrepancies** page, open by
default. Like everything else they are written by one place only: the engine
(`internal/reconcile`) or the cabling deducer (`internal/deduce`), never by a collector or the UI.

Each has a **fingerprint**. Opening a discrepancy whose fingerprint is already open refreshes it
(`last_seen`, `declared`, `observed`, `details`); one that was **accepted** never reopens while the
fingerprint is the same. A discrepancy is **resolved** (`closed_by`) when the gap is gone; nothing
is deleted.

| Kind | Opened by | On | Opens when | Resolves when | Fingerprint | Fields used |
|---|---|---|---|---|---|---|
| `value_mismatch` | engine | the object | an attribute has a declared and an observed value that differ | they agree, or one is withdrawn | `value_mismatch:<object>:<key>` | `key`, `declared`, `observed`, `details{declared_by, observed_by}` |
| `unresolved_neighbor` | engine | the near end | a `neighbor_of` (LLDP) names a device no collector reports | the neighbour is found | `unresolved_neighbor:<object>:neighbor_of:<hash of the endpoint>` | `details{relation, missing, source}` |
| `unresolved_reference` | engine | the known end | a relation names an object the inventory does not have | the object is found | `unresolved_reference:<object>:<relation>:<hash of the endpoint>` | `details{relation, missing, source}` |
| `cable_not_observed` | deducer | a port | a documented cable has no LLDP evidence | the pass no longer finds it | `cable:cable_not_observed:<port pair>` | `object_id`, `related_id`, `declared` |
| `cable_not_declared` | deducer | a port | LLDP sees a cable nobody documented | idem | `cable:cable_not_declared:<port pair>` | `observed` |
| `cable_conflict` | deducer | a port | the documented far end differs from the observed one | idem | `cable:cable_conflict:<port pair>` | `declared`, `observed` |
| `location_conflict` | engine | the device | the places of a device disagree on the site (below) | the places agree, are withdrawn, a mounting is withdrawn, or the rack or room changes | `location_conflict:<device>:<hash of reason and sites>` | `key: located_in` (`mounted_in` for `racks`), `details` |

The deducer resolves a `cable_*` discrepancy it did not refresh in its latest pass; the engine
resolves its own in the batch that ends the gap.

## `location_conflict`

Raised by the engine ([ADR 0061](../adr/0061-location-conflict-discrepancy.md)) when a device
touched by a batch has places (`located_in`, [ADR 0058](../adr/0058-placing-devices-outside-racks.md))
that disagree on the site, or a device is mounted in racks of different sites. Three reasons, in
`details.reason`:

- `sites`: the device is in no rack, and its open places lead to two or more different sites. The
  device has **no site** until they agree: site-limited readers lose it.
- `rack`: the device is mounted in a rack of site S and a place leads to another site. The rack
  wins, so S is the site, but that place is false and should be withdrawn.
- `racks`: the device is mounted (`mounted_in`) in racks that lead to two or more different sites.
  The device has **no site** until one mounting is withdrawn, whatever its places say, and it may
  have no place at all.

Two rooms of one site, a room and its site, or a place that leads to no site are not conflicts.
A rack in no site next to a rack in a site, two racks of one site, or racks that all lead to no
site are not conflicts either.

`details` carries the **ids and the sources only**, never a display name:

```json
{"reason": "sites", "sites": ["<site id>", "<site id>"],
 "places": [{"place_id": "…", "place_type": "room", "site_id": "…", "sources": ["user:carol"]}],
 "rack_id": "…", "rack_site_id": "…",
 "racks": [{"rack_id": "…", "site_id": "…", "sources": ["arista:eos"]}]}
```

(`rack_id` and `rack_site_id` only for `rack`, `racks` only for `racks`; `places` is always a list.) A device has at most one open `location_conflict`:
when the reason or the sites change, the old one is resolved and a new one opened. Places that
change inside the same sites refresh the same row. The check runs for the devices whose
`located_in` or `mounted_in` a batch changed, and for those placed under a room, building or rack
the batch moved, so a conflict that predates this feature is found when its device is next touched.
A preview of a batch that would open one carries a warning. A conflict is cleared by withdrawing
the wrong place (a source withdraws only its own claim); a site-limited reader does not see a
`sites` or `racks` conflict, because the device has no site.
