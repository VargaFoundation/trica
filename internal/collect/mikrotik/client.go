package mikrotik

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	ros "github.com/go-routeros/routeros/v3" //nolint:misspell // third-party module path, not prose
)

// The wire protocol is the one of the third-party Go client library for the
// RouterOS API (the module imported above as ros, MIT licence). It is used for three reasons:
// it is the reference Go implementation, it speaks both the post-6.43 login
// and the older challenge login, and, more importantly for the tests, it is a
// codec written independently of the one in fake.go, so the fake is checked
// against a real client rather than against its own twin.
//
// What the library does NOT give us, and this file adds:
//   - Time bounds. In synchronous mode (the only one we use) the library
//     ignores the context of RunContext; a hung router would block forever.
//     We dial ourselves, put the context deadline on the connection and close
//     the connection when the context ends.
//   - A read-only guarantee. The library will run any command. Everything in
//     this package goes through session.print, which refuses anything that is
//     not in readCommands.
//   - Quiet logs. The library logs sentences, passwords included, at debug
//     level to a text handler on stdout; we install a handler that is never
//     enabled.

// Read-only commands. Each one is a "print": it lists, it never changes
// anything. This is the complete list of what the collector may send besides
// /login, and fake_test.go asserts that nothing else reaches the router.
const (
	cmdIdentity    = "/system/identity/print"
	cmdResource    = "/system/resource/print"
	cmdRouterboard = "/system/routerboard/print"
	cmdPackage     = "/system/package/print"
	cmdHealth      = "/system/health/print"
	cmdInterface   = "/interface/print"
	cmdEthernet    = "/interface/ethernet/print"
	cmdBonding     = "/interface/bonding/print"
	cmdVLAN        = "/interface/vlan/print"
	cmdBridge      = "/interface/bridge/print"
	cmdBridgePort  = "/interface/bridge/port/print"
	cmdBridgeVLAN  = "/interface/bridge/vlan/print"
	cmdBridgeHost  = "/interface/bridge/host/print"
	cmdAddress     = "/ip/address/print"
	cmdNeighbor    = "/ip/neighbor/print"
)

var readCommands = map[string]bool{
	cmdIdentity: true, cmdResource: true, cmdRouterboard: true, cmdPackage: true, cmdHealth: true,
	cmdInterface: true, cmdEthernet: true, cmdBonding: true, cmdVLAN: true, cmdBridge: true,
	cmdBridgePort: true, cmdBridgeVLAN: true, cmdBridgeHost: true, cmdAddress: true, cmdNeighbor: true,
}

// errNotReadOnly is returned, without sending anything, for a command that is
// not in readCommands.
var errNotReadOnly = errors.New("command is not in the read-only allowlist")

// row is one !re sentence of a reply: the attributes of one item.
type row map[string]string

func (r row) str(key string) string { return strings.TrimSpace(r[key]) }

// flag reads a boolean. The API sends true/false; the old documentation shows
// yes/no, so both are accepted.
func (r row) flag(key string) bool { return parseFlag(r[key]) }

func parseFlag(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes":
		return true
	}
	return false
}

// list splits a comma-separated value ("ether1,ether2"), dropping blanks.
func (r row) list(key string) []string { return splitList(r[key]) }

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r row) int(key string) (int, bool) {
	n, err := strconv.Atoi(r.str(key))
	return n, err == nil
}

// session is one logged-in API connection.
type session struct {
	c    *ros.Client
	conn net.Conn
	stop func() bool
}

// dial connects, bounds the connection by ctx and logs in with the post-6.43
// one-stage login. A nil tlsCfg means plain TCP (port 8728); otherwise the
// connection is TLS (api-ssl, port 8729).
func dial(ctx context.Context, address, user, password string, tlsCfg *tls.Config) (*session, error) {
	var d net.Dialer
	var conn net.Conn
	var err error
	if tlsCfg != nil {
		conn, err = (&tls.Dialer{NetDialer: &d, Config: tlsCfg}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = d.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", address, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(dl); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("connect %s: %w", address, err)
		}
	}
	s := &session{conn: conn}
	// The library ignores ctx while waiting for a reply; closing the
	// connection is what unblocks it.
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if s.c, err = ros.NewClient(conn); err != nil {
		s.close()
		return nil, err
	}
	s.c.SetLogHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.Level(100)}))
	if err := s.c.LoginContext(ctx, user, password); err != nil {
		s.close()
		return nil, fmt.Errorf("login: %w", withContext(ctx, err))
	}
	return s, nil
}

func (s *session) close() {
	s.stop()
	if s.c != nil {
		_ = s.c.Close()
	}
	_ = s.conn.Close()
}

// print runs one read-only command and returns its rows.
func (s *session) print(ctx context.Context, path string) ([]row, error) {
	if !readCommands[path] {
		return nil, fmt.Errorf("%w: %q", errNotReadOnly, path)
	}
	reply, err := s.c.RunContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, withContext(ctx, err))
	}
	rows := make([]row, 0, len(reply.Re))
	for _, re := range reply.Re {
		rows = append(rows, row(re.Map))
	}
	return rows, nil
}

// withContext says "timed out" when the connection died because the run's
// context ended, instead of the bare "use of closed connection".
func withContext(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		return fmt.Errorf("%w (%w)", err, cerr)
	}
	return err
}

// isDeviceError reports whether err is a !trap or !fatal sent by the router:
// the command failed but the connection is still usable (for !trap).
func isDeviceError(err error) bool {
	var de *ros.DeviceError
	return errors.As(err, &de)
}
