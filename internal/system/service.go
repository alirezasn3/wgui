package system

import (
	"errors"
	"fmt"

	goSystemd "github.com/alirezasn3/go-systemd"
)

// ServiceName is the systemd unit wgui installs itself as.
const ServiceName = "wgui"

// serviceSpec describes the unit wgui installs.
//
// Every field here is load-bearing, and WantedBy most of all: without it
// `systemctl enable` has no target to link the unit into, so it silently does
// nothing and the panel never comes back after a reboot. The library defaults
// it now, but it is spelled out rather than inherited, because it is the one
// directive whose absence costs nothing at install time and everything at the
// next boot.
//
// There is deliberately no sandboxing. The panel configures the WireGuard
// device, edits sysctl, writes firewall rules and runs the operator's own
// scripts, so ProtectSystem and friends would break the features rather than
// protect anything the panel does not already have.
func serviceSpec(execPath string) *goSystemd.Service {
	return &goSystemd.Service{
		Name:        ServiceName,
		Description: "wgui — WireGuard management panel",
		ExecStart:   execPath,
		After:       "network-online.target nss-lookup.target",
		Wants:       "network-online.target",
		Restart:     "on-failure",
		RestartSec:  "3s",
		WantedBy:    "multi-user.target",
	}
}

// InstallService writes the unit for the running binary, enables it at boot and
// starts it.
//
// An existing unit is removed first rather than left alone. That is what makes
// running this again a repair: an installation from an older version, or one
// naming a binary that has since moved, is replaced instead of being enabled in
// place — and enabling a unit that has no WantedBy achieves nothing at all.
func InstallService(execPath string) error {
	status, err := goSystemd.StatusService(ServiceName)
	if err != nil {
		return err
	}
	if status.Exists {
		if err := goSystemd.DeleteService(ServiceName); err != nil &&
			!errors.Is(err, goSystemd.ErrServiceNotFound) {
			return permissionHint(err)
		}
	}
	return permissionHint(goSystemd.CreateService(serviceSpec(execPath)))
}

// UninstallService stops the service, unlinks it from its boot target and
// removes the unit. A unit that was never installed is not an error: the end
// state is what matters.
func UninstallService() error {
	err := goSystemd.DeleteService(ServiceName)
	if errors.Is(err, goSystemd.ErrServiceNotFound) {
		return nil
	}
	return permissionHint(err)
}

// permissionHint says what to do about the one failure an operator hits most.
func permissionHint(err error) error {
	if errors.Is(err, goSystemd.ErrPermissionDenied) {
		return fmt.Errorf("%w — try sudo", err)
	}
	return err
}
