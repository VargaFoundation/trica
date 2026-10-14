package agent

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Socket states in /proc/net/{tcp,udp}.
const (
	stEstablished = 0x01
	stListen      = 0x0A
	stUDPUnconn   = 0x07 // an unconnected UDP socket is the closest thing UDP has to listening
)

const (
	maxListeners = 5000
	maxConns     = 1000
	// maxEstablished bounds the connections held in memory while a busy host's table is read.
	maxEstablished = 200000
	// defaultEphemeralLow is the bottom of Linux's default ephemeral port range.
	defaultEphemeralLow = 32768
)

type sockRow struct {
	lip, rip     netip.Addr
	lport, rport uint16
	state        uint8
	inode        uint64
	proto        string
}

// parseHexAddr decodes "0A0200C0:1538" or the 32-hex-digit IPv6 form. The
// kernel prints each 32-bit word of the address in host byte order.
func parseHexAddr(s string) (netip.Addr, uint16, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.Addr{}, 0, fmt.Errorf("bad socket address %q", s)
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.Addr{}, 0, fmt.Errorf("bad port in %q: %w", s, err)
	}
	if len(h) != 8 && len(h) != 32 {
		return netip.Addr{}, 0, fmt.Errorf("bad address length in %q", s)
	}
	var b [16]byte
	n := len(h) / 2
	for w := 0; w < len(h)/8; w++ {
		v, err := strconv.ParseUint(h[w*8:w*8+8], 16, 32)
		if err != nil {
			return netip.Addr{}, 0, fmt.Errorf("bad address in %q: %w", s, err)
		}
		binary.NativeEndian.PutUint32(b[w*4:], uint32(v))
	}
	if n == 4 {
		return netip.AddrFrom4([4]byte(b[:4])), uint16(port), nil
	}
	return netip.AddrFrom16(b), uint16(port), nil
}

// readSockets streams one /proc/net table and calls keep for each row.
func (c *collector) readSockets(name, proto string, keep func(sockRow)) error {
	r, err := c.fs.Open("/proc/net/" + name)
	if err != nil {
		return err
	}
	defer r.Close()
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // header
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		var row sockRow
		row.proto = proto
		var perr error
		if row.lip, row.lport, perr = parseHexAddr(f[1]); perr != nil {
			return perr
		}
		if row.rip, row.rport, perr = parseHexAddr(f[2]); perr != nil {
			return perr
		}
		st, perr := strconv.ParseUint(f[3], 16, 8)
		if perr != nil {
			return fmt.Errorf("bad socket state %q: %w", f[3], perr)
		}
		row.state = uint8(st)
		if row.inode, perr = strconv.ParseUint(f[9], 10, 64); perr != nil {
			return fmt.Errorf("bad inode %q: %w", f[9], perr)
		}
		keep(row)
	}
	return sc.Err()
}

// ephemeralLow is the first port of the ephemeral range: an unconnected UDP
// socket above it is an outgoing query, not a service.
func (c *collector) ephemeralLow() uint16 {
	f := strings.Fields(readOptional(c.fs, "/proc/sys/net/ipv4/ip_local_port_range"))
	if len(f) == 2 {
		if n, err := strconv.ParseUint(f[0], 10, 16); err == nil && n > 0 {
			return uint16(n)
		}
	}
	return defaultEphemeralLow
}

// probeSockets finds what the host serves (listening TCP sockets, unconnected
// UDP sockets below the ephemeral range) and what it talks to (outbound
// established TCP connections), with the name of the owning process when
// /proc/<pid>/fd is readable.
//
// Only outbound connections become talks_to: an inbound one is a client of
// this host, and it is that client's agent that reports the relation. Several
// connections to one remote address fold into one, the smallest port first, so
// that the batch does not change with the ephemeral source ports.
func (c *collector) probeSockets() error {
	var listeners, established []sockRow
	readable := 0
	var lastErr error
	for _, t := range []struct{ file, proto string }{{"tcp", "tcp"}, {"tcp6", "tcp"}, {"udp", "udp"}, {"udp6", "udp"}} {
		err := c.readSockets(t.file, t.proto, func(r sockRow) {
			switch {
			case t.proto == "tcp" && r.state == stListen, t.proto == "udp" && r.state == stUDPUnconn:
				listeners = append(listeners, r)
			case t.proto == "tcp" && r.state == stEstablished && usableRemote(r.rip):
				if len(established) < maxEstablished {
					established = append(established, r)
				}
			}
		})
		switch {
		case err == nil:
			readable++
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission):
			lastErr = err
		default:
			return fmt.Errorf("/proc/net/%s: %w", t.file, err)
		}
	}
	if readable == 0 {
		return skipf("/proc/net sockets not readable: %v", lastErr)
	}

	low := c.ephemeralLow()
	tcpPorts := map[uint16]bool{}
	var keep []sockRow
	for _, l := range listeners {
		if l.lport == 0 || (l.proto == "udp" && l.lport >= low) {
			continue
		}
		if l.proto == "tcp" {
			tcpPorts[l.lport] = true
		}
		keep = append(keep, l)
	}
	sort.Slice(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if a.proto != b.proto {
			return a.proto < b.proto
		}
		if a.lport != b.lport {
			return a.lport < b.lport
		}
		return a.lip.Compare(b.lip) < 0
	})
	if len(keep) > maxListeners {
		c.inv.Skipped = append(c.inv.Skipped, fmt.Sprintf("sockets: %d listeners, first %d kept", len(keep), maxListeners))
		keep = keep[:maxListeners]
	}

	own := map[netip.Addr]bool{}
	for _, a := range c.inv.Addrs {
		own[a.IP] = true
	}
	// Fold the outbound connections by remote address.
	byRemote := map[netip.Addr]sockRow{}
	for _, e := range established {
		rip := e.rip.Unmap()
		if tcpPorts[e.lport] || own[rip] {
			continue // inbound, or a connection to ourselves
		}
		if cur, ok := byRemote[rip]; !ok || e.rport < cur.rport {
			byRemote[rip] = e
		}
	}
	remotes := make([]netip.Addr, 0, len(byRemote))
	for a := range byRemote {
		remotes = append(remotes, a)
	}
	sort.Slice(remotes, func(i, j int) bool { return remotes[i].Compare(remotes[j]) < 0 })
	if len(remotes) > maxConns {
		c.inv.Skipped = append(c.inv.Skipped, fmt.Sprintf("sockets: %d remote hosts, first %d kept", len(remotes), maxConns))
		remotes = remotes[:maxConns]
	}

	want := map[uint64]bool{}
	for _, l := range keep {
		want[l.inode] = true
	}
	for _, a := range remotes {
		want[byRemote[a].inode] = true
	}
	procs, unreadable := c.processesByInode(want)
	if unreadable > 0 {
		c.inv.Skipped = append(c.inv.Skipped, fmt.Sprintf("sockets: %d processes' descriptors not readable (not root), their sockets have no process name", unreadable))
	}

	for _, l := range keep {
		c.inv.Listeners = append(c.inv.Listeners, Listener{Proto: l.proto, Addr: l.lip.Unmap(), Port: l.lport, Process: procs[l.inode]})
	}
	for _, a := range remotes {
		r := byRemote[a]
		c.inv.Conns = append(c.inv.Conns, Conn{Proto: r.proto, Remote: a, Port: r.rport, Process: procs[r.inode]})
	}
	return nil
}

// usableRemote filters the remote addresses that say nothing about the
// infrastructure: loopback, unspecified, link-local (not unique) and multicast.
func usableRemote(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsLoopback() && !a.IsUnspecified() && !a.IsLinkLocalUnicast() && !a.IsMulticast()
}

// processesByInode maps socket inodes to the name (comm) of the lowest-numbered
// process holding them. Only the process name is read: never its command
// line or environment, which are where secrets live. A process whose
// descriptors cannot be listed (another user's, without root) is counted.
func (c *collector) processesByInode(want map[uint64]bool) (map[uint64]string, int) {
	out := map[uint64]string{}
	if len(want) == 0 {
		return out, 0
	}
	ents, err := c.fs.ReadDir("/proc")
	if err != nil {
		return out, 0
	}
	var pids []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > 0 && e.IsDir() {
			pids = append(pids, n)
		}
	}
	sort.Ints(pids)
	unreadable := 0
	for _, pid := range pids {
		if len(out) == len(want) || c.ctx.Err() != nil {
			break
		}
		dir := "/proc/" + strconv.Itoa(pid) + "/fd"
		fds, err := c.fs.ReadDir(dir)
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				unreadable++
			}
			continue // the process may simply have exited
		}
		name := ""
		for _, fd := range fds {
			target, err := c.fs.Readlink(dir + "/" + fd.Name())
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(target[len("socket:["):], "]"), 10, 64)
			if err != nil || !want[inode] {
				continue
			}
			if _, done := out[inode]; done {
				continue
			}
			if name == "" {
				name = readOptional(c.fs, "/proc/"+strconv.Itoa(pid)+"/comm")
			}
			out[inode] = name
		}
	}
	return out, unreadable
}
