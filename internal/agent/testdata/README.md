# Agent fixtures

Two fake hosts, each a directory tree the probes read instead of the real `/`
(`root/`) and the recorded output of the commands the agent may run (`exec/`).

Every file here is **synthesised by hand**, not captured from a machine, in the
layout and wording of the tools named below. Nothing was recorded on a real host,
so nothing needed anonymising after the fact; the same scheme is kept so that a
future real capture can be dropped in without surprises:

| What | Scheme |
|---|---|
| Hostnames | `host-01`, `host-02.example.invalid` |
| IPv4 | `192.0.2.0/24`, `198.51.100.0/24` (RFC 5737), `172.17.0.1` for the container bridge |
| IPv6 | `2001:db8::/32` (RFC 3849), link-local `fe80::` |
| MAC | `02:00:…` (locally administered) |
| Serials, asset ids | `SN-TEST-…` |
| UUIDs, machine ids | obviously fake (`…-0000-4000-8000-00000000000N`, `0123456789abcdef…`) |
| Vendors | `Example Systems`, `QEMU` |
| Processes | generic daemon names only; there is no `cmdline` or `environ` file, on purpose |

## `debian12/` — bare-metal server, `host-01`

Mimics Debian 12 on a 2-socket, 4-thread server with 256 GiB of memory.

- `root/var/lib/dpkg/status`: dpkg 1.21 status file. Includes `libc6` for two
  architectures (amd64, i386) at one version, and a `deinstall ok config-files`
  entry that must not count as installed.
- `root/etc/{os-release,machine-id,hostname}`, `root/proc/{cpuinfo,meminfo,net/*,sys/…}`,
  `root/sys/class/{dmi,net}/…`, `root/sys/block/…`: kernel 6.1 layouts.
  Interfaces: a bond (`bond0`) with a VLAN (`bond0.100`), three physical ports
  (`eno1`..`eno3`, one of them down), plus ones that must be ignored: loopback,
  `docker0`, a `veth…` pair.
- `root/proc/net/{tcp,tcp6,udp,udp6,route,if_inet6}` and `root/proc/<pid>/comm`:
  listeners (sshd, exim4 on loopback, postgres, node_exporter, chronyd), one
  inbound ssh session, one outbound pair of connections to one remote, one
  connection to loopback, a TIME_WAIT row, a connected UDP row (DHCP client)
  and an unconnected UDP socket on an ephemeral port. `ip_local_port_range` is present.
- `root/addrs.txt`: stands in for the netlink answer (see below), `ip -o addr`
  style, including a temporary IPv6 address, link-local addresses and the
  addresses of ignored interfaces.
- `root/run/systemd/system/.keep`: systemd is "running".
- `exec/systemctl.json`: `systemctl list-units --type=service --output=json`
  as systemd 252 prints it, with one `not-found` unit that must be ignored.
- `exec/smartctl-{nvme0n1,sda,sdb}.json`: `smartctl --json -a` as smartmontools
  7.3 prints it: an NVMe drive (`percentage_used`, `available_spare`), a SATA
  SSD (wear from attribute 233), a SATA HDD (reallocated and pending sectors).
- `exec/lldpctl.json`: `lldpctl -f json` as lldpd 1.0 prints it, two neighbours:
  one with a system name and an `ifname` port id, one without a system name and
  a `local` port id.

## `rhel9/` — KVM guest, `host-02.example.invalid`

Mimics RHEL 9.4 (kernel 5.14) as a QEMU/KVM guest with 2 vCPUs, one virtio disk.

- no dpkg database: `exec/rpm-qa.txt` is the output of the allow-listed
  `rpm -qa --qf '%{NAME}\t%|EPOCH?{%{EPOCH}:}|%{VERSION}-%{RELEASE}\t%{ARCH}\n'`
  (rpm 4.16), with an epoch, a `gpg-pubkey` pseudo-package that must be skipped
  and two architectures of one package.
- `exec/systemctl.json`: one enabled unit that is not loaded (`firewalld`).
- one outbound connection (syslog to `192.0.2.60:514` from `rsyslogd`), one listener pair (sshd on `0.0.0.0:22` and `:::22`).
- DMI says `QEMU`, so the host is a `vm`; its serial is absent, as on a guest.
- no `smartctl-*`, no `lldpctl`: the guest has neither, which a probe must treat as quiet.

## Notes

- Netlink is not testable on a fake root. On a root other than `/` the agent
  reads `/addrs.txt` instead (`ifname address/prefix [0xflags]` per line); the
  netlink decoding itself is tested on synthesised messages in
  `netlink_linux_test.go`.
- `fakeRunner` (in `helpers_test.go`) serves `exec/` and, like the real
  `ExecRunner`, refuses any command line outside the allow-list.
- Tests that need a variation copy the tree into `t.TempDir()` and edit it
  (`copyTree`); the fixtures themselves are never modified.
