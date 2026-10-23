# ADR-0052: Rack placement in a dialog: a modal primitive, a picker of devices in no rack, quick dialogs that block overlaps

- **Status**: accepted (2026-10)
- **Concerns**: `web/src/components/ui/Dialog.tsx` (new), `web/src/edit` (`QuickPlaceDialog`,
  `DevicePicker`, `useQuickReview`, `PlaceDeviceForm`), `web/src/racks/RackPage.tsx`,
  `internal/api` (`GET /objects?mounted=`), `web/src/styles` (`--trica-scrim`,
  `--trica-shadow-overlay`). Amends ADR 0040 (the list of primitives) and ADR 0039 (the
  declaration form is "not a modal"). Builds on ADR 0018 and ADR 0039.

## Context

Placing a device from the rack page opened a form in the page, under the actions: search a device
by name among every device the inventory has, choose a rack, a U, a face, a height, press Preview,
read, press Submit. The elevation is the thing the person looks at, and the form pushed it down.
The search offered devices that were already mounted (placing one of those is a move, and the
person usually wanted a device nobody had placed yet), and a drag, a **Move** or a **Turn**, which
have nothing left to ask, opened the whole form anyway.

## Decision

1. **A modal `Dialog` primitive on the native `<dialog>` element.** `showModal()` makes the page
   behind inert, draws the backdrop and closes on Esc without a library. The primitive adds what
   the element does not: the heading as the accessible name, the focus on the first control when
   it opens and back on the opener when it closes, Tab wrapping inside, a close button, a click on
   the backdrop. Only tokens (`--trica-scrim`, `--trica-shadow-overlay` are new, in the three
   theme blocks); no inline style (the CSP forbids it).
2. **Placing, moving and turning a device are a dialog** (`QuickPlaceDialog`, modes `add`,
   `move`, `turn`, `putBack`). It opens on a free unit (U and face filled in), on **Place a
   device**, on a drag, on **Move** and **Turn**. The rack's own fields and the full form stay a
   panel: a dialog is for one short decision.
3. **The picker offers devices in no rack.** `GET /objects` takes `mounted=true|false`, the same
   as `rel.mounted_in:*` and `-rel.mounted_in:*` in TQL, ANDed with `q` (400 for another value).
   The picker asks for `mounted=false` and names both statuses in `q` (the API hides `absent`
   objects unless the query says `status`), marks an absent device, and drops from the list ids the
   rack already holds. A toggle, "Include devices mounted in other racks (move)", drops
   `mounted=false` and says picking one leaves its rack. "Installed" means an open `mounted_in`
   relation; nothing new is stored.
4. **The preview is automatic in the dialog, and is still the engine's.** The batch is sent to
   `POST /batches/preview` once it is valid and has stopped changing (300 ms), and the dialog shows
   the engine's planned changes, not a sentence of its own. **Place** opens only under a preview
   of the batch that would be sent and that changes something, then sends the same batch to
   `POST /batches`. This keeps ADR 0039's rule (the page never says what a change does on its own
   authority); only the click on "Preview" goes away.
5. **A quick dialog blocks overlaps; the full form warns.** Two devices on the same U of a face
   (a full-depth device counts on both) disable **Place** and the dialog offers the nearest free
   position and, when the device is not full depth, the same U on the other face. "Open full form"
   carries the device, U, face and height to the form of ADR 0039, which keeps its warn-only
   behaviour for the cases where the floor disagrees with the model.
6. **Outcomes.** Applied (200): the dialog closes and the page says what happened ("Placed srv-02
   at U12 in r12.") in a status line. Sent for approval (202): the dialog stays with "Sent for
   approval · Proposal #n". A refusal is shown as the server wrote it.

## Consequences

- A `showModal()` dialog is inert to everything behind it, including the app's skip links; the
  dialog is short and closes on Esc, and the focus returns to where it came from.
- Tests: jsdom has no `showModal()`; `src/test/setup.ts` adds a shim that only sets `open`. The
  inertness and the backdrop are checked in a real browser (Playwright), not in Vitest.
- `PlaceDeviceForm` (the full form, and the device page's "Place in a rack") uses the same picker.
- Put back (a device whose placement was withdrawn) reuses the dialog with `mode="putBack"`.

## Not decided

Keyboard placement without the pointer on the drawing (the buttons and the dialog are the keyboard
path today), and a picker that sorts by what fits the free units.

## Amendment: integration with `located_in` (ADR 0058)

The dialog and the placement forms write `mounted_in` and nothing else: racking a device never
closes or replaces a `located_in` the device has (`batch.test.ts`, "racking a device never touches
its located_in"). The engine lets the rack win, and the old place stays in the history, shown on
the device page as "ignored while in a rack" and withdrawable there. The device page keeps its two
sections, "Rack placement" (this ADR) and "Location" (ADR 0058), each with its own data; the
"devices in no rack" filter of the picker looks at `mounted_in` only, so a device placed in a room
is still offered. If the rack is in another site than that place, the engine raises the
`location_conflict` of ADR 0061.
