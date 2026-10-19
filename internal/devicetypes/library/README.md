# Device-type library

Embedded in the binary (`//go:embed library`). Format, matching and licence decision:
[ADR 0020](../../../docs/adr/0020-device-type-library.md); images: [ADR 0042](../../../docs/adr/0042-device-type-images.md);
operator's guide: [docs/reference/devicetypes.md](../../../docs/reference/devicetypes.md).

| Directory | What | Origin and licence |
|---|---|---|
| `<Vendor>/` (`Arista`, `Dell`, `HPE`, `MikroTik`, `Netgate`) | Model files, copied unchanged | [netbox-community/devicetype-library](https://github.com/netbox-community/devicetype-library), `device-types/<Vendor>/`, CC0-1.0 |
| `trica/` | Model files written by this project (among them `DCS-7280SE-64-F` / `-R`, from the Arista 7280E data sheet: upstream has the -68 and -72 only; and the chassis variants picked by part number, ADR 0043) | Apache-2.0, origin `trica` |
| `elevation-images/<Vendor>/` | Face images, `<slug>.<front\|rear>.<png\|jpg\|jpeg\|webp>` | see below |
| `drawings/<Vendor>/` | SVG sources of the faces this project drew, `<slug>.<face>.svg` | **CC0-1.0**, see below |

`Arista/DCS-7050SX-64-F.yaml` and `-R.yaml` and `Dell/PowerEdge-R330.yaml` were copied at master
`37f5f02`, the other upstream model files at `565a9ba` (see `NOTICE`).

## Images

Every file below comes from `elevation-images/` of netbox-community/devicetype-library, master at
`37f5f02c27eedf60767594174598daec5b9e2bee`, licence **CC0-1.0** (checked on 2026-10-17 through the
GitHub licence API, `LICENSE.txt`). CC0 waives every right, modification included: each file was
**scaled down to 1000 pixels wide** (Lanczos) and recompressed — PNG to a 256-colour palette,
JPEG at quality 85 without its metadata — because a face is drawn 280 units wide and the
originals weighed up to 470 KB. Nothing else was changed.

| File | Model | Upstream size | Here |
|---|---|---|---|
| `HPE/hpe-proliant-dl360-gen9.front.jpg` | HPE ProLiant DL360 Gen9 | 1987 × 175, 71 476 B | 1000 × 88, 29 118 B |
| `HPE/hpe-proliant-dl360-gen9.rear.jpg` | HPE ProLiant DL360 Gen9 | 1992 × 200, 71 413 B | 1000 × 100, 35 033 B |
| `HPE/hpe-proliant-dl380-gen10.front.png` | HPE ProLiant DL380 Gen10 | 2615 × 465, 204 381 B | 1000 × 178, 16 119 B |
| `HPE/hpe-proliant-dl380-gen10.rear.png` | HPE ProLiant DL380 Gen10 | 3932 × 706, 473 395 B | 1000 × 180, 17 743 B |
| `Arista/arista-dcs-7050sx-64-f.front.png` | Arista DCS-7050SX-64-F | 2000 × 190, 400 881 B | 1000 × 95, 70 523 B |
| `Arista/arista-dcs-7050sx-64-r.front.png` | Arista DCS-7050SX-64-R | 2000 × 190, 400 881 B | 1000 × 95, 70 523 B |

Upstream has no rear image of the 7050SX-64, and no image at all of the DL380 Gen9, the DL180
Gen9, the R330 or the CRS354-48G-4S+2Q+RM; the DCS-7280SE-64 and the generic pfSense appliance are
not upstream. The project drew those faces (next section); the generic `hpe-proliant-dl380-gen9`
and `dell-poweredge-r330` models and the pfSense appliance keep the generic face of their role,
since their name does not say which chassis they are. The upstream DL360 Gen9 picture shows the
10SFF build (8SFF plus the 2SFF kit in the media bay) although its file names the 8SFF part
number: a server reporting `755258-B21` now gets `hpe-proliant-dl360-gen9-8sff` and its drawing.

## Faces drawn by the project

Every PNG below is an original drawing of the trica project, generated from its SVG source in
`drawings/` by `scripts/devicetype-faces/generate.mjs --png` (1000 px wide, the proportions of a
19-inch panel of the model's height; PNG rendered by the Chromium of the Playwright image the e2e
suite pins). The drawings are schematic and vendor-neutral: greys, no logo, no trademark (the
model name in small neutral text at most). They were **not** traced or derived from any vendor
photograph, marketing render or stencil (VisioCafe included): counts, grouping and order come from
the vendors' public text, listed per model below, and, for the left-to-right order of the
DL360 Gen9 rear and of the Arista faces, from the CC0 pictures above. Where the text does not give a
position, the drawing is schematic and the generator says so.

**Licence: CC0-1.0.** To the extent possible under law, the Varga Foundation and the trica
contributors waive all copyright and related rights to the drawings in `drawings/` and to the PNG
files of the table below (<https://creativecommons.org/publicdomain/zero/1.0/>), so that they can
be used anywhere, contributed upstream included. The model files beside them stay Apache-2.0.

| Model (slug) | Faces | Variant and facts from |
|---|---|---|
| `hpe-proliant-dl360-gen9-8sff` | front, rear | 8SFF CTO 755258-B21; HPE QuickSpecs DL360 Gen9 (c04346229): 8 SFF bays (1-8), media bay, power, health, NIC, UID, USB 3.0, pull tab; rear PCIe 1-3, 2 Flex Slot PSUs, VGA, 4x1GbE, iLO, serial, 2 USB 3.0, UID, FlexibleLOM |
| `hpe-proliant-dl380-gen9-8sff`, `-24sff` | front, rear | 8SFF CTO 719064-B21, 24SFF CTO 767032-B21; QuickSpecs DL380 Gen9 (c04346247): three 8SFF boxes, controls, USB 3.0; rear PCIe 1-6, rear 2 SFF option, 2 PSUs, I/O |
| `hpe-proliant-dl180-gen9-8sff`, `-8lff`, `-12lff` | front, rear | CTO 754523-B21, 754524-B21, 775506-B21 (and pre-configured SKUs); QuickSpecs DL180 Gen9 (c04346227): box 1 media bay, box 2, box 3 8SFF; 8 (+4) or 12 LFF; USB 3.0 on the LFF ears; rear PCIe 1-6, 2x1GbE, VGA, 2 USB 3.0, UID, optional iLO, PSU 1-2. LFF bay positions schematic |
| `dell-poweredge-r330-8sff`, `-4lff-hot-plug`, `-4lff-cabled` | front, rear | Dell PowerEdge R330 Owner's Manual: the three chassis' front features (power, NMI, ID, VGA, LCD and buttons or five diagnostic LEDs, iDRAC USB, USB 2.0, information tag, slim optical), back panel (serial, vFlash, iDRAC port, 2 PCIe, PSU1-2, 2 USB 3.0, 2 NICs, ID button and connector, VGA). Positions schematic |
| `arista-dcs-7280se-64-f`, `-r` | front, rear | Arista 7280E data sheet: 48 SFP+, 4 QSFP+, 1 management, 1 RS-232 RJ-45, 1 USB, 2 PSUs, 4 fans; handles red (front-to-rear) or blue (rear-to-front) |
| `arista-dcs-7050sx-64-f`, `-r` (upstream models) | rear | Arista 7050SX data sheet: 2 PSUs (1+1), 4 fans (N+1), colour-coded |
| `mikrotik-crs354-48g-4s-plus-2q-plus-rm` (upstream model) | front, rear | MikroTik product page and user manual: 48 GbE, 4 SFP+, 2 QSFP+, RJ-45 console, 10/100 management, reset, 3 fans, 2 AC inputs, no display. Port grouping, management side and rear layout schematic |

Each drawing has its **layout** in `layouts/<manufacturer>/<slug>.<face>.json`, written by the same
generator: where its bays, power supplies, fans, ports and slots are, so that the object page puts
the parts a collector reports in their places (ADR 0047, format in `docs/reference/devicetypes.md`).
The DL180 Gen9 LFF fronts have no layout: their bay positions are schematic.

To change a drawing, edit `scripts/devicetype-faces/faces.mjs` (or `draw.mjs`) and regenerate
the SVG, the PNG and the layout; `go test ./internal/devicetypes/` checks that every drawing has its PNG
with the right size and model.

**Not used, on purpose:** VisioCafe stencils. Their licence forbids redistribution and any use on
a web service, which is what an embedded image served by the API is.

## Adding an image

Drop `elevation-images/<manufacturer>/<slug>.<face>.<ext>` here, with `<manufacturer>` and
`<slug>` exactly as in the model file; add a row above with its origin and licence. An image
that matches no model fails the load (`go test ./internal/devicetypes/`). An image drawn by the
project has its SVG source in `drawings/` under the same name, which makes its origin `trica`. Only images whose
licence allows redistribution and use in a web application belong here; a photograph of one's
own equipment belongs in the operator's directory (`devicetypes.extra_dir`), not in the binary.
