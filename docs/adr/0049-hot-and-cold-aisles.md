# ADR-0049: A rack says which aisle its front faces; every mounted device is checked against it

- **Status**: accepted (2026-10)
- **Concerns**: `internal/schema/builtin.yaml` (`rack.front_aisle`), `internal/airflow` (new),
  `internal/api` (rack contents' `airflow`, `GET /objects/{id}/airflow`), `internal/store`
  (`DescendantValue.Display`), `internal/collect/arista` (airflow facts), `web/src/aisles` (new),
  `web/src/racks`, `cmd/trica-e2e-server` (room-b, r30 to r36). Builds on ADR 0004 (claims),
  ADR 0039 (the UI declares as batches), ADR 0048 (choosing the chassis variant).

## Context

An operator asked to say "hot aisle" and "cold aisle" on a rack page, and for trica to confirm
from it that each device is mounted the right way round and that its fans blow the right way.
A device turned the wrong way (a port-side intake switch mounted on the rear, a power supply of
the other airflow) breathes the hot air of its neighbours; nothing in the inventory said so.

The schema already had what the check needs on the device side: `device_type.airflow` (NetBox
values, relative to the device's own front), `device.airflow` ("as configured or observed"),
`psu.airflow` and `fan.airflow`, and `mounted_in.face` (the rack face the device's front is on).
It also had an `aisle` type (child of a room, `temperature: hot|cold`) that nothing used, with
no geometry. It did not have which way a rack is turned relative to the aisles.

## Decision

1. **The fact is a rack attribute: `rack.front_aisle: cold | hot`.** The rear faces the other
   aisle. It is one claim per rack, said in one gesture on the rack page, owned by whoever said
   it (ADR 0004), historised, and visible on the discrepancy board like any attribute when two
   sources disagree. The floor plan does not need more: a rack has `floor_x`, `floor_y` and
   `facing`, so the tile in front of it is its front aisle and the tile behind it the other one;
   the room's aisles are drawn from its racks, and a tile that one rack says is cold and another
   says is hot is flagged on the plan and in the racks table (one of them is turned the wrong
   way, or one declaration is wrong). That works for any number of racks and rows.

   Rejected: relating the rack to `aisle` objects (`front_of` / `rear_of`). An aisle object has no
   position, so it would draw nothing the derivation does not; the one-click gesture would have
   to find or create the right aisle of the room (a weak name inside the room merges every "Cold
   aisle"); and a wrong relation is harder to see than a wrong value. The `aisle` type stays, for
   naming an aisle; a later ADR may give it geometry and derive `front_aisle` from it.

2. **Declaring it.** The rack page's "Aisles and airflow" card offers, to a caller who holds
   `propose` and when the registry has `rack.front_aisle`: "The front of this rack faces: the
   cold aisle | the hot aisle | not set". The choice builds a partial batch
   (`declareFrontAisle` in `web/src/edit/batch.ts`): the rack by id with `front_aisle`, or, for
   "not set", a withdrawal of the caller's own claim. It is previewed on `POST /batches/preview`
   and submitted to `POST /batches` like every UI declaration (ADR 0039). "Not set" when the
   value is someone else's sends nothing and says so: the page never withdraws another source's
   claim. The batches are replayed against the real engine (`ui_batches_test.go`).

3. **The check is computed by the server, in one pure package (`internal/airflow`)**, so the
   API, the browser tests' mock and any later consumer (MCP) say the same thing:
   - the airflow used is the device's own `airflow` when it has one (configured or observed),
     else what all of its fans and power supplies that report one agree on, else its model's;
   - the intake face is the mounting face for `front_to_rear`, the other face for
     `rear_to_front` (a front-to-rear device mounted on the rear takes its air in at the rack's
     rear); `side_to_rear` is judged by its exhaust;
   - the cold face is the front when `front_aisle` is cold, the rear when it is hot;
   - **ok** when the intake is the cold face (`intake_on_cold_aisle`), a side-to-rear device blows
     into the hot aisle (`exhaust_into_hot_aisle`) or the device is `passive`; **mismatch** when
     the intake is the hot face (`intake_on_hot_aisle`), a side-to-rear device blows into the cold
     aisle (`exhaust_into_cold_aisle`), or the device is right but one of its fans or power
     supplies is turned the other way (`part_intake_on_hot_aisle`); **unknown** with the missing
     fact otherwise (`aisle_not_set`, `airflow_unknown`, `face_unknown`, `side_airflow`,
     `mixed_airflow`, `not_mounted`).
   - Warnings, besides the status: `airflow_contradicts_model` when the device or its parts say
     another airflow than its model (with `model_declared` when a person chose that model,
     ADR 0048: the other airflow variant may be the right one), `parts_disagree` naming the
     fans or power supplies turned another way. These are said with the check, not opened as
     `discrepancy` rows: only the engine opens those, from two claims of the same attribute.

4. **Where it is shown.** Each rack item of `GET /racks/{id}/contents` carries `airflow`. The
   rack page names the aisle of each elevation (blue "Cold aisle", red "Hot aisle"), draws the
   rack from the side with the air going from the cold aisle to the hot one, outlines a reversed
   device in amber with a strip on its right edge (its accessible name says "airflow:
   Reversed"), lists the reversed devices with the reason and the warnings, and adds an Airflow
   column to the mounted table. `GET /objects/{id}/airflow` gives the device page an "Airflow"
   card with every fact the check read. Colours come from the theme's tokens (both themes),
   words carry the meaning, the CSP is unchanged (no inline style).

5. **What collectors say.** Collectors stay read-only (show commands only). The Arista collector
   reports `device.airflow` from `show environment cooling`'s `airflowDirection` where the
   release has it, and `airflow` on fan trays and power supplies from their part numbers
   (`-F` / `-RED` forward, `-R` / `-BLUE` reverse). The model's own suffix (`DCS-7050SX-64-F`)
   already chooses the library model and its airflow (ADR 0043). Servers (Redfish), MikroTik and
   most fixed-airflow devices report no direction: their model's airflow is the one used.

## Consequences

- A rack with no `front_aisle` checks nothing: every device is "not checked", and the page asks
  for the declaration. The owner of a room says it once per rack.
- A collector that reports the airflow a switch really blows wins over the model the library or
  a person chose; the contradiction is shown so that someone fixes the model or the fan trays.
- The rack contents read one more query (the airflow of the devices' parts), whatever the number
  of devices.
- Not done here: bulk declaration for a row, aisle objects with geometry, the check in MCP tools,
  a `discrepancy` kind for airflow contradictions, temperature-based confirmation (inlet warmer
  than the room's cold aisle).
