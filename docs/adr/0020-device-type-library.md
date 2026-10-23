# ADR-0020: Device-type library in the netbox devicetype-library format

- **Status**: accepted (2026-10)
- **Concerns**: `internal/devicetypes`, the `device_type` schema type, the `instance_of` relation

## Context

Rack views, thermal checks and port drawings need to know what a model looks like: height,
depth, weight, airflow, ports. Collectors rarely report it. Writing a catalogue from scratch
is a task that never ends; `netbox-community/devicetype-library` already describes thousands
of models in a simple YAML format.

## Decision

> Amended by [ADR 0056](0056-growing-the-device-type-library.md): `u_height` may be 0 ("occupies
> no rack unit"), as upstream writes it for desktop and wall devices; the vendored subset grew.

1. **Format.** The library reads that format as is: `manufacturer`, `model`, `slug`,
   `part_number`, `u_height`, `is_full_depth`, `airflow`, `weight` + `weight_unit`, and the
   port lists (`interfaces`, `console-ports`, `console-server-ports`, `power-ports`,
   `power-outlets`, `front-ports`, `rear-ports`). Other keys are ignored, so an upstream file
   can be dropped in unchanged and our own files can be contributed upstream. The keys we
   use are validated strictly; a model without a positive `u_height`, with an unknown
   airflow or weight unit, a duplicate port name or a malformed slug fails the whole load.
   A half-loaded library would silently miss devices.
2. **Licence.** The upstream repository is **CC0-1.0**, checked on 2026-10-14 through the
   GitHub licence API (`https://github.com/netbox-community/devicetype-library/blob/master/LICENSE.txt`,
   master at `565a9ba`). CC0 allows redistribution under Apache-2.0. The vendored files are
   copied unchanged under `internal/devicetypes/library/<Vendor>/` and acknowledged in
   `NOTICE`. Models written by this project live in `internal/devicetypes/library/trica/`
   and carry origin `trica`, so the two never get mixed up.
3. **Vendored subset.** Arista, MikroTik, HPE, Dell and Netgate models the project expects
   to meet, plus one model of our own (a generic 1U pfSense appliance). The set is a list of
   files, not a promise of coverage; adding a model is adding a file.
4. **Mapping** to the existing `device_type` type: slug (strong identity), vendor, model,
   part number, `u_height`, `full_depth`, airflow (`front-to-rear` becomes `front_to_rear`),
   `weight_kg` (lb, oz and g converted), `psu_count` (number of power ports), and `ports`
   (a JSON list of templates: group, name, type, label, mgmt-only, max draw).
   **Port templates are not interface objects.** Creating `interface` objects from a
   template would duplicate the ones collectors observe; a template is a reference to draw
   and compare against, not an observation.
5. **Matching** a device to a model uses vendor and model, then part number. Names are
   compared lower-cased without punctuation (`CRS326-24G-2S+RM` equals the slug form), a
   vendor repeated in the model name is dropped, and a few vendor spellings are aliased
   (Arista Networks, HP / HPE / Hewlett Packard Enterprise, Dell Inc. / Dell EMC,
   Routerboard / MikroTik). A device without a vendor never matches.
   **A name that fits two different models matches none**: a wrong U height is worse than
   a missing one.
6. **Airflow variants.** Some vendors suffix the model with `-F` / `-R` for the airflow. A
   device reporting the bare name matches a synthesised base model only when every
   vendored variant agrees on height, depth, weight and ports; that model has no airflow
   and no part number, since those are exactly what differs. An exact name always wins.
7. **Writing.** The library never writes a table. `Sync` reads the devices, builds an
   `sdk.Batch` from source `library:devicetypes` and submits it to the engine (ADR 0005):
   one `device_type` object per model in use, one `instance_of` relation per matched device.
   The confidence is **declared**, the lowest, so a value a collector observed on a
   `device_type` is never overridden, and a disagreement shows up as a value mismatch.
   The batch is full-scope: when a device stops matching (its model changed) the engine
   withdraws the relation, history keeps it, and nothing is deleted.

## Consequences

- A device whose vendor and model are known gets its height, weight, airflow and port
  templates without any collector reporting them.
- The `device` type has no height attribute: the height is read through `instance_of`.
- Our own models and upstream ones share a slug namespace; a clash fails the load.
- Not decided here: a scheduler or CLI command that runs `Sync`, a UI to pick a model by hand
  (a declared `instance_of` from a user would coexist with the library's). Model images: ADR 0042.
