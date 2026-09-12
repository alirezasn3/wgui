// Package monitor watches destinations and runs a script when one stops
// answering.
package monitor

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Probe is the result of one reachability check.
type Probe struct {
	OK  bool
	RTT time.Duration
	Err error
}

// Check tests whether target is reachable.
//
// A target with a port ("example.com:443") is checked with a TCP connection,
// which says whether the service is up; anything else is checked with an ICMP
// echo. The distinction matters because plenty of hosts drop ICMP, and a
// monitor that cannot tell "firewalled" from "down" would run its script for no
// reason.
func Check(ctx context.Context, target string, timeout time.Duration) Probe {
	target = strings.TrimSpace(target)
	if target == "" {
		return Probe{Err: fmt.Errorf("no target")}
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	if host, port, err := net.SplitHostPort(target); err == nil && port != "" {
		return checkTCP(ctx, net.JoinHostPort(host, port), timeout)
	}
	return checkICMP(ctx, target, timeout)
}

func checkTCP(ctx context.Context, address string, timeout time.Duration) Probe {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return Probe{Err: err}
	}
	conn.Close()
	return Probe{OK: true, RTT: time.Since(start)}
}

// icmpID identifies our echo requests. The privileged socket sees every reply on
// the machine, so they have to be told apart.
var icmpID = os.Getpid() & 0xffff

var seq struct {
	sync.Mutex
	n int
}

func nextSeq() int {
	seq.Lock()
	defer seq.Unlock()
	seq.n = (seq.n + 1) & 0xffff
	return seq.n
}

func checkICMP(ctx context.Context, target string, timeout time.Duration) Probe {
	addr, err := net.ResolveIPAddr("ip4", target)
	if err != nil {
		return Probe{Err: fmt.Errorf("resolve %s: %w", target, err)}
	}

	conn, id, err := listenICMP()
	if err != nil {
		return Probe{Err: err}
	}
	defer conn.Close()

	sequence := nextSeq()
	message := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Body: &icmp.Echo{ID: id, Seq: sequence, Data: []byte("wgui")},
	}
	encoded, err := message.Marshal(nil)
	if err != nil {
		return Probe{Err: err}
	}

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return Probe{Err: err}
	}

	start := time.Now()
	// The unprivileged socket rewrites the echo id, so the destination address
	// is what has to match on the way back.
	if _, err := conn.WriteTo(encoded, destination(conn, addr)); err != nil {
		return Probe{Err: err}
	}

	buf := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(buf)
		if err != nil {
			return Probe{Err: fmt.Errorf("no reply from %s within %s", target, timeout)}
		}
		if !sameHost(peer, addr) {
			continue
		}

		reply, err := icmp.ParseMessage(ipv4.ICMPTypeEchoReply.Protocol(), buf[:n])
		if err != nil {
			continue
		}
		echo, ok := reply.Body.(*icmp.Echo)
		if reply.Type != ipv4.ICMPTypeEchoReply || !ok || echo.Seq != sequence {
			continue
		}
		return Probe{OK: true, RTT: time.Since(start)}
	}
}

// listenICMP opens a raw socket, falling back to the unprivileged datagram one
// where the kernel allows it.
func listenICMP() (*icmp.PacketConn, int, error) {
	if conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0"); err == nil {
		return conn, icmpID, nil
	}
	conn, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		return nil, 0, fmt.Errorf("open an ICMP socket: %w "+
			"(wgui needs to run as root, or net.ipv4.ping_group_range must include its group)", err)
	}
	// The kernel picks the id on this socket, so ours is not used.
	return conn, 0, nil
}

// destination wraps the address in the form the open socket expects.
func destination(conn *icmp.PacketConn, addr *net.IPAddr) net.Addr {
	if _, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return &net.UDPAddr{IP: addr.IP}
	}
	return addr
}

func sameHost(peer net.Addr, addr *net.IPAddr) bool {
	switch v := peer.(type) {
	case *net.IPAddr:
		return v.IP.Equal(addr.IP)
	case *net.UDPAddr:
		return v.IP.Equal(addr.IP)
	default:
		return false
	}
}
