# Redfish fixtures

**These fixtures are synthesised.** No document here was captured from a real
iLO or iDRAC. They were written by hand from the Redfish schemas (DSP2046,
DSP0266) and from what HPE and Dell document for each BMC generation, then
anonymised by construction:

- serial numbers and UUIDs are obviously fake (`SYNTH-...`, `...5555555500a1`);
- MAC addresses are locally administered (`02:00:5e:...`);
- IP addresses are in the documentation ranges `192.0.2.0/24`,
  `198.51.100.0/24`, `203.0.113.0/24`;
- host names are `srv-NN` or under `example.invalid`;
- the password the fake accepts is a constant in `fake.go`, not a secret.

They prove that the collector handles the *shapes* described below. They do not
prove that a real firmware answers with those shapes.

## Format

One JSON file per device: an object mapping the resource path (no trailing
slash) to the document served there. A paged resource is keyed with its query
(`".../Entries?page=2"`). Timestamps are templates expanded when the fake
starts: `{{now-20d}}`, `{{now-12h}}`, `{{now-2d@-05:00}}` (with a UTC offset),
relative to `FakeNow`, so the fixtures never age past a collector's look-back
window.

## What each file models

| File | Device | What it exercises |
|------|--------|-------------------|
| `ilo4-dl360g9.json` | HPE ProLiant DL360 Gen9, iLO 4 firmware 2.7x | `Oem.Hp` spelling; no standard `Storage`, disks under `Oem.Hp.Links.SmartStorage` (ArrayControllers, PhysicalDrives, `Location` as a string, `SSDEnduranceUtilizationPercentage` at top level, padded serial); firmware inventory under the system as one document keyed by category (`Current`); `TotalSystemMemoryGB`; DIMM `SizeMB`, one `NotPresent`; fans with `FanName`/`CurrentReading`/`Units`, one absent; two supplies with the same name; `AverageConsumedWatts` only; IML in two pages (`?page=2`) with a critical outside the window and one entry without a date; manager with a disabled second NIC (0.0.0.0); an IEL service that must never be read. |
| `ilo5-dl380g10.json` | HPE ProLiant DL380 Gen10, iLO 5 firmware 2.x | `Oem.Hpe` spelling; standard `Memory` and `Storage` (with the OEM SmartStorage link also present: it must **not** be requested); `Drives` with `Location` as a list and `Oem.Hpe.SSDEnduranceUtilizationPercentage`; an absent bay; fans in percent and rpm; `UpdateService/FirmwareInventory` fetched member by member, one without a version; IML single page; manager with a second enabled NIC without an address. |
| `idrac8-r330.json` | Dell PowerEdge R330, iDRAC 8 firmware 2.8x | Paths without trailing slash and ids with dots and colons (`System.Embedded.1`, `Disk.Bay.0:Enclosure.Internal.0-1:RAID.Integrated.1-1`); `PredictedMediaLifeLeftPercent`; drives identified by NAA; temperature sensors **without** `PhysicalContext` (placement from the name); an absent power supply and DIMM; firmware `Installed-*` and a `Previous-*` (`StandbyOffline`) entry that must be ignored; SEL with `Members@odata.nextLink` using `?$skip=` and a timestamp with a UTC offset; a Lifecycle log that must never be read. |

The fake (`../fake.go`) additionally simulates, per test: refused credentials,
one failing sub-request (`FailPath`), a malformed body (`MalformPath`), a slow
response (`SlowPath`), and edits of any document (`Set`, `Edit`, `Delete`).
Every request is recorded; the tests assert that only `GET` was sent.

## What to check on real equipment before trusting a field

These are the places where a synthesised fixture is most likely to differ from
a real BMC. Compare each against the web interface of one canary host.

1. **iLO 4 `LinkStatus` and host interface speed.** The fixture uses
   `LinkUp`/`NoLink`; some iLO 4 firmware writes `Up`/`Down` or leaves the
   field out (the collector accepts all of these, but the field may be absent).
2. **iLO 4 `Oem.Hp.Links.Memory` and SmartStorage layout.** Paths, the
   `ArrayControllers`/`PhysicalDrives` link names, `Location` format and the
   absence of DIMM serials are from documentation, not captures.
3. **iLO 4 `FirmwareInventory` `Current` shape.** Modelled as
   `{"Current": {"<category>": [{"Name", "VersionString"}, ...]}}`; confirm the
   real document and whether it sits under the system or under `UpdateService`.
4. **iDRAC 8 `Storage`/`Drives` paths** and whether
   `PredictedMediaLifeLeftPercent` is populated on the firmware in use (it
   needs a supported SSD; otherwise `wear_pct` is simply absent).
5. **`TotalSystemMemoryGB` meaning.** HPE writes GB for what is GiB; the
   collector treats both spellings as GiB. Check the figure against the
   installed DIMMs.
6. **Dell `Installed-`/`Previous-` firmware entries.** The `Previous-` entries
   are dropped by id prefix or `StandbyOffline` state; confirm which of the two
   your firmware uses.
7. **Serial/UUID byte order.** Some BMCs and the OS (`dmidecode`) present the
   system UUID with different byte order. The collector stores what the BMC
   says; if the same host is also discovered by another source with another
   byte order, the two will not merge on `hw_uuid` alone.
8. **Severity vocabulary of the event logs.** The counters match `Critical` and
   `Warning` (case-insensitive). Check that your IML/SEL use those words in
   `Severity` and not only in `Message`.
9. **Which BMC interface answers.** The one carrying the first usable IPv4 is
   named `bmc`; on a server with several, confirm that this is the one the
   inventory should call the management port.
