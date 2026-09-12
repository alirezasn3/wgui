package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProc stands in for /proc/sys and /etc/sysctl.d so the sysctl logic can be
// exercised without root, and without changing the machine running the tests.
func fakeProc(t *testing.T, values map[string]string) (proc, sysctl string) {
	t.Helper()

	proc, sysctl = t.TempDir(), t.TempDir()
	for rel, v := range values {
		path := filepath.Join(proc, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(v+"\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	oldProc, oldSysctl, oldSupported := procRoot, sysctlDir, supported
	procRoot, sysctlDir, supported = proc, sysctl, true
	t.Cleanup(func() { procRoot, sysctlDir, supported = oldProc, oldSysctl, oldSupported })
	return proc, sysctl
}

func TestReadReportsCurrentState(t *testing.T) {
	fakeProc(t, map[string]string{
		forwardV4:  "1",
		forwardV6:  "0",
		congestion: "cubic",
		available:  "reno cubic bbr",
		qdisc:      "fq_codel",
	})

	s := Read()
	if !s.IPForwardingV4 || s.IPForwardingV6 {
		t.Errorf("forwarding = v4 %v, v6 %v; want v4 on and v6 off", s.IPForwardingV4, s.IPForwardingV6)
	}
	if s.CongestionControl != "cubic" || s.DefaultQdisc != "fq_codel" {
		t.Errorf("got %s/%s, want cubic/fq_codel", s.CongestionControl, s.DefaultQdisc)
	}
	if !s.BBRAvailable {
		t.Error("BBR is listed as available but was not detected")
	}
	if s.BBREnabled {
		t.Error("BBR is not the active algorithm but was reported as enabled")
	}
}

func TestEnableIPForwardingWritesKernelAndDropIn(t *testing.T) {
	proc, sysctl := fakeProc(t, map[string]string{forwardV4: "0", forwardV6: "0"})

	if err := EnableIPForwarding(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	for _, rel := range []string{forwardV4, forwardV6} {
		b, err := os.ReadFile(filepath.Join(proc, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.TrimSpace(string(b)) != "1" {
			t.Errorf("%s = %q, want 1", rel, strings.TrimSpace(string(b)))
		}
	}

	// The change has to survive a reboot, not just this boot.
	b, err := os.ReadFile(filepath.Join(sysctl, "99-wgui-forwarding.conf"))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	for _, want := range []string{"net.ipv4.ip_forward = 1", "net.ipv6.conf.all.forwarding = 1"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("drop-in missing %q:\n%s", want, b)
		}
	}
}

// A kernel built without IPv6 has no forwarding knob for it; that must not fail
// the whole operation.
func TestEnableIPForwardingSkipsMissingIPv6(t *testing.T) {
	_, sysctl := fakeProc(t, map[string]string{forwardV4: "0"})

	if err := EnableIPForwarding(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(sysctl, "99-wgui-forwarding.conf"))
	if strings.Contains(string(b), "ipv6") {
		t.Errorf("drop-in mentions IPv6 on a kernel that has none:\n%s", b)
	}
}

func TestSetCongestionControlSelectsBBRAndFQ(t *testing.T) {
	proc, sysctl := fakeProc(t, map[string]string{
		congestion: "cubic",
		available:  "reno cubic bbr",
		qdisc:      "fq_codel",
	})

	if err := SetCongestionControl("bbr"); err != nil {
		t.Fatalf("set: %v", err)
	}

	if got := readFile(t, filepath.Join(proc, congestion)); got != "bbr" {
		t.Errorf("congestion control = %q, want bbr", got)
	}
	// BBR expects fq; selecting one without the other is a half-measure.
	if got := readFile(t, filepath.Join(proc, qdisc)); got != "fq" {
		t.Errorf("qdisc = %q, want fq alongside BBR", got)
	}

	b, err := os.ReadFile(filepath.Join(sysctl, "99-wgui-congestion.conf"))
	if err != nil {
		t.Fatalf("read drop-in: %v", err)
	}
	for _, want := range []string{"net.core.default_qdisc = fq", "net.ipv4.tcp_congestion_control = bbr"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("drop-in missing %q:\n%s", want, b)
		}
	}
}

func TestSetCongestionControlRejectsWhatTheKernelLacks(t *testing.T) {
	proc, _ := fakeProc(t, map[string]string{
		congestion: "cubic",
		available:  "reno cubic",
		qdisc:      "fq_codel",
	})

	err := SetCongestionControl("bbr")
	if err != ErrBBRUnavailable {
		t.Fatalf("err = %v, want ErrBBRUnavailable", err)
	}
	// Nothing may have been touched on the way to that error.
	if got := readFile(t, filepath.Join(proc, congestion)); got != "cubic" {
		t.Errorf("congestion control = %q, want it left at cubic", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.TrimSpace(string(b))
}

// Off Linux the package must refuse rather than write to paths that mean
// something else, so the panel can say so instead of appearing to succeed.
func TestUnsupportedOffLinux(t *testing.T) {
	fakeProc(t, map[string]string{forwardV4: "0"})
	supported = false

	if err := EnableIPForwarding(); err != ErrUnsupported {
		t.Errorf("EnableIPForwarding = %v, want ErrUnsupported", err)
	}
	if err := SetCongestionControl("bbr"); err != ErrUnsupported {
		t.Errorf("SetCongestionControl = %v, want ErrUnsupported", err)
	}
	if s := Read(); s.Supported {
		t.Error("Read reported the settings as supported off Linux")
	}
}
