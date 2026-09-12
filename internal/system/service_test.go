package system

import "testing"

// The unit is written once and then only ever read by systemd, so the fields
// that make it work are worth pinning.
func TestServiceSpecIsComplete(t *testing.T) {
	spec := serviceSpec("/opt/wgui/wgui")

	for name, got := range map[string]string{
		"Name":        spec.Name,
		"Description": spec.Description,
		"ExecStart":   spec.ExecStart,
		"After":       spec.After,
		"Wants":       spec.Wants,
		"Restart":     spec.Restart,
		"RestartSec":  spec.RestartSec,
		// Without this, `systemctl enable` links the unit into nothing and the
		// panel does not come back after a reboot.
		"WantedBy": spec.WantedBy,
	} {
		if got == "" {
			t.Errorf("%s is empty", name)
		}
	}

	if spec.WantedBy != "multi-user.target" {
		t.Errorf("WantedBy = %q, want multi-user.target", spec.WantedBy)
	}
	if spec.ExecStart != "/opt/wgui/wgui" {
		t.Errorf("ExecStart = %q, want the path it was given", spec.ExecStart)
	}
}
