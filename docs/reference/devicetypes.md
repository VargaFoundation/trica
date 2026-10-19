# Device-type library

Models of equipment (rack height, depth, weight, airflow, port templates) in the file format
of [netbox-community/devicetype-library](https://github.com/netbox-community/devicetype-library).
Decision and licence evidence: [ADR 0020](../adr/0020-device-type-library.md).

## What a device gets

For every inventory device whose `vendor` and `model` (or `part_number`) match a model,
`devicetypes.Sync` submits a **declared** batch (source `library:devicetypes`) to the
reconciliation engine:

- a `device_type` object (identity: `slug`) with `vendor`, `model`, `part_number`,
  `u_height`, `full_depth`, `airflow`, `weight_kg`, `psu_count`, `ports`, and `front_image` /
  `rear_image` (the library path of the image, when there is one, see below);
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
| First, a part number listed in a model's `part_numbers` (the chassis variant, [ADR 0043](../adr/0043-device-type-variants-and-drawn-faces.md)) | `ProLiant DL380 Gen9` + `767032-B21` = `hpe-proliant-dl380-gen9-24sff`, matched `by sku` |
| Case and punctuation ignored | `dcs 7050tx 48` = `DCS-7050TX-48`; the slug is accepted as a name too |
| Vendor spellings aliased | `Arista Networks`, `HP`, `Hewlett Packard Enterprise`, `Dell Inc.`, `Dell EMC`, `Routerboard` |
| Vendor repeated in the model is dropped | `Dell PowerEdge R640` |
| Then the part number | `P19766-B21` |
| Airflow variants | `DCS-7050SX3-48YC8` matches the `-F` model only if all variants agree on everything but airflow; airflow and part number are then left out |
| Ambiguous name, or a SKU listed by two models | matches nothing (the SKU then leaves the decision to the name) |
| No vendor | matches nothing |

## Adding a model to the embedded library

To run with your own models without a rebuild, see "Your own models and images" below. To add
one to trica itself, drop a file under `internal/devicetypes/library/<Vendor>/<model>.yaml` (upstream files as
they are; models written here go in `library/trica/`). Required: `manufacturer`, `model`,
`slug` (lowercase, digits, `.`, `-`), `u_height` > 0. `weight_unit` is one of `kg`, `g`,
`lb`, `oz`. `airflow` is one of `front-to-rear`, `rear-to-front`, `left-to-right`,
`right-to-left`, `side-to-rear`, `passive`, `mixed`. `go test ./internal/devicetypes/` loads
and validates every file; an invalid one fails the load.

`part_numbers` (a trica extension, ignored by upstream tools) lists the vendor SKUs of one
chassis variant, configure-to-order and pre-configured: a server reporting one of them gets
that model whatever its model name says. Use it when a vendor reports one name for several
chassis (HPE: `ProLiant DL180 Gen9` for the 8SFF, 8LFF and 12LFF cages), take the SKUs from the
vendor's tables only, and cite them in the file. A SKU must be listed by one model only.

| Variant models (`library/trica/`) | SKUs |
|---|---|
| `hpe-proliant-dl360-gen9-8sff` | 755258-B21 |
| `hpe-proliant-dl380-gen9-8sff` / `-24sff` | 719064-B21 / 767032-B21 |
| `hpe-proliant-dl180-gen9-8sff` / `-8lff` / `-12lff` | 754523-B21, 833973-B21 / 754524-B21, 833971-B21, 833972-B21 / 775506-B21, 833974-B21 |
| `dell-poweredge-r330-8sff` / `-4lff-hot-plug` / `-4lff-cabled` | none: Dell reports no chassis part number (the Redfish `SKU` is the service tag); a device reporting `PowerEdge R330` gets the upstream model, without picture. To show your chassis, copy the PNG of your variant to `elevation-images/Dell/dell-poweredge-r330.front.png` (and `.rear.png`) in your directory, see below |

## Face images

A model may have an image of each face. Decision: [ADR 0042](../adr/0042-device-type-images.md).

- **Where.** `elevation-images/<manufacturer>/<slug>.<face>.<ext>`, the upstream convention:
  `<manufacturer>` and `<slug>` exactly as in the model file, `<face>` `front` or `rear`, `<ext>`
  `png`, `jpg`, `jpeg` or `webp` (in that order of preference when there are several). SVG is
  refused: served from the application's origin it could carry script. At most 2 MiB a file; the
  content must be what the extension says.
- **The `front_image` / `rear_image` keys** of a model file are booleans upstream ("upstream has
  an image"). They must be booleans; they are not trusted: the file is looked up whatever they
  say, so an upstream file that says `true` for an image we do not vendor simply has none, and a
  photograph added without editing the model file is used.
- **Served by** `GET /api/v1/device-types/{slug}/{front|rear}` (read right, bearer token like
  every API call; `Cache-Control: private, max-age=3600` and a strong `ETag`, `304` on
  `If-None-Match`). `GET /racks/{id}/contents` names the paths that exist (`front_image`,
  `rear_image`) beside the device's `role`, `health` and `parts_health`.
- **Shown** on the rack elevation scaled to the device's U box; a device whose model has no image
  of that face gets a generic face drawn for its role ([UI reference](ui.md#racks-and-rooms-what-the-drawing-relies-on)).
- **Airflow families.** The base model synthesised from `-F` / `-R` variants shows the images of
  its first variant (`-F`).

Embedded images and their licences are listed in `internal/devicetypes/library/README.md`.
Faces upstream has no picture of are **drawn by the project** ([ADR 0043](../adr/0043-device-type-variants-and-drawn-faces.md)):
schematic, vendor-neutral, from the vendors' published counts and layouts, CC0-1.0. Their SVG
sources are in `library/drawings/`, the generator in `scripts/devicetype-faces/`
(`node scripts/devicetype-faces/generate.mjs --png`, in the Playwright image the e2e suite
pins); such an image reports the origin `trica` even for an upstream model.

## Where the parts sit

A face image may have a **layout**: `layouts/<manufacturer>/<slug>.<face>.json`, the places of the
parts on it. Decision: [ADR 0047](../adr/0047-parts-placed-on-device-faces.md). The object page of a
device draws them with the parts the collectors report (`GET /objects/{id}/faces`).

```json
{
  "width_mm": 482.6, "height_mm": 44.45,
  "spots": [
    {"kind": "bay", "name": "1:1", "x": 29.35, "y": 2.4, "w": 70.2, "h": 19.4},
    {"kind": "psu", "name": "1",   "x": 335.15, "y": 2.2, "w": 60, "h": 39.6}
  ]
}
```

- `width_mm` and `height_mm` are the size of the whole image in the units of the spots
  (millimetres for the project's drawings; for a photograph, its pixels work as well). A spot's
  box is from the top left of the image and must be inside it; 1 to 512 spots, at most 256 KiB,
  no other field, no two spots of one kind with the same name.
- `kind` is `bay`, `psu`, `fan`, `port` or `slot`; `name` is how the device numbers that place,
  which is what the rules below read.
- The project's layouts are generated with its drawings (`scripts/devicetype-faces`). Upstream
  photographs have none; the object page shows them without spots.

| Part | Goes in | From |
|---|---|---|
| `disk` | `bay` `<box>:<bay>` | its `slot`, then its name: `Port=1I:Box=3:Bay=5` (iLO 5), `1I:1:3` (iLO 4), `3:5` |
| `disk` | `bay` `<n>` | `Solid State Disk 0:1:2` (iDRAC, last number), `Disk.Bay.2` (iDRAC FQDD), `Bay 2` |
| `psu` | `psu` `<n>` | the last number of its name, plus one when one of the device's power supplies is numbered 0 (iLO 5 counts from 0, the chassis from 1) |
| `fan` | `fan` `<n>` | the first number of its name; only fans directly under the device (a power supply's fan is not on the face) |
| `interface` (kind empty, `physical` or `bmc`) | `port` | its own name (`Ethernet12`, `ether3`), then without a breakout lane (`Ethernet49/1` → `Ethernet49`), `bmc` for the BMC, `nic<n>` for `eno<n>`, `NIC.Embedded.<n>-…`, `NIC.Integrated.1-<n>…` |
| `interface` | `slot` `<n>` | `ens<n>` (and `ens<n>d<m>`), `NIC.Slot.<n>-…` |

A name none of these read leaves the part **unplaced**: listed under the drawing, never put in a
place by count. In an operator directory, a layout goes in `layouts/` like the embedded ones; an
operator image that replaces a face drops that face's embedded layout (its spots fit the drawing,
not the photograph) unless the directory has a layout for it too. A layout no model and image use
is logged at start and ignored.

## Your own models and images

Set `devicetypes.extra_dir` (or `TRICA_DEVICETYPES_EXTRA_DIR`) to a directory laid out like the
library; the server reads it at start, after the embedded library, and its files win:

```
/etc/trica/devicetypes/
├── acme/Shelf-2U.yaml                              # a model the library does not have
├── HPE/ProLiant-DL380-Gen9.yaml                    # replaces the embedded model with this slug
└── elevation-images/
    ├── HPE/hpe-proliant-dl380-gen9.front.jpg       # a photograph where the library has none
    └── Arista/arista-dcs-7050sx-64-r.front.png     # replaces the embedded drawing
└── layouts/
    └── HPE/hpe-proliant-dl380-gen9.front.json      # where the bays are on that photograph
```

| What you add | Effect |
|---|---|
| A model file (any directory but `elevation-images`) | Added to the library, origin `operator`; one with the slug of an embedded model replaces it and keeps that model's embedded images unless you give your own |
| An image for an existing slug and face | Replaces the embedded image of that face, or gives one to a model that had none |
| An image no model has (wrong manufacturer, slug or face, an unsupported type) | Ignored and logged at start (`device type library: operator directory`), the server starts |
| An invalid model file, a duplicate slug inside the directory, an image over 2 MiB or whose content is not its type | The library is not loaded (logged); the API and the rest of the server still run, without the library |
| A path that is not a directory | The server refuses to start (configuration error) |

Hidden entries (names starting with `.`) are skipped, so a Kubernetes volume, which holds its
files twice under `..<timestamp>/`, works as is. The directory is read once: restart to apply a
change. With the Helm chart, put the files on a PersistentVolumeClaim and set
`deviceTypes.existingClaim` (and `subPath`); it is mounted read-only at `/etc/trica/devicetypes`.
A photograph: shoot the face square on, crop to the chassis (ears included), 1000 pixels wide is
plenty; it is stretched to the U box, so a crop with the chassis' own proportions looks right.

## Not covered

The server runs `Sync` at start, after the migrations, then every `deduce.interval`
([configuration](configuration.md)). Module bays are ignored; no model is picked by hand in the
UI. Parts are not placed from HPE Redfish NIC names (`1`..`8`), a FlexibleLOM seen as `eno49`, or a
power supply's `BayNumber`; the Dell R330 variants and the DL180 Gen9 LFF fronts have no layout
reachable today (ADR 0047). Images are not reloaded without a restart, and the export PDF holds the images as part of its
picture, not as separate objects.
