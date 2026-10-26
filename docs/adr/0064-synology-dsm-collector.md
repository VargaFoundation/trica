# ADR-0064: A Synology collector over the DSM Web API

- **Status**: accepted (2026-10).
- **Concerns**: `internal/collect/synology` (new), `internal/collect/all`, `internal/wiring`
  (the kinds test), `docs/reference/collectors.md`, `docs/plan/STATUS.md`. No schema change, no
  migration, no API change.

## Context

The office NAS (a DS1821+, DSM 7.4.1) is infrastructure the inventory does not know. A NAS holds
data and runs services: its serial number, its drives and their health and wear, its DSM version, the
packages and containers on it and the UPS that keeps it up all matter to someone who answers for it.

DSM can be read two ways. SNMP (a MIB for the system, one for the disks, one for the UPS) needs the
service turned on in DSM, a community or a user, and says little: no package list, no container, no
drive firmware or wear, a serial number only through a vendor MIB. The Web API behind DSM's own console
(`/webapi/entry.cgi`) needs no change on the NAS and answers all of it, but it is not documented
for these calls and every call, reads and writes alike, is a `POST` of an api/method/version triple.

## Decision

1. **The Web API, not SNMP.** It is richer (serial, drives with firmware, temperature, health and
   wear, packages, containers, UPS) and asks nothing of the NAS but an account. The calls are those
   the console makes, recorded on a DS1821+ (`testdata/README.md`).
2. **`POST` only, in the body, never in a URL.** DSM accepts credentials and session ids in a query
   string, which would land in every access log and in a Go transport error. The client sends the
   account, the password and `_sid` in the form body, strips the URL from transport errors, redacts the
   password and the session id from any text and never follows a redirect.
3. **Read-only is an allow-list of (api, version, method), not a verb.** A `POST` says nothing
   about what a call does. The client refuses, before sending, everything but login, logout and the
   eight read calls listed in `client.go`, and a second list checks that every method is a read verb
   (`info`, `getinfo`, `load_info`, `get`, `list`, `query`). `readonly_test.go` proves the wire, the
   guard and the source; a new call needs an entry, a fixture and a line in the module's README.
4. **One session per run, always closed.** `SYNO.API.Auth` v6 `login`, then the reads, then `logout`
   in a `defer` with its own 10 s timeout, so a failed or timed-out run does not leave one of DSM's
   few sessions open. A failed logout is a warning.
5. **Core and optional calls.** A failing system or DSM information call fails the run. The others
   (network, storage, packages, UPS, containers) are optional: 102 and 103 (no such API or method, as
   without Container Manager) are a warning on a complete run; 104 and 105 are a warning on a partial
   run (nothing is withdrawn); any other failure is a partial, unhealthy run.
6. **Mapping within the schema.** The NAS, interfaces, addresses, drives, software and the UPS use the
   existing types and attributes. Storage pools, volumes and SSD caches have no type and none is
   added: they only feed the NAS's `health` (README). The UPS is identified by the NAS's name, since
   DSM gives it no serial.
7. **The account is a limit of DSM.** These APIs answer only an account of the `administrators`
   group, and 2-step verification cannot be answered by a collector. The README says to create a
   dedicated administrator without 2-step verification, used only by trica; the errors for DSM codes
   403 and 404 say so.

## Consequences

- A NAS is one target (`kind: synology`, `address` with the DSM HTTPS port, `username`, a password
  reference, `tls`, `options.role`, `options.skip`).
- trica holds the credentials of an administrator of the NAS. The allow-list, the body-only
  credentials and the tests of `privacy_test.go` and `readonly_test.go` are the answer, and the account's
  scope is the operator's to restrict in DSM.
- The Web API for these calls is not a published interface: a DSM update may change a field. Fields are
  decoded tolerantly (numbers as text, booleans as `"yes"`), only declared fields are read, and a change
  that breaks the core calls fails the run visibly rather than reporting wrong values.
- Three recorded answers are kept but unused (`system_storage`, `api_info`, `utilization`) and one is read
  only as a fallback (`net_iface`); `testdata/README.md` says which.

## What is not proven

- Never run against a live NAS from CI: the fixtures are one DS1821+ recorded on 2026-10-26 and a
  synthesised set.
- DSM 6 is not covered (other API versions, no Container Manager).
- One model, one DSM build: other models name drives, caches and NICs differently (`sata`, `sas`,
  `nvme`, bonds, 10G add-in cards).
- 2-step verification: only the error text is tested, not a login against it.
- A NAS with several UPS, an expansion unit or an SHR pool with a failing drive has not been seen: the
  health rollup is written from the states DSM documents.
