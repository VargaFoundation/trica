# Device-type library

Embedded in the binary (`//go:embed library`). It is trica's own data, edited in place
([ADR 0063](../../../docs/adr/0063-the-device-type-library-is-tricas-data.md)); format and matching:
[ADR 0020](../../../docs/adr/0020-device-type-library.md); images: [ADR 0042](../../../docs/adr/0042-device-type-images.md);
operator's guide: [docs/reference/devicetypes.md](../../../docs/reference/devicetypes.md).

| Directory | What | Licence |
|---|---|---|
| `<Vendor>/` (`APC`, `Arista`, `Cisco`, `Dell`, `Eaton`, `Fortinet`, `Fujitsu`, `HPE`, `Juniper`, `Lenovo`, `MikroTik`, `Netgate`, `QNAP`, `Supermicro`, `Synology`, `Ubiquiti`) | Model files | CC0-1.0 (ADR 0063 says where they came from) |
| `trica/` | Models written from vendor documents (among them `DCS-7280SE-64-F` / `-R`, from the Arista 7280E data sheet; the chassis variants of the catalogue, ADR 0043; the NetApp DS4246 and DS2246 shelves; ADR 0056: `Synology-DS3617xs` from the [DS3617xs data sheet](https://cndl.synology.cn/download/Document/Hardware/DataSheet/DiskStation/17-year/DS3617xs/enu/Synology_DS3617xs_Data_Sheet_enu.pdf), `Ubiquiti-UA-Hub-Door` from the [UA-Hub-Door tech specs](https://techspecs.ui.com/unifi/door-access/ua-hub-door) and `Ubiquiti-UA-G2-Pro` from the [UA-G2-Pro tech specs](https://techspecs.ui.com/unifi/door-access/ua-g2-pro), all read on 2026-10-23, with the source in each file) | Apache-2.0 |
| `elevation-images/<Vendor>/` | Face images, `<slug>.<front\|rear>.<png\|jpg\|jpeg\|webp>`: pictures of the unit and the PNG of the faces the project drew | see below |
| `drawings/<Vendor>/` | SVG sources of the faces this project drew, `<slug>.<face>.svg` | **CC0-1.0**, see below |
| `layouts/<Vendor>/` | The spots of a drawn face, `<slug>.<face>.json` (ADR 0047) | Apache-2.0 |

Every model file is trica's: a change to a model is made in its file. The only third-party licence
the library carries is CC0-1.0, which attaches no condition.

## UniFi shortnames and port names

A UniFi controller reports a device by a shortname (`USPM24P` for a USW Pro Max 24 PoE), which is not
always the model's `part_number`. The 94 Ubiquiti models that need it list their shortnames in
`part_numbers` (126 in all), so that a device matches by SKU. The source is Ubiquiti's device
fingerprint database, <https://static.ui.com/fingerprint/ui/public.json> (700 devices, read
2026-10-23): the `shortnames` of the record whose `sku` is the model's `part_number`. Models of the
database that the library does not have are left out, so are the USW-Flex-2.5G family and the
CKG2-RM, which it does not list (the three Flex 2.5G models have no `part_numbers`: they are only
renamed, below).

The UniFi Network application and its Integration API number a device's ports 1..N and the
collector names them `Port N` (ADR 0060). A model's interfaces are named so only when its own names
or labels number every wired interface exactly once, from 1 to N (a label `Port 9 - WAN 1`, a name
`SFP+ 25`, `10G Port 9` or `Port 1 (PoE+)`): 40 models, whose interface keeps its former name as
`label`. A model whose numbering repeats or skips (SFP 1 after Port 24, an unnumbered MGMT, eth0,
WAN / LAN1) keeps the names its file gives: `internal/devicetypes/library_growth_test.go` lists each
of the 46 with what is missing (#177). The rule rests on an assumption nothing here proves: that the
`idx` of the Integration API is the number printed on the faceplate. The API documents `idx` as an
integer of at least 1 and says no more (Network API 10.6.106, read 2026-10-23), and the Ubiquiti
tech specs of the UCG-Max, UCG-Fiber, UXG-Pro, USW-Pro-Max-16, US-24 and USW-WAN-RJ45 do not number
ports (same day): see point 9 of `internal/collect/unifi/testdata/README.md`.

## Pictures (113 files)

Every picture of a unit below is under `elevation-images/`, licence **CC0-1.0**. CC0 waives every
right, modification included: each file was **scaled down to at most 1000 pixels wide** (Lanczos;
smaller ones kept at their size) and recompressed (PNG to a 256-colour palette, JPEG at quality 85
without its metadata), because a face is drawn 280 units wide and the originals weighed up to 1.2 MB.
Nothing else was changed, but the picture of the UDM Pro Max, a 1500 x 1500 render of the unit on a
white square with its reflection: it was also **cropped to the unit** (trimmed, then cut at the
bottom edge of the chassis), because the web stretches a face to the U box. The rear of the
CRS326-24G-2S+RM has no picture, its original carried red annotation boxes. History and provenance:
ADR 0063.

A model without a picture has the generic face of its role, or a face the project drew (next
section). The DL360 Gen9 picture shows the 10SFF build (8SFF plus the 2SFF kit in the media bay)
although its file names the 8SFF part number: a server reporting `755258-B21` gets
`hpe-proliant-dl360-gen9-8sff` and its drawing.

| File | Here |
|---|---|
| `HPE/hpe-proliant-dl360-gen9.front.jpg` | 1000 × 88, 29 118 B |
| `HPE/hpe-proliant-dl360-gen9.rear.jpg` | 1000 × 100, 35 033 B |
| `HPE/hpe-proliant-dl380-gen10.front.png` | 1000 × 178, 16 119 B |
| `HPE/hpe-proliant-dl380-gen10.rear.png` | 1000 × 180, 17 743 B |
| `Arista/arista-dcs-7050sx-64-f.front.png` | 1000 × 95, 70 523 B |
| `Arista/arista-dcs-7050sx-64-r.front.png` | 1000 × 95, 70 523 B |
| `Arista/arista-dcs-7050cx3-32s-f.front.png` | 1000 × 100, 79 867 B |
| `Arista/arista-dcs-7050cx3-32s-f.rear.png` | 1000 × 104, 82 400 B |
| `Arista/arista-dcs-7050qx-32s.front.png` | 1000 × 106, 77 989 B |
| `Arista/arista-dcs-7050sx3-48yc8-f.front.png` | 1000 × 102, 74 497 B |
| `Arista/arista-dcs-7050sx3-48yc8-f.rear.png` | 1000 × 104, 85 155 B |
| `Arista/arista-dcs-7050tx-48.front.png` | 1000 × 98, 54 729 B |
| `Arista/arista-dcs-7050tx-64.front.png` | 1000 × 99, 67 955 B |
| `Arista/arista-dcs-7060cx-32s-f.front.png` | 1000 × 113, 66 276 B |
| `Arista/arista-dcs-7060cx-32s-f.rear.png` | 1000 × 118, 76 763 B |
| `Arista/arista-dcs-7060cx-32s-r.front.png` | 1000 × 113, 66 276 B |
| `Arista/arista-dcs-7060cx-32s-r.rear.png` | 1000 × 100, 81 072 B |
| `Arista/arista-dcs-7280cr2-60-f.front.png` | 1000 × 199, 150 363 B |
| `Arista/arista-dcs-7280cr2-60-f.rear.png` | 1000 × 202, 152 690 B |
| `Arista/arista-dcs-7280cr3-32p4-f.front.png` | 1000 × 99, 76 531 B |
| `Arista/arista-dcs-7280cr3-32p4-f.rear.png` | 1000 × 102, 84 525 B |
| `Arista/arista-dcs-7280qr-c36-f.front.png` | 1000 × 113, 76 931 B |
| `Arista/arista-dcs-7280sr-48c6-f.front.png` | 1000 × 119, 78 095 B |
| `Arista/arista-dcs-7280sr-48c6-r.front.png` | 1000 × 119, 78 095 B |
| `Cisco/cisco-c9300-48p.front.png` | 1000 × 96, 19 518 B |
| `Cisco/cisco-c9300-48p.rear.png` | 1000 × 96, 32 057 B |
| `Cisco/cisco-c9300-48t.front.png` | 1000 × 96, 17 333 B |
| `Cisco/cisco-c9300-48t.rear.png` | 1000 × 96, 30 644 B |
| `Cisco/cisco-n9k-c9372px.front.png` | 1000 × 93, 20 138 B |
| `Cisco/cisco-n9k-c9372px.rear.png` | 1000 × 94, 28 419 B |
| `Cisco/cisco-ws-c2960x-24ts-l.front.png` | 1000 × 91, 24 239 B |
| `Cisco/cisco-ws-c2960x-48fps-l.front.png` | 1000 × 91, 21 422 B |
| `Cisco/cisco-ws-c2960x-48td-l.front.png` | 1000 × 91, 20 414 B |
| `Cisco/cisco-ws-c2960x-48ts-l.front.png` | 1000 × 91, 21 220 B |
| `Dell/dell-poweredge-r430.front.jpg` | 462 × 40, 6 024 B |
| `Dell/dell-poweredge-r430.rear.jpg` | 464 × 41, 9 315 B |
| `Dell/dell-poweredge-r440.front.png` | 850 × 71, 46 219 B |
| `Dell/dell-poweredge-r440.rear.png` | 1000 × 101, 58 977 B |
| `Dell/dell-poweredge-r530.front.jpg` | 1000 × 198, 35 811 B |
| `Dell/dell-poweredge-r530.rear.jpg` | 1000 × 203, 54 501 B |
| `Dell/dell-poweredge-r540.front.jpg` | 1000 × 183, 47 198 B |
| `Dell/dell-poweredge-r540.rear.jpg` | 1000 × 197, 58 884 B |
| `Dell/dell-poweredge-r620.front.png` | 953 × 89, 3 265 B |
| `Dell/dell-poweredge-r620.rear.png` | 952 × 89, 4 374 B |
| `Dell/dell-poweredge-r630.front.png` | 882 × 80, 6 396 B |
| `Dell/dell-poweredge-r630.rear.png` | 887 × 89, 14 179 B |
| `Dell/dell-poweredge-r640.front.png` | 1000 × 91, 57 323 B |
| `Dell/dell-poweredge-r640.rear.png` | 1000 × 104, 66 661 B |
| `Dell/dell-poweredge-r6515.front.jpg` | 1000 × 89, 23 658 B |
| `Dell/dell-poweredge-r6515.rear.jpg` | 1000 × 98, 37 678 B |
| `Dell/dell-poweredge-r720.front.png` | 964 × 171, 4 236 B |
| `Dell/dell-poweredge-r720.rear.png` | 959 × 171, 6 093 B |
| `Dell/dell-poweredge-r730xd.front.png` | 925 × 168, 11 476 B |
| `Dell/dell-poweredge-r730xd.rear.png` | 915 × 180, 20 826 B |
| `Dell/dell-poweredge-r750.front.png` | 760 × 137, 7 534 B |
| `Dell/dell-poweredge-r750.rear.png` | 739 × 144, 15 033 B |
| `Dell/dell-poweredge-r7515.front.jpg` | 763 × 138, 29 070 B |
| `Dell/dell-poweredge-r7515.rear.jpg` | 730 × 144, 39 469 B |
| `Eaton/eaton-5px1500irt.front.jpeg` | 524 × 101, 14 533 B |
| `Eaton/eaton-5px1500irt.rear.jpg` | 834 × 164, 32 544 B |
| `Fortinet/fortinet-fg-100f.front.png` | 1000 × 92, 12 468 B |
| `HPE/hpe-aruba-2530-24g.front.png` | 1000 × 102, 16 992 B |
| `HPE/hpe-aruba-2530-48g.front.png` | 1000 × 102, 22 467 B |
| `HPE/hpe-aruba-2930f-24g-4sfpp.front.png` | 1000 × 103, 14 719 B |
| `HPE/hpe-aruba-2930f-48g-4sfpp.front.png` | 1000 × 103, 21 098 B |
| `HPE/hpe-proliant-dl20-gen9.front.jpg` | 524 × 53, 9 228 B |
| `HPE/hpe-proliant-dl360-gen10-plus.front.png` | 715 × 61, 35 325 B |
| `HPE/hpe-proliant-dl360-gen10-plus.rear.png` | 723 × 71, 39 471 B |
| `HPE/hpe-proliant-dl360-gen10.front.png` | 1000 × 90, 18 851 B |
| `HPE/hpe-proliant-dl360-gen10.rear.png` | 728 × 68, 38 752 B |
| `HPE/hpe-proliant-dl380e-gen8.front.png` | 1000 × 183, 22 784 B |
| `HPE/hpe-proliant-dl380e-gen8.rear.png` | 1000 × 183, 30 757 B |
| `HPE/hpe-proliant-dl380p-gen8.front.png` | 1000 × 183, 21 437 B |
| `HPE/hpe-proliant-dl380p-gen8.rear.png` | 1000 × 183, 33 040 B |
| `HPE/hpe-proliant-dl385-gen10-plus-v2.front.png` | 1000 × 187, 125 177 B |
| `HPE/hpe-proliant-dl385-gen10-plus-v2.rear.png` | 1000 × 203, 157 584 B |
| `Juniper/juniper-ex2200-48t-4g.front.jpg` | 793 × 79, 26 502 B |
| `Juniper/juniper-ex4300-48p.front.png` | 1000 × 102, 73 314 B |
| `Juniper/juniper-ex4300-48p.rear.png` | 1000 × 102, 69 803 B |
| `Juniper/juniper-qfx5100-48s-6q.front.jpg` | 1000 × 100, 44 188 B |
| `Juniper/juniper-qfx5100-48s-6q.rear.jpg` | 1000 × 100, 36 144 B |
| `Lenovo/lenovo-system-x3650-m5.front.png` | 737 × 133, 53 210 B |
| `Lenovo/lenovo-system-x3650-m5.rear.png` | 1000 × 195, 153 183 B |
| `Lenovo/lenovo-thinksystem-sr650.front.png` | 1000 × 180, 30 130 B |
| `Lenovo/lenovo-thinksystem-sr650.rear.png` | 1000 × 196, 55 564 B |
| `MikroTik/mikrotik-ccr1009-7g-1c-1s-plus.front.png` | 1000 × 92, 38 420 B |
| `MikroTik/mikrotik-ccr2004-16g-2s-plus.front.jpg` | 1000 × 93, 21 543 B |
| `MikroTik/mikrotik-ccr2004-16g-2s-plus.rear.jpg` | 1000 × 92, 17 071 B |
| `MikroTik/mikrotik-ccr2116-12g-4s-plus.front.png` | 1000 × 99, 31 811 B |
| `MikroTik/mikrotik-ccr2116-12g-4s-plus.rear.png` | 1000 × 96, 25 945 B |
| `MikroTik/mikrotik-crs317-1g-16s-plus-rm.front.png` | 1000 × 92, 37 917 B |
| `MikroTik/mikrotik-crs326-24g-2s-plus-rm.front.jpg` | 1000 × 91, 23 063 B |
| `MikroTik/mikrotik-crs328-24p-4s-plus-rm.front.png` | 1000 × 87, 26 990 B |
| `Supermicro/supermicro-sys-6029p-tr.front.png` | 1000 × 187, 26 869 B |
| `Supermicro/supermicro-sys-6029p-tr.rear.png` | 1000 × 187, 27 208 B |
| `Synology/synology-rs1221-plus.front.jpeg` | 928 × 176, 36 021 B |
| `Synology/synology-rs3621xs-plus.front.jpg` | 1000 × 184, 29 217 B |
| `Synology/synology-rs3621xs-plus.rear.jpg` | 1000 × 205, 53 435 B |
| `Ubiquiti/ubiquiti-unifi-switch-24-pro.front.png` | 1000 × 102, 55 324 B |
| `Ubiquiti/ubiquiti-unifi-switch-24-pro.rear.png` | 1000 × 100, 24 169 B |
| `Ubiquiti/ubiquiti-unifi-switch-48-pro-poe-gen2.front.png` | 1000 × 98, 49 948 B |
| `Ubiquiti/ubiquiti-unifi-switch-48-pro-poe-gen2.rear.png` | 1000 × 98, 25 203 B |
| `Ubiquiti/ubiquiti-unifi-switch-aggregation.front.png` | 1000 × 99, 35 396 B |
| `Ubiquiti/ubiquiti-enterprise-fortress-gateway.front.png` | 1000 × 99, 32 719 B |
| `Ubiquiti/ubiquiti-enterprise-fortress-gateway.rear.png` | 1000 × 100, 44 108 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro-max.front.png` | 1000 × 99, 36 545 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro-max.rear.png` | 1000 × 99, 35 005 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro-special-edition.front.png` | 1000 × 99, 35 105 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro-special-edition.rear.png` | 1000 × 100, 36 528 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro.front.png` | 1000 × 100, 41 993 B |
| `Ubiquiti/ubiquiti-unifi-dream-machine-pro.rear.png` | 1000 × 102, 42 378 B |
| `Ubiquiti/ubiquiti-unifi-switch-pro-max-24-poe.front.png` | 1000 × 100, 44 017 B |
| `Ubiquiti/ubiquiti-unifi-switch-pro-max-24-poe.rear.png` | 1000 × 100, 33 581 B |

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
be used anywhere. The model files beside them stay Apache-2.0.

| Model (slug) | Faces | Variant and facts from |
|---|---|---|
| `hpe-proliant-dl360-gen9-8sff` | front, rear | 8SFF CTO 755258-B21; HPE QuickSpecs DL360 Gen9 (c04346229): 8 SFF bays (1-8), media bay, power, health, NIC, UID, USB 3.0, pull tab; rear PCIe 1-3, 2 Flex Slot PSUs, VGA, 4x1GbE, iLO, serial, 2 USB 3.0, UID, FlexibleLOM |
| `hpe-proliant-dl380-gen9-8sff`, `-24sff` | front, rear | 8SFF CTO 719064-B21, 24SFF CTO 767032-B21; QuickSpecs DL380 Gen9 (c04346247): three 8SFF boxes, controls, USB 3.0; rear PCIe 1-6, rear 2 SFF option, 2 PSUs, I/O |
| `hpe-proliant-dl380-gen10-8sff`, `-8sff-nvme`, `-24sff`, `-24sff-nvme` (fronts), the six DL380 Gen10 chassis (rear) | front, rear | 8SFF CTO 868703-B21, 24SFF CTO 868704-B21, the 8 NVMe Express Bay kit 826689-B21 in Box 3; QuickSpecs DL380 Gen10 (a00008180enw): Box 1 (Universal Media Bay only there), Box 2, Box 3 (the 8SFF's primary cage), power, health, NIC, UID, iLO service port, USB 3.0, pull tag; rear risers 1-3 (slots 1-8), optional serial port, PSU bays 1 (far right) and 2, VGA, 4x1GbE, iLO, 2 USB 3.0, UID, FlexibleLOM. Left-to-right order of boxes and rear from the CC0 picture above |
| `hpe-proliant-dl180-gen9-8sff`, `-8lff`, `-12lff` | front, rear | CTO 754523-B21, 754524-B21, 775506-B21 (and pre-configured SKUs); QuickSpecs DL180 Gen9 (c04346227): box 1 media bay, box 2, box 3 8SFF; 8 (+4) or 12 LFF; USB 3.0 on the LFF ears; rear PCIe 1-6, 2x1GbE, VGA, 2 USB 3.0, UID, optional iLO, PSU 1-2. LFF bay positions schematic |
| `dell-poweredge-r330-8sff`, `-4lff-hot-plug`, `-4lff-cabled` | front, rear | Dell PowerEdge R330 Owner's Manual: the three chassis' front features (power, NMI, ID, VGA, LCD and buttons or five diagnostic LEDs, iDRAC USB, USB 2.0, information tag, slim optical), back panel (serial, vFlash, iDRAC port, 2 PCIe, PSU1-2, 2 USB 3.0, 2 NICs, ID button and connector, VGA). Positions schematic |
| `arista-dcs-7280se-64-f`, `-r` | front, rear | Arista 7280E data sheet: 48 SFP+, 4 QSFP+, 1 management, 1 RS-232 RJ-45, 1 USB, 2 PSUs, 4 fans; handles red (front-to-rear) or blue (rear-to-front) |
| `arista-dcs-7050sx-64-f`, `-r` (models with a picture at the front) | rear | Arista 7050SX data sheet: 2 PSUs (1+1), 4 fans (N+1), colour-coded |
| `mikrotik-crs354-48g-4s-plus-2q-plus-rm` | front, rear | MikroTik product page and user manual: 48 GbE, 4 SFP+, 2 QSFP+, RJ-45 console, 10/100 management, reset, 3 fans, 2 AC inputs, no display. Port grouping, management side and rear layout schematic |

### Catalogue faces

`scripts/devicetype-faces/catalogue.mjs` draws the catalogue's faces with parametric builders
(server front and rear, switch port and power sides, UPS, PDU, disk shelf), from counts only:

| Models | Faces | Facts from |
|---|---|---|
| Dell PowerEdge R230, R320, R340, R420, R430, R520, R620, R630, R640, R650, R720, R720xd, R730, R730xd, R740, R740xd chassis variants (`library/trica/Dell-PowerEdge-*`) | front, rear | Each system's Owner's Manual (`dl.dell.com/topicspdf/poweredge-<model>_owners-manual_en-us.pdf`; the R720 one covers the R720xd): drive bays per chassis, optical and LCD where listed, PCIe slots, power supplies, NICs, iDRAC, serial, VGA, USB |
| HPE ProLiant DL360p Gen8, DL380p Gen8, DL360 Gen9 4LFF, DL380 Gen9 12LFF, DL360 Gen10 chassis variants; DL380 Gen10 8LFF and 12LFF (fronts only) | front, rear | QuickSpecs c04128242, c04128241, c04346229, c04346247, a00008159enw, a00008180enw: CTO table (drive cage, SKUs), front and rear legends |
| Cisco WS-C3750X-24T-S, -48P-S, WS-C3850-24T-S, -48T-S, -48P-S, C9300-24T, N3K-C3048TP-1GE, N3K-C3064PQ-10GX; Juniper EX2200-24T-4G, EX3300-48T, EX4300-48T; MikroTik CRS326-24S+2Q+RM, CRS312-4C+8XG-RM, CRS354-48P-4S+2Q+RM; Ubiquiti USW-24, USW-48, ES-48-LITE; Fortinet FG-200F, FG-600E | port side, power side | Port counts and groups: the model file (from the vendor's data sheet); supplies, fans and stacking ports: the vendor's data sheet or hardware guide |
| Rears of pictures that have only a front: Cisco WS-C2960X (4), Arista DCS-7050QX-32S, 7050TX-48, 7050TX-64, 7280SR-48C6-F/-R, Juniper EX2200-48T-4G, MikroTik CRS328-24P-4S+RM, Ubiquiti USW-Aggregation, Aruba 2530/2930F (4), FortiGate 100F, Synology RS1221+ | power side or rear | The same documents: fixed or modular supplies, fans |
| Synology RS820+, RS2421+; QNAP TS-1263U-RP, TS-863U-RP; NetApp DS4246, DS2246 | front, rear | Hardware installation guides: drive bays, LAN ports, supplies; NetApp: 24 drives, 4 or 2 PSUs, two IOM6 |
| APC SMT1500RMI2U, SMT3000RMI2U, SMX1500RMI2U, SRT3000RMXLI, AP7920, AP7921, AP7900B; Eaton 5PX3000IRT2U | front, rear | Outlets, input and communication ports: the model file and the user manuals |

Positions the documents do not give are the builders', the same for every model of a kind, and
no catalogue face has a layout. The PNG are rendered at 1000 pixels wide, then reduced to a
palette by pngquant (the generator does it when pngquant is installed; the drawings of the table
above were reduced too).

Each drawing has its **layout** in `layouts/<manufacturer>/<slug>.<face>.json`, written by the same
generator: where its bays, power supplies, fans, ports and slots are, so that the object page puts
the parts a collector reports in their places (ADR 0047, format in `docs/reference/devicetypes.md`).
The DL180 Gen9 and DL380 Gen10 LFF fronts have no layout: their bay positions are schematic.
The HPE faces of `faces.mjs` draw their drive cages with `driveCage` and `hotplugCarrier`
(`draw.mjs`): generic hot-plug carriers (status ring, label window, vent, lever, latch), so that a
face reads as hardware at the size the object page draws it.

To change a drawing, edit `scripts/devicetype-faces/faces.mjs` (or `draw.mjs`) and regenerate
the SVG, the PNG and the layout; `go test ./internal/devicetypes/` checks that every drawing has its PNG
with the right size and model.

**Not used, on purpose:** VisioCafe stencils. Their licence forbids redistribution and any use on
a web service, which is what an embedded image served by the API is.

## Adding an image

Drop `elevation-images/<manufacturer>/<slug>.<face>.<ext>` here, with `<manufacturer>` and
`<slug>` exactly as in the model file; add a row to the pictures table with its licence. An image
that matches no model fails the load (`go test ./internal/devicetypes/`). An image drawn by the
project has its SVG source in `drawings/` under the same name, which marks it drawn (`Image.Drawn`); an image without a drawing is a picture. Only images whose
licence allows redistribution and use in a web application belong here; a photograph of one's
own equipment belongs in the operator's directory (`devicetypes.extra_dir`), not in the binary.
