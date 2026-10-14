//go:build !linux

package agent

import (
	"context"
	"errors"
)

// NetlinkAddrs is only available on Linux; elsewhere it reports no addresses.
type NetlinkAddrs struct{}

// Addrs implements AddrSource.
func (NetlinkAddrs) Addrs(context.Context, FS) ([]Addr, error) {
	return nil, errors.New("netlink is only available on Linux")
}
