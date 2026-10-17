# ADR-0039: The UI declares the physical layout as batches, previewed first

- **Status**: accepted (2026-10)
- **Concerns**: `web/src/edit`, `web/src/racks`, `web/src/pages/ObjectPage.tsx`,
  `internal/schema/builtin.yaml` (`device.u_height`), `internal/api` (`GET /racks/{id}/contents`),
  `internal/reconcile` (preview of a relation whose attributes change), `cmd/trica-e2e-server`.
  Amends ADR 0018 ("writes are two calls"). Builds on ADR 0005 (single writer), ADR 0030 (floor
  tile).

## Context

The rack elevation and the floor plan draw what the inventory says, but most of what they need
is physical and no collector can see it: which room a rack stands in and on which tile, which U a
device is mounted at and on which face, and the devices nobody can query (a PDU without a network
card, a UPS, a patch panel, a shelf). Until now only a declaration file could say it. The people
who walk the floor need to say it from the page they are looking at.

ADR 0018 kept the UI to two writes (approve and reject a proposal). Opening the UI to writes
raises four questions: through which path, what a person sees before anything is written, what a
"move" means when the placement it replaces was said by someone else, and where the height of a
device without a known model lives.

## Decision

**One path: a partial batch to `POST /batches`.** The forms build an `sdk.Batch`
(`web/src/edit/batch.ts`) and nothing else; the reconcile engine stays the only writer (ADR 0005).
Every batch is `partial`: a full one would withdraw every other claim the person ever made. The
source and the confidence are the server's (`user:<subject>`, `declared`); the page shows the
source the server answered with. Existing objects are designated by id (`sdk.IDKey`); a new
container is created inside its parent so that its name, a weak identifier, is scoped by it.

**Preview, then submit, the same batch.** The page sends the batch to `POST /batches/preview` and
shows the engine's answer in words (what it creates, the values and relations it opens or closes,
its warnings). Submit appears only under a preview of the batch the form holds now; any change to
the form drops the preview. The answer to the submit is shown as it comes: 200 applied (the
inventory queries are refreshed), 202 sent for approval (a link to the proposal, nothing
refreshed), 400, 403, 409 and 422 in words with the server's detail.

**The page checks what it can before asking.** A U position inside the rack (U 1 at the bottom,
the top unit `position + height - 1` within the rack's height), on the units the schema allows
(half units because `mounted_in.position` is a float; whole units if an overlay makes it an int),
a positive height, a whole floor tile. These block the preview. An overlap with a device already
on the same face (a full-depth device counts on both), a tile that already holds a rack, a tile
outside the room, a device whose height nobody says (checked as 1 U) are warnings: the floor may
really look like that, and the elevation flags it afterwards.

**Forms come from the schema registry.** Faces, roles, facings, kinds, units and labels are read
from `GET /schema`; a field whose attribute the registry does not have is not shown, and placing
devices is not offered at all when the registry has no `mounted_in` with a `position`. Which
attributes each container's form shows is a list in the code (`CONTAINER_FIELDS`): the registry
says what an attribute is, not which ones a person should type on the floor.

**A move withdraws the caller's own claim only.** Claims are owned per source (ADR 0004): a person
can withdraw what they said, not what a collector or a colleague said. Moving a device places it in
the new rack and withdraws the caller's `mounted_in` to the racks it leaves; when the placement it
replaces was said by another source, the form says so before anything is sent, and the device stays
listed in both racks until that source withdraws its claim (a collector does when its next full run
no longer reports it).

**A device may carry its own height.** `device` gets `u_height` (float, U). The rack contents take
it when the device has no model or its model has no height; the model's height wins when it has
one, so a wrong declaration cannot redraw a known model.

**The engine's preview shows a relation whose attributes change.** Moving a device to another U of
the same rack keeps the relation open and changes its attributes; the engine already historised
that, but listed no planned change, so the preview read "nothing would change". It now records a
`relate` change with `before` and `after`.

**Who sees the controls.** The controls appear for a caller who holds `propose` in `GET /me`; the
API checks every batch again, and a grant limited to a type or a site is refused there (403) and
shown. Free units of the elevation and free tiles of the floor plan are pointer shortcuts, kept out
of the tab order and the accessibility tree; the buttons "Place a device" and "Add a rack" are the
keyboard path to the same forms.

## Consequences

- A person's declaration and a collector's observation of the same placement coexist as two
  claims; the UI cannot "correct" a collector, only add a declared claim and let the priority
  rules and the discrepancy board say which one stands. Withdrawing another source's claim is not
  something the API offers, and this ADR does not add it.
- A "new" device named like an existing one is recognised by the engine (a weak name match outside
  any parent), not created. The preview shows it (no `create`), and the form warns.
- The batches the forms build are written to `web/src/edit/testdata/ui-batches.json` by a Vitest
  test and replayed by a Go test against the real API and engine (`ui_batches_test.go`), so a
  builder whose batch the engine would refuse or misread fails in CI, not in front of a user.
- Not done here: moving a rack between rooms (its parent), cabling, power feeds, bulk entry, and
  undo (a later batch withdraws, history keeps everything).
