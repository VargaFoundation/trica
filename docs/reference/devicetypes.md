# Device-type library

Models of equipment (rack height, depth, weight, airflow, port templates) in the file format
of [netbox-community/devicetype-library](https://github.com/netbox-community/devicetype-library).
Decision and licence evidence: [ADR 0020](../adr/0020-device-type-library.md).

## What a device gets

For every inventory device whose `vendor` and `model` (or `part_number`) match a model,
`devicetypes.Sync` submits a **declared** batch (source `library:devicetypes`) to the
reconciliation engine:

- a `device_type` object (identity: `slug`) with `vendor`, `model`, `part_number`,
  `u_height`, `full_depth`, `airflow`, `weight_kg`, `psu_count`, `ports`;
- an `instance_of` relation from the device to that `device_type`.

The rack height of a device is therefore `instance_of` then `u_height`. Port templates are a
JSON list on the `device_type` (`group`, `name`, `type`, `label`, `mgmt_only`, `max_draw_w`);
they do not create `interface` objects.

Because the batch is declared, it never overrides a value a collector observed, and a
disagreement is listed as a value mismatch. When a device's model changes, the old relation
is closed (history keeps it) and a new one opened.

## Matching

| Rule | Example |
|---|---|
| Case and punctuation ignored | `dcs 7050tx 48` = `DCS-7050TX-48`; the slug is accepted as a name too |
| Vendor spellings aliased | `Arista Networks`, `HP`, `Hewlett Packard Enterprise`, `Dell Inc.`, `Dell EMC`, `Routerboard` |
| Vendor repeated in the model is dropped | `Dell PowerEdge R640` |
| Then the part number | `P19766-B21` |
| Airflow variants | `DCS-7050SX3-48YC8` matches the `-F` model only if all variants agree on everything but airflow; airflow and part number are then left out |
| Ambiguous name | matches nothing |
| No vendor | matches nothing |

## Adding a model

Drop a file under `internal/devicetypes/library/<Vendor>/<model>.yaml` (upstream files as
they are; models written here go in `library/trica/`). Required: `manufacturer`, `model`,
`slug` (lowercase, digits, `.`, `-`), `u_height` > 0. `weight_unit` is one of `kg`, `g`,
`lb`, `oz`. `airflow` is one of `front-to-rear`, `rear-to-front`, `left-to-right`,
`right-to-left`, `side-to-rear`, `passive`, `mixed`. `go test ./internal/devicetypes/` loads
and validates every file; an invalid one fails the load.

## Not covered

No command or schedule runs `Sync` yet; images and module bays are ignored; no
model is picked by hand in the UI.
