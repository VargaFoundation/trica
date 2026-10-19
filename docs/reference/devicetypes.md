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
| Then the part number | `673648-B21` (the upstream part number of the DL360p Gen8) |
| Airflow variants | `DCS-7050SX3-48YC8` matches the `-F` model only if all variants agree on everything but airflow; airflow and part number are then left out |
| Ambiguous name, or a SKU listed by two models | matches nothing (the SKU then leaves the decision to the name) |
| No vendor | matches nothing |
| Another source declared the device's model (a person who chose the chassis, a declaration file) | the library claims no `instance_of` for that device and withdraws its own; it still describes the declared model ([ADR 0048](../adr/0048-choosing-the-chassis-variant.md)) |

## How sure a match is, and the variants a person can choose

`Index.Choices` (behind `GET /api/v1/objects/{id}/device-type-candidates`) says whether the model
is the exact chassis and lists the candidates ([ADR 0048](../adr/0048-choosing-the-chassis-variant.md)).

| `reason` | Certain | When | Example |
|---|---|---|---|
| `declared` | yes | another source declared the model (it wins over the name) | a person chose `dell-poweredge-r330-8sff` |
| `sku` | yes | a part number listed by one model | `ProLiant DL380 Gen9` + `767032-B21` |
| `model`, `mounting`, `part_number` | yes | the matched model has no other candidate, or is itself a variant | `PowerEdge R640`; `PowerEdge R330 8 x 2.5in hot-plug` |
| `base` | no | the name matched a model that has variants | `PowerEdge R330` (the generic model, three chassis); `ProLiant DL380 Gen9` with an unlisted SKU |
| `airflow` | no | the name matched an airflow family's base | `DCS-7050SX3-48YC8` |
| `ambiguous` | no | nothing matched, models fit the name | `ProLiant DL180 Gen9` (three chassis, no generic model) |
| `none` | no | nothing matched or fits | |

The **candidates** are the models of the vendor whose name starts, word for word, with the name
the device reports, the match itself, and the models `variant_of` links to any of them (when
`variant_of` curates the family, a model found by its name alone that it does not link is left
out, see "Catalogue"); a name that fits more than 12 models offers none. `variant_of: <slug>` (a trica extension, optional)
names the generic model a file is a chassis variant of, for a variant whose name does not extend
the generic one's; its value must be a slug other than the file's own.

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
| `hpe-proliant-dl360-gen9-4lff` / `hpe-proliant-dl380-gen9-12lff` | 755259-B21 / 719061-B21 |
| `hpe-proliant-dl360p-gen8-4lff` / `-8sff` / `-10sff` | 655651-B21 / 654081-B21 / 666532-B21 |
| `hpe-proliant-dl380p-gen8-8lff` / `-8sff` / `-12lff` / `-25sff` | 665553-B21 / 653200-B21 / 665552-B21 / 665554-B21 |
| `hpe-proliant-dl360-gen10-4lff` / `-8sff` | 867958-B21, P19765-B21 / 867959-B21, P19766-B21, P56949-B21, P56950-B21 |
| `hpe-proliant-dl380-gen10-8lff` / `-12lff` / `-8sff` / `-24sff` | 868706-B21, P19717-B21 / 868705-B21, P19718-B21 / 868703-B21, P19720-B21, P56969-B21, P56970-B21 / 868704-B21, P19719-B21 |
| The Dell variants of the catalogue (below) | none, like the R330: the page offers the chassis to choose from |
| `dell-poweredge-r330-8sff` / `-4lff-hot-plug` / `-4lff-cabled` | none: Dell reports no chassis part number (the Redfish `SKU` is the service tag); a device reporting `PowerEdge R330` gets the upstream model, without picture, and its page says the variant is not certain and offers the three chassis to choose from ([ADR 0048](../adr/0048-choosing-the-chassis-variant.md)). For a whole fleet of one chassis, copy the PNG of your variant to `elevation-images/Dell/dell-poweredge-r330.front.png` (and `.rear.png`) in your directory instead, see below |

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
| `psu` | `psu` `<n>` | the last number of its name, plus one when one of the device's power supplies is numbered 0 (iLO 5 counts from 0, the chassis from 1: `HpeServerPowerSupply 0` and `1` are bays 1 and 2; iLO 4 writes `HpServerPowerSupply 1` and `2`) |
| `fan` | `fan` `<n>` | the first number of its name; only fans directly under the device (a power supply's fan is not on the face) |
| `interface` (kind empty, `physical` or `bmc`) | `port` | its own name (`Ethernet12`, `ether3`), then without a breakout lane (`Ethernet49/1` → `Ethernet49`), `bmc` for the BMC, `nic<n>` for `eno<n>`, `NIC.Embedded.<n>-…`, `NIC.Integrated.1-<n>…` |
| `interface` | `slot` `<n>` | `ens<n>` (and `ens<n>d<m>`), `NIC.Slot.<n>-…` |

**One physical part, several names.** The BMC and the operating system report the same disk and
the same port under two names (`3:1` and `nvme0n1`; iLO's `2` and `eno1`). A disk with the serial
number, or a port with the MAC address, of a placed part shares its spot: the spot lists both, and
its colour is the worst state of the parts that report one (a part that says nothing of its state
does not grey it out). When none of them is placed they are one line under the drawing, the others
given as "also seen as" (`also` in the answer). A controller module is never a part of a face: an
NVMe drive that iLO also reports as its own storage controller (`DA000000`) is counted once, as a
disk.

A name none of these read leaves the part **unplaced**: listed under the drawing, never put in a
place by count. The answer says why (`unplaced`), and the page groups them so:

| `unplaced` | Group on the page | Example |
|---|---|---|
| `os` | Known only to the operating system | `sda`, `nvme0n1`, `eno5`: the kernel's and systemd's names say nothing of the bay or the port; a SATA system disk on the embedded controller, which iLO does not list, stays here |
| `no_spot` | Not on this model's drawing | a drive iLO puts in `2:1` on an 8SFF chassis whose Box 2 is blank: the chassis may be another variant, chosen under "Device model" |
| `unread` | Names that do not say where | iLO's port `5` without a MAC address |

Each line gives a disk's model and capacity, which tells a system disk from a data disk at a glance. In an operator directory, a layout goes in `layouts/` like the embedded ones; an
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

## Catalogue

The embedded library is a **catalogue of the models people actually rack**: the servers,
switches, firewalls, storage and power that sell most on the refurbished and data-centre market,
each with its front and rear face where one could be had, and each chassis variant of a server as
its own model. 191 models; 160 have faces (101 NetBox photographs, 214 faces drawn by the project).

### How the models were chosen

Resellers' catalogues were used **only to pick which models are popular** (a model name is a
fact); none of their pictures or text was used. Servers: the configure-to-order listings of Bargain
Hardware ([bargainhardware.co.uk](https://www.bargainhardware.co.uk/), which refuses automated
reads, and its eBay shop, e.g. [R730xd 24SFF CTO](https://www.ebay.de/itm/146472017514),
[DL380 Gen10 16SFF CTO](https://www.ebay.de/itm/126728237360),
[DL360 Gen9 4LFF CTO](https://ebay.com/itm/126728121388)) and of ETB Technologies
([DL380 Gen10 12LFF](https://www.ebay.de/itm/286161397983)): Dell PowerEdge 12th to 15th
generation, HPE ProLiant Gen8 to Gen10, Lenovo, Supermicro and Fujitsu 1U/2U. Network: the
refurbished Cisco collections of [NetworkTigers](https://www.networktigers.com/collections/cisco-catalyst-campus-and-branch-access-switches-refurbished-used-new)
and [EPOKA](https://epoka.com/collections/cisco-switches) (Catalyst 2960-X, 3750-X, 3850, 9300,
Nexus), plus the families the project's users run (Arista 7050/7060/7280, Juniper EX and QFX,
MikroTik CRS, UniFi, Aruba 2530/2930F, FortiGate, Netgate). Storage and power: Synology and QNAP
rack units, NetApp DS4246/DS2246 shelves, APC Smart-UPS and AP79xx PDUs, Eaton 5PX.

### Where each face comes from

1. **NetBox photograph** — the model file and its picture from
   [netbox-community/devicetype-library](https://github.com/netbox-community/devicetype-library),
   CC0-1.0 (licence checked through the GitHub API on 2026-10-19), master at
   `9510b3cc3c9fca781ec09b61ef5ca2f3a00cdb39`, scaled to at most 1000 pixels wide and recompressed
   (PNG to a 256-colour palette, JPEG at quality 85); the list is in `library/README.md`.
2. **Drawn by the project** — where upstream has no picture of that exact model or chassis, a
   schematic face generated by `scripts/devicetype-faces/catalogue.mjs` from **facts only**: drive
   bay counts per chassis (Dell Owner's Manuals, HPE QuickSpecs), port counts and groups (the model
   file and the vendor's data sheet), power supplies, fans and slots. Never traced from a vendor
   photograph, render, stencil (VisioCafe included) or reseller picture. CC0-1.0. Catalogue faces
   carry **no layout**: their bay and port numbering is not taken from the vendors' text, so a
   device's parts are listed beside them, not placed. Exception: the DL380 Gen10 SFF fronts (8SFF,
   8SFF NVMe, 24SFF, 24SFF NVMe) and its rear, shared by its six chassis, are drawn by `faces.mjs` with their
   layouts, from the QuickSpecs' box and rear legends (ADR 0047, addendum).
3. **None** — the base model of a server whose chassis the name does not tell (`dell-poweredge-r730`,
   `hpe-proliant-dl380-gen9`...): its variants have the faces (next section).

### Chassis variants and families

A server sold in several chassis (4 x 3.5in, 8 x 2.5in, 24 x 2.5in...) has one model per chassis
in `library/trica/`, slug `<vendor>-<model>-<variant>`, with **`variant_of`** naming the generic
model (`variant_of: dell-poweredge-r730xd`, see "Matching" above): a device reporting the generic
name gets the generic model, marked not certain, and its page offers the chassis. A family curated
by `variant_of` is that family: a model found by its name alone that no variant links (the
`ProLiant DL380 Gen10 Plus` for a `ProLiant DL380 Gen10`) is not offered. `TestCatalogueFamilies`
checks that every `variant_of` names a model of the same manufacturer and height, that the
variant's slug extends it, and that every variant has both faces.

- **HPE**: each variant lists the configure-to-order SKUs of its chassis from the QuickSpecs CTO table
  (Network Choice and BC models included), so a server reporting one is matched `by sku`.
- **Dell**: no chassis part number exists (the Redfish `SKU` is the service tag): a device reporting
  `PowerEdge R730xd` gets the base model; the variant is picked by hand.
- **Not split** (one model, upstream picture or none): Lenovo, Supermicro, Fujitsu, the HPE Gen10 Plus,
  DL20, DL60 and DL385 servers.

### Size

Everything under `library/` is embedded in the binary. The catalogue brings it from 1.1 MB to
10.6 MB: 7.7 MB of face images (319 files, at most 1000 pixels wide and 256 KiB each), 2.5 MB of
SVG sources of the drawings (the loader reads them to tell a drawn face from an upstream one),
0.3 MB of model files. `TestCatalogueImagesFitTheBudget` holds the whole library under **15 MiB**;
past that, the plan is a catalogue pack outside the binary (the faces of the catalogue in a
directory loaded as `devicetypes.extra_dir`, or an OCI artifact), with the models of the project's
own racks kept embedded. Drawn PNG are reduced to a palette by `pngquant` in the generator
(19.4 MB as rendered, 3.3 MB stored).

### The list

Front and rear: **NetBox** = upstream CC0 photograph, **drawn** = schematic face drawn by the project, **—** = none. ↳ marks a chassis variant (`variant_of`).

**Servers** (115 models)

| Model | Slug | Front | Rear | SKUs (`part_numbers`) |
|---|---|---|---|---|
| Dell PowerEdge R220 | `dell-poweredge-r220` | — | — |  |
| Dell PowerEdge R230 | `dell-poweredge-r230` | — | — |  |
| ↳ Dell PowerEdge R230 4 x 3.5in cabled | `dell-poweredge-r230-4lff-cabled` | drawn | drawn |  |
| ↳ Dell PowerEdge R230 4 x 3.5in hot-plug | `dell-poweredge-r230-4lff-hot-plug` | drawn | drawn |  |
| Dell PowerEdge R320 | `dell-poweredge-r320` | — | — |  |
| ↳ Dell PowerEdge R320 4 x 3.5in hot-plug | `dell-poweredge-r320-4lff-hot-plug` | drawn | drawn |  |
| ↳ Dell PowerEdge R320 8 x 2.5in | `dell-poweredge-r320-8sff` | drawn | drawn |  |
| Dell PowerEdge R330 | `dell-poweredge-r330` | — | — |  |
| ↳ Dell PowerEdge R330 4 x 3.5in cabled | `dell-poweredge-r330-4lff-cabled` | drawn | drawn |  |
| ↳ Dell PowerEdge R330 4 x 3.5in hot-plug | `dell-poweredge-r330-4lff-hot-plug` | drawn | drawn |  |
| ↳ Dell PowerEdge R330 8 x 2.5in hot-plug | `dell-poweredge-r330-8sff` | drawn | drawn |  |
| Dell PowerEdge R340 | `dell-poweredge-r340` | — | — |  |
| ↳ Dell PowerEdge R340 4 x 3.5in hot-plug | `dell-poweredge-r340-4lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R340 8 x 2.5in | `dell-poweredge-r340-8sff` | drawn | drawn |  |
| Dell PowerEdge R420 | `dell-poweredge-r420` | — | — |  |
| ↳ Dell PowerEdge R420 4 x 3.5in hot-plug | `dell-poweredge-r420-4lff-hot-plug` | drawn | drawn |  |
| ↳ Dell PowerEdge R420 8 x 2.5in | `dell-poweredge-r420-8sff` | drawn | drawn |  |
| Dell PowerEdge R430 | `dell-poweredge-r430` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R430 10 x 2.5in | `dell-poweredge-r430-10sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R430 4 x 3.5in hot-plug | `dell-poweredge-r430-4lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R430 8 x 2.5in | `dell-poweredge-r430-8sff` | drawn | drawn |  |
| Dell PowerEdge R440 | `dell-poweredge-r440` | NetBox | NetBox |  |
| Dell PowerEdge R520 | `dell-poweredge-r520` | — | — |  |
| ↳ Dell PowerEdge R520 4 x 3.5in hot-plug | `dell-poweredge-r520-4lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R520 8 x 3.5in | `dell-poweredge-r520-8lff` | drawn | drawn |  |
| Dell PowerEdge R530 | `dell-poweredge-r530` | NetBox | NetBox |  |
| Dell PowerEdge R540 | `dell-poweredge-r540` | NetBox | NetBox |  |
| Dell PowerEdge R620 | `dell-poweredge-r620` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R620 10 x 2.5in | `dell-poweredge-r620-10sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R620 8 x 2.5in | `dell-poweredge-r620-8sff` | drawn | drawn |  |
| Dell PowerEdge R630 | `dell-poweredge-r630` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R630 10 x 2.5in | `dell-poweredge-r630-10sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R630 8 x 2.5in | `dell-poweredge-r630-8sff` | drawn | drawn |  |
| Dell PowerEdge R640 | `dell-poweredge-r640` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R640 10 x 2.5in | `dell-poweredge-r640-10sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R640 4 x 3.5in hot-plug | `dell-poweredge-r640-4lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R640 8 x 2.5in | `dell-poweredge-r640-8sff` | drawn | drawn |  |
| Dell PowerEdge R650 | `dell-poweredge-r650` | — | — |  |
| ↳ Dell PowerEdge R650 10 x 2.5in | `dell-poweredge-r650-10sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R650 4 x 3.5in hot-plug | `dell-poweredge-r650-4lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R650 8 x 2.5in | `dell-poweredge-r650-8sff` | drawn | drawn |  |
| Dell PowerEdge R6515 | `dell-poweredge-r6515` | NetBox | NetBox |  |
| Dell PowerEdge R720 | `dell-poweredge-r720` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R720 16 x 2.5in | `dell-poweredge-r720-16sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R720 8 x 3.5in | `dell-poweredge-r720-8lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R720 8 x 2.5in | `dell-poweredge-r720-8sff` | drawn | drawn |  |
| Dell PowerEdge R720xd | `dell-poweredge-r720xd` | — | — |  |
| ↳ Dell PowerEdge R720xd 12 x 3.5in | `dell-poweredge-r720xd-12lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R720xd 24 x 2.5in | `dell-poweredge-r720xd-24sff` | drawn | drawn |  |
| Dell PowerEdge R730 | `dell-poweredge-r730` | — | — |  |
| ↳ Dell PowerEdge R730 16 x 2.5in | `dell-poweredge-r730-16sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R730 8 x 3.5in | `dell-poweredge-r730-8lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R730 8 x 2.5in | `dell-poweredge-r730-8sff` | drawn | drawn |  |
| Dell PowerEdge R730xd | `dell-poweredge-r730xd` | NetBox | NetBox |  |
| ↳ Dell PowerEdge R730xd 12 x 3.5in | `dell-poweredge-r730xd-12lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R730xd 24 x 2.5in | `dell-poweredge-r730xd-24sff` | drawn | drawn |  |
| Dell PowerEdge R740 | `dell-poweredge-r740` | — | — |  |
| ↳ Dell PowerEdge R740 16 x 2.5in | `dell-poweredge-r740-16sff` | drawn | drawn |  |
| ↳ Dell PowerEdge R740 8 x 3.5in | `dell-poweredge-r740-8lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R740 8 x 2.5in | `dell-poweredge-r740-8sff` | drawn | drawn |  |
| Dell PowerEdge R740xd | `dell-poweredge-r740xd` | — | — |  |
| ↳ Dell PowerEdge R740xd 12 x 3.5in | `dell-poweredge-r740xd-12lff` | drawn | drawn |  |
| ↳ Dell PowerEdge R740xd 24 x 2.5in | `dell-poweredge-r740xd-24sff` | drawn | drawn |  |
| Dell PowerEdge R750 | `dell-poweredge-r750` | NetBox | NetBox |  |
| Dell PowerEdge R7515 | `dell-poweredge-r7515` | NetBox | NetBox |  |
| Fujitsu PRIMERGY RX2530 M4 | `fujitsu-primergy-rx2530-m4` | — | — |  |
| Fujitsu PRIMERGY RX2540 M4 | `fujitsu-primergy-rx2540-m4` | — | — |  |
| Fujitsu PRIMERGY RX2540 M5 | `fujitsu-primergy-rx2540-m5` | — | — |  |
| ↳ HPE ProLiant DL180 Gen9 12LFF | `hpe-proliant-dl180-gen9-12lff` | drawn | drawn | 775506-B21, 833974-B21 |
| ↳ HPE ProLiant DL180 Gen9 8LFF | `hpe-proliant-dl180-gen9-8lff` | drawn | drawn | 754524-B21, 833971-B21, 833972-B21 |
| ↳ HPE ProLiant DL180 Gen9 8SFF | `hpe-proliant-dl180-gen9-8sff` | drawn | drawn | 754523-B21, 833973-B21 |
| HPE ProLiant DL20 Gen10 | `hpe-proliant-dl20-gen10` | — | — |  |
| HPE ProLiant DL20 Gen9 | `hpe-proliant-dl20-gen9` | NetBox | — |  |
| HPE ProLiant DL360 Gen10 | `hpe-proliant-dl360-gen10` | NetBox | NetBox |  |
| ↳ HPE ProLiant DL360 Gen10 4LFF | `hpe-proliant-dl360-gen10-4lff` | drawn | drawn | 867958-B21, P19765-B21 |
| ↳ HPE ProLiant DL360 Gen10 8SFF | `hpe-proliant-dl360-gen10-8sff` | drawn | drawn | 867959-B21, P19766-B21, P56949-B21, P56950-B21 |
| HPE ProLiant DL360 Gen10 Plus | `hpe-proliant-dl360-gen10-plus` | NetBox | NetBox |  |
| HPE ProLiant DL360 Gen9 | `hpe-proliant-dl360-gen9` | NetBox | NetBox |  |
| ↳ HPE ProLiant DL360 Gen9 4LFF | `hpe-proliant-dl360-gen9-4lff` | drawn | drawn | 755259-B21 |
| ↳ HPE ProLiant DL360 Gen9 8SFF | `hpe-proliant-dl360-gen9-8sff` | drawn | drawn | 755258-B21 |
| HPE ProLiant DL360e Gen8 | `hpe-proliant-dl360e-gen8` | — | — |  |
| HPE ProLiant DL360p Gen8 | `hpe-proliant-dl360p-gen8` | — | — |  |
| ↳ HPE ProLiant DL360p Gen8 10SFF | `hpe-proliant-dl360p-gen8-10sff` | drawn | drawn | 666532-B21 |
| ↳ HPE ProLiant DL360p Gen8 4LFF | `hpe-proliant-dl360p-gen8-4lff` | drawn | drawn | 655651-B21 |
| ↳ HPE ProLiant DL360p Gen8 8SFF | `hpe-proliant-dl360p-gen8-8sff` | drawn | drawn | 654081-B21 |
| HPE ProLiant DL380 Gen10 | `hpe-proliant-dl380-gen10` | NetBox | NetBox |  |
| ↳ HPE ProLiant DL380 Gen10 12LFF | `hpe-proliant-dl380-gen10-12lff` | drawn | drawn | 868705-B21, P19718-B21 |
| ↳ HPE ProLiant DL380 Gen10 24SFF | `hpe-proliant-dl380-gen10-24sff` | drawn | drawn | 868704-B21, P19719-B21 |
| ↳↳ HPE ProLiant DL380 Gen10 24SFF NVMe | `hpe-proliant-dl380-gen10-24sff-nvme` | drawn | drawn | — (the 24SFF CTO with the 8 NVMe cage in Box 3; chosen on the device) |
| ↳ HPE ProLiant DL380 Gen10 8LFF | `hpe-proliant-dl380-gen10-8lff` | drawn | drawn | 868706-B21, P19717-B21 |
| ↳ HPE ProLiant DL380 Gen10 8SFF | `hpe-proliant-dl380-gen10-8sff` | drawn | drawn | 868703-B21, P19720-B21, P56969-B21, P56970-B21 |
| ↳↳ HPE ProLiant DL380 Gen10 8SFF NVMe | `hpe-proliant-dl380-gen10-8sff-nvme` | drawn | drawn | — (the 8SFF CTO with the 8 NVMe cage in Box 3; chosen on the device) |
| HPE ProLiant DL380 Gen10 Plus | `hpe-proliant-dl380-gen10-plus` | — | — |  |
| HPE ProLiant DL380 Gen9 | `hpe-proliant-dl380-gen9` | — | — |  |
| ↳ HPE ProLiant DL380 Gen9 12LFF | `hpe-proliant-dl380-gen9-12lff` | drawn | drawn | 719061-B21 |
| ↳ HPE ProLiant DL380 Gen9 24SFF | `hpe-proliant-dl380-gen9-24sff` | drawn | drawn | 767032-B21 |
| ↳ HPE ProLiant DL380 Gen9 8SFF | `hpe-proliant-dl380-gen9-8sff` | drawn | drawn | 719064-B21 |
| HPE ProLiant DL380e Gen8 | `hpe-proliant-dl380e-gen8` | NetBox | NetBox |  |
| HPE ProLiant DL380p Gen8 | `hpe-proliant-dl380p-gen8` | NetBox | NetBox |  |
| ↳ HPE ProLiant DL380p Gen8 12LFF | `hpe-proliant-dl380p-gen8-12lff` | drawn | drawn | 665552-B21 |
| ↳ HPE ProLiant DL380p Gen8 25SFF | `hpe-proliant-dl380p-gen8-25sff` | drawn | drawn | 665554-B21 |
| ↳ HPE ProLiant DL380p Gen8 8LFF | `hpe-proliant-dl380p-gen8-8lff` | drawn | drawn | 665553-B21 |
| ↳ HPE ProLiant DL380p Gen8 8SFF | `hpe-proliant-dl380p-gen8-8sff` | drawn | drawn | 653200-B21 |
| HPE ProLiant DL385 Gen10 | `hpe-proliant-dl385-gen10` | — | — |  |
| HPE ProLiant DL385 Gen10 Plus v2 | `hpe-proliant-dl385-gen10-plus-v2` | NetBox | NetBox |  |
| HPE ProLiant DL385p Gen8 | `hpe-proliant-dl385p-gen8` | — | — |  |
| HPE ProLiant DL60 Gen9 | `hpe-proliant-dl60-gen9` | — | — |  |
| Lenovo System x3550 M5 | `lenovo-system-x3550-m5` | — | — |  |
| Lenovo System x3650 M5 | `lenovo-system-x3650-m5` | NetBox | NetBox |  |
| Lenovo ThinkSystem SR630 | `lenovo-thinksystem-sr630` | — | — |  |
| Lenovo ThinkSystem SR650 | `lenovo-thinksystem-sr650` | NetBox | NetBox |  |
| Supermicro SuperChassis-CSE-846E16-R1200B | `supermicro-superchassis-cse-846e16-r1200b` | — | — |  |
| Supermicro SYS-1029P-WTRT | `supermicro-sys-1029p-wtrt` | — | — |  |
| Supermicro SuperServer 1029U-TR4T | `supermicro-sys-1029u-tr4t` | — | — |  |
| Supermicro SuperServer 2029U-E1CR25M | `supermicro-sys-2029u-e1cr25m` | — | — |  |
| Supermicro SuperServer 6029P-TR | `supermicro-sys-6029p-tr` | NetBox | NetBox |  |
| Supermicro SuperServer 6029U-TR4T+ | `supermicro-sys-6029u-tr4t-plus` | — | — |  |

**Network** (54 models)

| Model | Slug | Front | Rear | SKUs (`part_numbers`) |
|---|---|---|---|---|
| Arista DCS-7050CX3-32S-F | `arista-dcs-7050cx3-32s-f` | NetBox | NetBox |  |
| Arista DCS-7050QX-32S | `arista-dcs-7050qx-32s` | NetBox | drawn |  |
| Arista DCS-7050SX3-48YC8-F | `arista-dcs-7050sx3-48yc8-f` | NetBox | NetBox |  |
| Arista DCS-7050TX-48 | `arista-dcs-7050tx-48` | NetBox | drawn |  |
| Arista DCS-7050TX-64 | `arista-dcs-7050tx-64` | NetBox | drawn |  |
| Arista DCS-7060CX-32S-F | `arista-dcs-7060cx-32s-f` | NetBox | NetBox |  |
| Arista DCS-7060CX-32S-R | `arista-dcs-7060cx-32s-r` | NetBox | NetBox |  |
| Arista DCS-7280CR2-60-F | `arista-dcs-7280cr2-60-f` | NetBox | NetBox |  |
| Arista DCS-7280CR3-32P4-F | `arista-dcs-7280cr3-32p4-f` | NetBox | NetBox |  |
| Arista DCS-7280QR-C36-F | `arista-dcs-7280qr-c36-f` | NetBox | — |  |
| Arista DCS-7280SE-64-F | `arista-dcs-7280se-64-f` | drawn | drawn |  |
| Arista DCS-7280SE-64-R | `arista-dcs-7280se-64-r` | drawn | drawn |  |
| Arista DCS-7280SR-48C6-F | `arista-dcs-7280sr-48c6-f` | NetBox | drawn |  |
| Arista DCS-7280SR-48C6-R | `arista-dcs-7280sr-48c6-r` | NetBox | drawn |  |
| Cisco Catalyst 9300-24T | `cisco-c9300-24t` | drawn | drawn |  |
| Cisco Catalyst 9300-48P | `cisco-c9300-48p` | NetBox | NetBox |  |
| Cisco Catalyst 9300-48T | `cisco-c9300-48t` | NetBox | NetBox |  |
| Cisco Nexus 3048 | `cisco-n3k-c3048tp-1ge` | drawn | drawn |  |
| Cisco Nexus C3064PQ-10GX | `cisco-n3k-c3064pq-10gx` | drawn | drawn |  |
| Cisco Nexus 9372PX | `cisco-n9k-c9372px` | NetBox | NetBox |  |
| Cisco Catalyst 2960X-24TS-L | `cisco-ws-c2960x-24ts-l` | NetBox | drawn |  |
| Cisco Catalyst 2960X-48FPS-L | `cisco-ws-c2960x-48fps-l` | NetBox | drawn |  |
| Cisco Catalyst 2960X-48TD-L | `cisco-ws-c2960x-48td-l` | NetBox | drawn |  |
| Cisco Catalyst 2960X-48TS-L | `cisco-ws-c2960x-48ts-l` | NetBox | drawn |  |
| Cisco Catalyst 3750X-24T-S | `cisco-ws-c3750x-24t-s` | drawn | drawn |  |
| Cisco Catalyst 3750X-48P-S | `cisco-ws-c3750x-48p-s` | drawn | drawn |  |
| Cisco Catalyst 3850-24T-S | `cisco-ws-c3850-24t-s` | drawn | drawn |  |
| Cisco Catalyst 3850-48P IP Base | `cisco-ws-c3850-48p-s` | drawn | drawn |  |
| Cisco Catalyst 3850-48T IP Base | `cisco-ws-c3850-48t-s` | drawn | drawn |  |
| HPE Aruba 2530-24G | `hpe-aruba-2530-24g` | NetBox | drawn |  |
| HPE Aruba 2530-48G | `hpe-aruba-2530-48g` | NetBox | drawn |  |
| HPE Aruba 2930F-24G-4SFP+ | `hpe-aruba-2930f-24g-4sfpp` | NetBox | drawn |  |
| HPE Aruba 2930F-48G-4SFP+ | `hpe-aruba-2930f-48g-4sfpp` | NetBox | drawn |  |
| Juniper EX2200-24T-4G | `juniper-ex2200-24t-4g` | drawn | drawn |  |
| Juniper EX2200-48T-4G | `juniper-ex2200-48t-4g` | NetBox | drawn |  |
| Juniper EX3300-48T | `juniper-ex3300-48t` | drawn | drawn |  |
| Juniper EX4300-48P | `juniper-ex4300-48p` | NetBox | NetBox |  |
| Juniper EX4300-48T | `juniper-ex4300-48t` | drawn | drawn |  |
| Juniper QFX5100-48S-6Q | `juniper-qfx5100-48s-6q` | NetBox | NetBox |  |
| MikroTik CCR1009-7G-1C-1S+ | `mikrotik-ccr1009-7g-1c-1s-plus` | NetBox | — |  |
| MikroTik CCR2004-16G-2S+ | `mikrotik-ccr2004-16g-2s-plus` | NetBox | NetBox |  |
| MikroTik CCR2116-12G-4S+ | `mikrotik-ccr2116-12g-4s-plus` | NetBox | NetBox |  |
| MikroTik CRS312-4C+8XG-RM | `mikrotik-crs312-4c-plus-8xg-rm` | drawn | drawn |  |
| MikroTik CRS317-1G-16S+RM | `mikrotik-crs317-1g-16s-plus-rm` | NetBox | — |  |
| MikroTik CRS326-24G-2S+RM | `mikrotik-crs326-24g-2s-plus-rm` | NetBox | — |  |
| MikroTik CRS326-24S+2Q+RM | `mikrotik-crs326-24s-plus-2q-plus-rm` | drawn | drawn |  |
| MikroTik CRS328-24P-4S+RM | `mikrotik-crs328-24p-4s-plus-rm` | NetBox | drawn |  |
| MikroTik CRS354-48P-4S+2Q+RM | `mikrotik-crs354-48p-4s-plus-2q-plus-rm` | drawn | drawn |  |
| Ubiquiti EdgeSwitch 48 Lite | `ubiquiti-es-48-lite` | drawn | drawn |  |
| Ubiquiti UniFi Switch 24 Gen2 | `ubiquiti-unifi-switch-24-gen2` | drawn | drawn |  |
| Ubiquiti UniFi Switch 24 Pro | `ubiquiti-unifi-switch-24-pro` | NetBox | NetBox |  |
| Ubiquiti UniFi Switch 48 Gen2 | `ubiquiti-unifi-switch-48-gen2` | drawn | drawn |  |
| Ubiquiti UniFi Switch 48 Pro PoE Gen2 | `ubiquiti-unifi-switch-48-pro-poe-gen2` | NetBox | NetBox |  |
| Ubiquiti UniFi Switch Aggregation | `ubiquiti-unifi-switch-aggregation` | NetBox | drawn |  |

**Firewalls** (4 models)

| Model | Slug | Front | Rear | SKUs (`part_numbers`) |
|---|---|---|---|---|
| Fortinet FortiGate 100F | `fortinet-fg-100f` | NetBox | drawn |  |
| Fortinet FortiGate 200F | `fortinet-fg-200f` | drawn | drawn |  |
| Fortinet FortiGate 600E | `fortinet-fg-600e` | drawn | drawn |  |
| Generic Generic 1U pfSense appliance | `generic-1u-pfsense-appliance` | — | — |  |

**Storage** (8 models)

| Model | Slug | Front | Rear | SKUs (`part_numbers`) |
|---|---|---|---|---|
| NetApp DS2246 | `netapp-ds2246` | drawn | drawn |  |
| NetApp DS4246 | `netapp-ds4246` | drawn | drawn |  |
| QNAP TS-1263U-RP | `qnap-ts-1263u-rp` | drawn | drawn |  |
| QNAP TS-863U-RP | `qnap-ts-863u-rp` | drawn | drawn |  |
| Synology RS1221+ | `synology-rs1221-plus` | NetBox | drawn |  |
| Synology RS2421+ | `synology-rs2421-plus` | drawn | drawn |  |
| Synology RS3621xs+ | `synology-rs3621xs-plus` | NetBox | NetBox |  |
| Synology RS820+ | `synology-rs820-plus` | drawn | drawn |  |

**Power** (9 models)

| Model | Slug | Front | Rear | SKUs (`part_numbers`) |
|---|---|---|---|---|
| APC AP7900B | `apc-ap7900b` | drawn | drawn |  |
| APC AP7920 | `apc-ap7920` | drawn | drawn |  |
| APC AP7921 | `apc-ap7921` | drawn | drawn |  |
| APC Smart-UPS SRT 3000VA RM | `apc-smart-ups-srt-3000va-rm` | drawn | drawn |  |
| APC SMT1500RMI2U | `apc-smt1500rmi2u` | drawn | drawn |  |
| APC SMT3000RMI2U | `apc-smt3000rmi2u` | drawn | drawn |  |
| APC SMX1500RMI2U | `apc-smx1500rmi2u` | drawn | drawn |  |
| Eaton 5PX1500iRT | `eaton-5px1500irt` | NetBox | NetBox |  |
| Eaton 5PX3000IRT2U | `eaton-5px3000irt2u` | drawn | drawn |  |

## Not covered

The server runs `Sync` at start, after the migrations, then every `deduce.interval`
([configuration](configuration.md)). Module bays are ignored; a model is picked by hand one
device at a time (ADR 0048), not for many at once. Parts are not placed from HPE Redfish NIC names (`1`..`8`), a FlexibleLOM seen as `eno49`, or a
power supply's `BayNumber`; the Dell R330 variants and the DL180 Gen9 LFF fronts have no layout
reachable today (ADR 0047). Images are not reloaded without a restart, and the export PDF holds the images as part of its
picture, not as separate objects.
