# ADR-0057: UniFi consoles and devices come from the Site Manager cloud API, read-only, with the personal data never decoded

- **Status**: accepted (2026-10)
- **Concerns**: `internal/collect/unifi` (new), `internal/collect/all`, `internal/schema/builtin.yaml`
  (`device.role`, `managed_by`), `web/src/inventory/kind.ts` and the web catalogues,
  `docs/reference/collectors.md`, `charts/trica/README.md`. Builds on ADR 0005 (single writer),
  ADR 0026 (collector runner), ADR 0036 (identity scope), ADR 0039 (sites are declared) and
  ADR 0055 (a module describes itself). `pkg/sdk` is untouched.

## Context

An office runs a UniFi estate: one Dream Machine Pro Max console, six access points, a switch, a
door hub and a reader. The operator has a Site Manager API key and asked for everything to be
brought into trica.

What the cloud API (`https://api.ui.com`, `GET /v1/hosts`, `/v1/sites`, `/v1/devices`) offers, as
recorded on 2026-10-23:

- A **controller's view from the cloud**, with no LAN reach: hardware, versions, state, the
  console's WAN ports and addresses, the applications it runs, the devices it has adopted and their
  state. Not ports, uplinks, LLDP, VLANs or clients.
- A key that covers **every console of the account** that created it.
- **Personal data in the answers**: the account's e-mail, name, role and permissions, the
  console's coordinates and address text, its time zone and several identifiers. None of it is
  inventory.
- A device list whose `shortname` (`U7PRO`) and display `model` (`U7 Pro`) are what a device-type
  library matches on; a console that appears in both `/v1/hosts` and `/v1/devices` under the same
  MAC.
- A trica schema with no role for an access point, an access-control hub or a camera, and no relation
  for "adopted by a controller".

## Decision

1. **One module, `unifi`**, a target of the targets file, with `password` the API key (a `file:` or
   `env:` reference, sent in the `X-API-KEY` header and nowhere else), no `address` for
   `https://api.ui.com` (an override must be https), and two options: `hosts` (which consoles) and
   `roles` (overrides, `<shortname or model>=<role>`; a list of strings because the options
   reflection of ADR 0055 has no map kind).
2. **GET only, three endpoints**, redirects never followed, lists followed by `nextToken` (at most 50
   pages). `TestOnlyGETIsBuilt` parses the package and refuses any request that is not
   `http.MethodGet`, any `Post`/`Put`/`Patch`/`Delete`/`Head`, and any string naming `/connector/`,
   `/ea/` or `/proxy/`; the fake answers 405 to a write and records it. `/v1/hosts` is the probe: a
   refused key ends the run after one request.
3. **The decode structs are an allow-list.** The account (`userData`), the location, the time zone and
   the identifiers are not declared, so they cannot be read, mapped or logged by a later change that
   adds "one more field" (`TestDecodeStructsHaveNoPersonalFields`); the anonymised fixtures keep them
   as sentinels and `TestPersonalDataNeverEmitted` proves none reaches a batch. The key is removed
   from any server message before it reaches a health record (`TestKeyNeverLeaks`).
4. **A device per UniFi device**, identified by `base_mac` (strong; every entry has one) and `name`
   (weak). The console is the same device in the host and device lists and is merged by MAC; its
   `serial` is the host's `serialno`. `vendor` is `Ubiquiti`, `model` the display model,
   `part_number` the shortname: the device-type library is matched on those, and the library story
   owns the test that the five recorded pairs resolve.
5. **Three new device roles, `access_point`, `access_control` and `camera`, and one new relation,
   `managed_by` (device to device, no impact).** Without the roles nine of the ten devices of the
   office would be `other`. `depends_on` and `contains` would carry the wrong impact (an access
   point keeps serving when its console is down), `routes_via` would be a guess; so a relation of
   its own, with no impact, from each adopted device to its console. Roles come from ordered rules
   (`roleOf`), the display model read before the shortname because `USP...` is both a switch and a
   PDU; a model no rule knows is `other` with a warning that names `options.roles`.
6. **No `site` object, and the UniFi site is not stored.** A UniFi site is a namespace inside a console
   ("default"), not a place; people declare places (ADR 0039). Devices outside a rack cannot be in a
   trica site today: issue #161 proposes a fix.
7. **Addresses**: a device's address is an `ip_address` assigned to the device; the console's WAN side
   address to its WAN `interface`; its LAN addresses to the console; link-local ones are left out.
   They all go through the runner's domain stamp (ADR 0036), because private ranges repeat across
   offices. `mgmt_ip` is an attribute, never an identity: a weak address identity could merge a new
   device into another site's.
8. **The office's public address is not an `ip_address`**: the runner would stamp it with the office's
   domain. It is stored in `internet_access.public_ips` (a list attribute, not scoped), with the ISP
   name and the port state, one `internet_access` per enabled WAN, which the console `routes_via`.
   The coordinates, the location text and the time zone are never stored.
9. **Partial results stay partial.** A failing `/v1/sites` keeps the devices and drops the Internet
   accesses; a failing `/v1/devices` keeps the consoles from their host entries (without model or
   version, so those never flip between sources); a console disconnected from the cloud has its
   devices emitted without `health` and the batch is partial. Nothing is withdrawn by a partial
   batch.
10. **Deferred**: ISP metrics (early access, large, per site; they need series on `internet_access`:
    issue #162) and ports, uplinks and cabling through the console's local Network Integration API
    (issue #163). Both are follow-up stories; neither is attempted with synthesised-only fixtures.

## Consequences

- The first collector that reaches an Internet service: the server needs egress to `api.ui.com:443`
  (or `HTTPS_PROXY`), and a NetworkPolicy that restricts egress must allow it. The key reads the whole
  account: the documentation says to use the least role, a dedicated account and `options.hosts`.
- Two schema additions (three enum values and a relation) ripple into the OpenAPI-independent web
  catalogues (role labels in both languages, role icons); a device with a role the web does not know
  is still drawn with the generic icon.
- The recorded set is one console; pagination, error bodies, the update states and cameras are
  synthesised and listed as unverified in `testdata/README.md` and in STATUS.
- Uptime changes on every run (`uptime_s`, as for the other collectors): a second run with no change
  upstream writes nothing only because the test clock is fixed.
- A device replaced under RMA is a new device (a new MAC) and the old one becomes absent. An
  `internet_access` is named after the console's host name, so renaming the console creates new
  ones.
- A console's `mgmt_ip` is its WAN side address, because that is what the cloud reports; its LAN
  address is the second `ip_address` on the device.
