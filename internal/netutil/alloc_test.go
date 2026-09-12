package netutil

import (
	"net/netip"
	"testing"
)

func TestNextFreeAddress(t *testing.T) {
	subnet := netip.MustParsePrefix("10.0.0.0/24")
	server := netip.MustParseAddr("10.0.0.1")

	got, err := NextFreeAddress(subnet, server, map[string]bool{})
	if err != nil || got != "10.0.0.2/32" {
		t.Fatalf("first free = %q, %v; want 10.0.0.2/32", got, err)
	}

	used := map[string]bool{"10.0.0.2/32": true, "10.0.0.3/32": true}
	got, err = NextFreeAddress(subnet, server, used)
	if err != nil || got != "10.0.0.4/32" {
		t.Fatalf("with gaps = %q, %v; want 10.0.0.4/32", got, err)
	}
}

func TestNextFreeAddressReusesHoles(t *testing.T) {
	subnet := netip.MustParsePrefix("10.0.0.0/24")
	server := netip.MustParseAddr("10.0.0.1")

	used := map[string]bool{"10.0.0.2/32": true, "10.0.0.4/32": true}
	got, err := NextFreeAddress(subnet, server, used)
	if err != nil || got != "10.0.0.3/32" {
		t.Fatalf("hole reuse = %q, %v; want 10.0.0.3/32", got, err)
	}
}

// A /30 has exactly one usable address once the network, broadcast and server
// addresses are excluded, so the second allocation must fail rather than hand
// out the broadcast address.
func TestNextFreeAddressExhausted(t *testing.T) {
	subnet := netip.MustParsePrefix("10.0.0.0/30")
	server := netip.MustParseAddr("10.0.0.1")

	got, err := NextFreeAddress(subnet, server, map[string]bool{})
	if err != nil || got != "10.0.0.2/32" {
		t.Fatalf("only free = %q, %v; want 10.0.0.2/32", got, err)
	}
	if _, err := NextFreeAddress(subnet, server, map[string]bool{"10.0.0.2/32": true}); err == nil {
		t.Fatal("expected exhaustion error, got nil")
	}
}

func TestLastAddr(t *testing.T) {
	for _, tc := range []struct{ prefix, want string }{
		{"10.0.0.0/24", "10.0.0.255"},
		{"10.0.0.0/16", "10.0.255.255"},
		{"192.168.1.0/30", "192.168.1.3"},
	} {
		if got := lastAddr(netip.MustParsePrefix(tc.prefix)); got.String() != tc.want {
			t.Errorf("lastAddr(%s) = %s, want %s", tc.prefix, got, tc.want)
		}
	}
}
