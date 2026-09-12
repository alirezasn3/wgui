// Package netutil allocates tunnel addresses out of the interface's subnet.
package netutil

import (
	"errors"
	"fmt"
	"net/netip"
)

var ErrPoolExhausted = errors.New("no free address left in the interface subnet")

// NextFreeAddress returns the lowest unused host address in subnet as a /32,
// skipping the network address, the broadcast address and the server's own
// address. used holds the addresses already handed out, in "10.0.0.5/32" form.
func NextFreeAddress(subnet netip.Prefix, server netip.Addr, used map[string]bool) (string, error) {
	if !subnet.Addr().Is4() {
		return "", fmt.Errorf("only IPv4 subnets are supported, got %s", subnet)
	}

	subnet = subnet.Masked()
	broadcast := lastAddr(subnet)

	for addr := subnet.Addr().Next(); subnet.Contains(addr); addr = addr.Next() {
		if addr == broadcast {
			break
		}
		if addr == server {
			continue
		}
		candidate := addr.String() + "/32"
		if !used[candidate] {
			return candidate, nil
		}
	}
	return "", ErrPoolExhausted
}

// lastAddr returns the highest address in the prefix, i.e. the IPv4 broadcast
// address.
func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	hostBits := 32 - p.Bits()
	for i := 0; i < hostBits; i++ {
		b[3-i/8] |= 1 << (i % 8)
	}
	return netip.AddrFrom4(b)
}
