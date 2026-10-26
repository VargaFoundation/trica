# Fixtures of the synology collector

Two sets of the DSM Web API. Each file is the complete JSON body of one DSM answer (the
`{"success":true,"data":...}` envelope included), served by `FakeDSM` for a `POST
/webapi/entry.cgi`. The fake picks the file from the call (`api`, `method`, and `type` for
`SYNO.Core.System info`); a call no file answers is DSM's error 102, which is what a NAS without
that API says.

| Set | Recorded or synthesised | Content |
|---|---|---|
| `recorded-2026-10/` | **Recorded** on 2026-10-26 from one Synology DS1821+ running DSM 7.4.1-90080 Update 1, through the Web API with an administrators-group account, then anonymised and trimmed (below). | The NAS with 4 GiB of RAM, 6 NICs (one 10 GbE connected on a /24, five with a link-local address), 6 SATA HDDs and 2 M.2 NVMe drives used as SSD cache, two storage pools (`reuse_1` and its volume are `attention`, which gives a `warning` rollup), 21 packages, one running container image (`netbirdio/netbird:0.78.1`) and a USB UPS (EATON Ellipse PRO 650, online). |
| `synthesised/` | **Synthesised** by hand from the recorded shapes and the DSM 7.2 documentation. Nothing in it was observed. | A DS923+-like NAS (DSM 7.2.2-72806 Update 3, 8 GiB, 30 hours of uptime): temperature warning raised; a crashed pool and volume; one crashed HDD; a SATA SSD with `remain_life` 85 and one with an untrusted `remain_life`; two connected NICs with an address each (one /24, one /25, one with MTU 9000), a link-local NIC and a `pppoe` entry; no UPS (`enable` false); no Container Manager (error 102 in `containers.json`); a third-party package with a `maintainer`. No `net_iface.json`: the fake answers 102, which the collector only asks for when the system call lists no NIC. |

## Files and the call each answers

| File | Call | Recorded set | Synthesised set | Read by the collector |
|---|---|---|---|---|
| `system_info.json` | `SYNO.Core.System` v1 `info` | recorded | synthesised | yes |
| `system_network.json` | `SYNO.Core.System` v1 `info`, `type=network` | recorded | synthesised | yes (NICs, MACs, hostname) |
| `dsm_info.json` | `SYNO.DSM.Info` v2 `getinfo` | recorded | synthesised | yes |
| `storage_load_info.json` | `SYNO.Storage.CGI.Storage` v1 `load_info` | recorded | synthesised | yes (drives, pools, volumes, caches) |
| `network.json` | `SYNO.Core.Network` v1 `get` | recorded | synthesised | yes (server name, gateway) |
| `packages.json` | `SYNO.Core.Package` v2 `list` | recorded | synthesised | yes |
| `ups.json` | `SYNO.Core.ExternalDevice.UPS` v1 `get` | recorded | synthesised (disabled) | yes |
| `containers.json` | `SYNO.Docker.Container` v1 `list` | recorded (trimmed) | synthesised (error 102) | yes |
| `net_iface.json` | `SYNO.Core.Network.Interface` v1 `list` | recorded | none | only when `system_network` lists no NIC (a test blanks it) |
| `system_storage.json` | `SYNO.Core.System` v1 `info`, `type=storage` | recorded | none | **no**: `load_info` says more; kept as recorded, the fake serves it |
| `api_info.json` | `SYNO.API.Info` v1 `query` (`query.cgi`) | recorded (trimmed) | none | **no**: not served by the fake, kept as the record of the API versions the allow-list was written from |
| `utilization.json` | `SYNO.Core.System.Utilization` v1 `get` | recorded | none | **no**: not on the allow-list, not served by the fake; a record of what exists |

## Anonymisation and trimming (recorded set)

Applied by hand, consistently across the files (one real value always got the same fake value):

- Serial numbers: the NAS is `0000ABCDE0001` (also `machineSerial`), the drives `FAKEHDD0001` to
  `FAKEHDD0006` and `FAKENVME00001`, `FAKENVME00002`, in `serial` and `ui_serial`. Drive models,
  firmware and sizes are kept as recorded.
- MAC addresses: locally administered, `02-11-32-00-00-01` to `-06`, in DSM's dash format.
- Addresses: the LAN is `192.0.2.0/24` (NAS `192.0.2.57`, gateway and DNS `192.0.2.1`); the
  link-local addresses `169.254.x.x` are random ones, kept as recorded in shape. The address the
  NAS was reached at is `198.51.100.36` (not in a fixture: the fake listens on 127.0.0.1).
- Names: hostname and server name `nas-a1`. UUIDs, volume and cache UUIDs are zero-filled.
- Containers: trimmed to `name`, `image`, `Image`, `status`, `State` (running state), `is_package`
  and `id` (zeros): the real answer carries environment variables, ports and mounts.
- API info: trimmed to the APIs the collector uses.
- Nothing else was edited; no account, share, user, e-mail, SNMP community, NTP or DNS name of the
  real NAS is kept (`ups.json` has the empty SNMP fields DSM answers). `anonymised_test.go` fails on
  a MAC outside `02-11-32-00-00-xx`, a serial outside the forms above, an address outside
  `192.0.2.0/24`, the other documentation ranges and link-local, an e-mail, or a credential field.

## What the fakes are used for

`FakeDSM` checks the account and password (`Account`, `Password`: fixed fake values of the package,
the target holds them in a temporary file), hands out a fake session id (`FAKESID0001`, ...), refuses
a call without a valid one with error 119, and records every request without its credentials, so the
tests can prove the collector sent only allow-listed POSTs with nothing in the URL and that it logged
out. Knobs: `SetCredentials`, `RejectLogin`, `Fail`, `SetBody`, `Slow`.
