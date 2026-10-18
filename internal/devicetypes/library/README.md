# Device-type library

Embedded in the binary (`//go:embed library`). Format, matching and licence decision:
[ADR 0020](../../../docs/adr/0020-device-type-library.md); images: [ADR 0042](../../../docs/adr/0042-device-type-images.md);
operator's guide: [docs/reference/devicetypes.md](../../../docs/reference/devicetypes.md).

| Directory | What | Origin and licence |
|---|---|---|
| `<Vendor>/` (`Arista`, `Dell`, `HPE`, `MikroTik`, `Netgate`) | Model files, copied unchanged | [netbox-community/devicetype-library](https://github.com/netbox-community/devicetype-library), `device-types/<Vendor>/`, CC0-1.0 |
| `trica/` | Model files written by this project (among them `DCS-7280SE-64-F` / `-R`, from the Arista 7280E data sheet: upstream has the -68 and -72 only) | Apache-2.0, origin `trica` |
| `elevation-images/<Vendor>/` | Face images, `<slug>.<front\|rear>.<png\|jpg\|jpeg\|webp>` | see below |

`Arista/DCS-7050SX-64-F.yaml` and `-R.yaml` were copied at master `37f5f02`, the other upstream
model files at `565a9ba` (see `NOTICE`).

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

Upstream has no rear image of the 7050SX-64, and no image at all of the DL380 Gen9 or the
CRS354-48G-4S+2Q+RM; the DCS-7280SE-64 and the generic pfSense appliance are not upstream. Those
models are drawn with the generic face of their device's role.

**Not used, on purpose:** VisioCafe stencils. Their licence forbids redistribution and any use on
a web service, which is what an embedded image served by the API is.

## Adding an image

Drop `elevation-images/<manufacturer>/<slug>.<face>.<ext>` here, with `<manufacturer>` and
`<slug>` exactly as in the model file; add a row above with its origin and licence. An image
that matches no model fails the load (`go test ./internal/devicetypes/`). Only images whose
licence allows redistribution and use in a web application belong here; a photograph of one's
own equipment belongs in the operator's directory (`devicetypes.extra_dir`), not in the binary.
