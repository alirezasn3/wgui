// Package system reads and changes the two kernel settings a WireGuard server
// almost always needs: IP forwarding, without which the server cannot route
// peer traffic at all, and BBR congestion control, which usually helps over
// long-distance links.
//
// Changes are applied to the running kernel through /proc/sys and persisted as
// files in /etc/sysctl.d so they survive a reboot.
package system

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	// ErrUnsupported is returned on anything that is not Linux.
	ErrUnsupported = errors.New("this setting can only be changed on Linux")
	// ErrPermission is returned when wgui is not running with enough privileges.
	ErrPermission = errors.New("wgui needs to run as root to change kernel settings")
	// ErrBBRUnavailable is returned when the kernel has no BBR to switch to.
	ErrBBRUnavailable = errors.New("this kernel does not offer BBR; load the tcp_bbr module or use a newer kernel")
)

// supported is a variable, not a direct runtime.GOOS check, so the sysctl logic
// can be exercised by tests on any machine.
var supported = runtime.GOOS == "linux"

// Paths are variables so tests can point them at a temporary directory.
var (
	procRoot   = "/proc/sys"
	sysctlDir  = "/etc/sysctl.d"
	forwardV4  = "net/ipv4/ip_forward"
	forwardV6  = "net/ipv6/conf/all/forwarding"
	congestion = "net/ipv4/tcp_congestion_control"
	available  = "net/ipv4/tcp_available_congestion_control"
	qdisc      = "net/core/default_qdisc"
)

// Status describes the current kernel configuration and what can be done to it.
type Status struct {
	Supported bool `json:"supported"` // false off Linux
	Writable  bool `json:"writable"`  // false when not running as root

	IPForwardingV4 bool `json:"ipForwardingV4"`
	IPForwardingV6 bool `json:"ipForwardingV6"`

	CongestionControl string   `json:"congestionControl"`
	AvailableAlgos    []string `json:"availableAlgos"`
	DefaultQdisc      string   `json:"defaultQdisc"`
	BBRAvailable      bool     `json:"bbrAvailable"`
	BBREnabled        bool     `json:"bbrEnabled"`
}

// Read reports the current state. It never fails: anything unreadable is
// reported as unset, since this only drives what the settings page displays.
func Read() Status {
	s := Status{Supported: supported}
	if !s.Supported {
		return s
	}

	s.IPForwardingV4 = readProc(forwardV4) == "1"
	s.IPForwardingV6 = readProc(forwardV6) == "1"
	s.CongestionControl = readProc(congestion)
	s.DefaultQdisc = readProc(qdisc)
	s.AvailableAlgos = strings.Fields(readProc(available))

	for _, algo := range s.AvailableAlgos {
		if algo == "bbr" {
			s.BBRAvailable = true
			break
		}
	}
	s.BBREnabled = s.CongestionControl == "bbr"
	s.Writable = os.Geteuid() == 0

	return s
}

// EnableIPForwarding turns on forwarding for IPv4 and IPv6 and makes it stick.
func EnableIPForwarding() error {
	if !supported {
		return ErrUnsupported
	}

	settings := []setting{{forwardV4, "1"}}
	// IPv6 forwarding is only set when the kernel exposes it, so a build with
	// IPv6 disabled does not fail the whole operation.
	if exists(forwardV6) {
		settings = append(settings, setting{forwardV6, "1"})
	}

	if err := apply(settings); err != nil {
		return err
	}
	return persist("99-wgui-forwarding.conf", settings)
}

// SetCongestionControl switches the congestion control algorithm. Passing "bbr"
// also selects the fq queueing discipline, which is what BBR expects.
func SetCongestionControl(algo string) error {
	if !supported {
		return ErrUnsupported
	}

	current := Read()
	if !contains(current.AvailableAlgos, algo) {
		if algo == "bbr" {
			return ErrBBRUnavailable
		}
		return fmt.Errorf("this kernel does not offer %q; it offers %s",
			algo, strings.Join(current.AvailableAlgos, ", "))
	}

	settings := []setting{{congestion, algo}}
	if algo == "bbr" {
		settings = append([]setting{{qdisc, "fq"}}, settings...)
	}

	if err := apply(settings); err != nil {
		return err
	}
	return persist("99-wgui-congestion.conf", settings)
}

type setting struct {
	path  string // relative to /proc/sys, e.g. net/ipv4/ip_forward
	value string
}

// key turns a /proc/sys path into its sysctl name, net/ipv4/ip_forward becoming
// net.ipv4.ip_forward.
func (s setting) key() string { return strings.ReplaceAll(s.path, "/", ".") }

// apply writes to the running kernel.
func apply(settings []setting) error {
	for _, s := range settings {
		if err := os.WriteFile(filepath.Join(procRoot, s.path), []byte(s.value+"\n"), 0o644); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return ErrPermission
			}
			return fmt.Errorf("set %s: %w", s.key(), err)
		}
	}
	return nil
}

// persist writes a sysctl.d drop-in so the change survives a reboot. The running
// kernel has already been changed by this point, so a failure here is reported
// but leaves the setting active until the next restart.
func persist(name string, settings []setting) error {
	var b strings.Builder
	b.WriteString("# Written by wgui.\n")
	for _, s := range settings {
		fmt.Fprintf(&b, "%s = %s\n", s.key(), s.value)
	}

	path := filepath.Join(sysctlDir, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("%w (the change is active now but will not survive a reboot)", ErrPermission)
		}
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readProc(path string) string {
	b, err := os.ReadFile(filepath.Join(procRoot, path))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func exists(path string) bool {
	_, err := os.Stat(filepath.Join(procRoot, path))
	return err == nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
