# ADR-0053: Putting a device back: what "taken out" means, two reads of closed placements

- **Status**: accepted (2026-10)
- **Concerns**: `internal/store` (`MountHistory`, `RemovedFrom`), `internal/api`
  (`GET /objects/{id}/placement-history`, `GET /racks/{id}/removed`), `web/src/edit`
  (`DevicePlacement`, `usePlacementHistory`, `QuickPlaceDialog` mode `putBack`),
  `web/src/racks` (`RemovedTray`, `useRemoved`). Builds on ADR 0039 and ADR 0052.

## Context

A device taken out of a rack (its `mounted_in` relation closed) left nothing to start from: to put
it back, the person retyped the rack, the U and the face. The inventory still knows them, since
nothing is deleted (the closed relation and its attributes are in the history), but no read
exposed it.

## Decision

1. **"Taken out of rack R"** means: the device's most recent `mounted_in` relation, across every
   rack, ended in R, and the device has no open `mounted_in`. A device mounted somewhere else since
   is not "removed" from R; a device that was never mounted has nothing to put back.
2. **Two reads, both over the closed relations of `relations`** (`valid` range, `upper_inf`), no
   new table, no migration:
   - `GET /objects/{id}/placement-history`: where a device was mounted (rack, U, face, since,
     until, source), the open placement first, then the closed ones, the latest closed first.
     Default 20. 404 for an object that is not a device. A rack the caller may not read is left out.
   - `GET /racks/{id}/removed`: the devices taken out of the rack (definition above), newest first,
     as the rack's items (`RackItem`: model, height, face drawings) plus `removed_at`, `removed_by`
     and the device's `status`. `since` limits the window; default 20. Same 403 as
     `/racks/{id}/contents`; a device the caller may not read is not returned.
3. **Putting back is an ordinary `placeDevice` batch.** The same partial batch as the dialog of
   ADR 0052: a `mounted_in` relation at the last rack, U and face, with no `leave`. The engine
   decides; only `internal/reconcile` writes. The dialog, in mode `putBack`, pre-fills from the
   last placement and previews at once; **Put back** then **Confirm** is two clicks.
4. **The old slot may be taken.** The quick dialog blocks overlaps (ADR 0052 point 5), so an
   occupied old slot is refused, the nearest free unit is offered, and the full form is one click
   away. A rack that is `absent` is warned about, not forbidden.
5. **A 202 is not an application.** A put back sent for approval marks the tray entry "Sent for
   approval" in the page's state only; the device stays listed until the proposal is applied.
6. **Move pre-fills the current placement.** On the device page, **Move** opens the full form with
   the current rack, U and face.
7. **The tray accepts a drag.** Each entry has a handle: dropping it on a free unit of an
   elevation opens the same dialog on that unit. The button is the keyboard path; the drag is a
   convenience, not the only way.

## Consequences

- No source of truth is added: the history the inventory already keeps answers.
- **Not measured**: the queries scan a device's (or a rack's) `mounted_in` rows, closed ones
  included; the cost on a large history is unknown. A partial index on `relations (type, to_id)`
  covering closed `mounted_in` rows is the follow-up if it shows; none is added now.
- The tray lists the newest 20, with no time window.
- A device removed from a rack and mounted nowhere else stays in the tray indefinitely.

## Amendment: integration with `located_in` (ADR 0058)

Putting a device back writes `mounted_in` and nothing else (`batch.test.ts`, "racking a device
never touches its located_in", which covers `putBack`): a place a person gave the device while it
was out of its rack is neither closed nor read by the tray, and the rack wins again as soon as the
device is back in it. The tray (`/racks/{id}/removed`) lists closed `mounted_in` relations only; a
device removed from the rack and placed in a room shows in the tray and in the room's "Devices
here", and the device page shows its two sections, "Rack placement" and "Location", side by side.
