package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTool drops an executable of the given name into dir.
func fakeTool(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// useFakeTools points the lookup at a directory of stand-ins.
func useFakeTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	oldDirs, oldSupported, oldPath := sbinDirs, supported, os.Getenv("PATH")
	sbinDirs, supported = []string{dir}, true
	os.Setenv("PATH", dir)
	t.Cleanup(func() {
		sbinDirs, supported = oldDirs, oldSupported
		os.Setenv("PATH", oldPath)
	})
	return dir
}

func byName(sets []RuleSet, name string) RuleSet {
	for _, rs := range sets {
		if rs.Name == name {
			return rs
		}
	}
	return RuleSet{}
}

func TestNetworkRulesReportsEachTool(t *testing.T) {
	dir := useFakeTools(t)
	fakeTool(t, dir, "iptables", `echo "-A ufw-user-input -p udp --dport 51820 -j ACCEPT"`)
	fakeTool(t, dir, "ip", `echo "default via 10.0.0.1 dev wg0"`)
	// ufw itself is deliberately absent.

	sets := NetworkRules(context.Background())
	if len(sets) != len(networkCommands) {
		t.Fatalf("got %d sections, want one per command", len(sets))
	}

	// Each ufw chain is read on its own, so the rules an operator wrote are not
	// buried in ufw's generated scaffolding.
	for _, chain := range []string{"ufw-user-input", "ufw-user-forward", "ufw-user-output"} {
		rs := byName(sets, chain)
		if !rs.Available || rs.Error != "" {
			t.Errorf("%s = available %v, error %q", chain, rs.Available, rs.Error)
		}
		if rs.Command != "iptables -S "+chain {
			t.Errorf("%s command = %q", chain, rs.Command)
		}
		if !strings.Contains(rs.Output, "ufw-user-input") {
			t.Errorf("%s output = %q", chain, rs.Output)
		}
	}

	if route := byName(sets, "ip route"); !strings.Contains(route.Output, "default via") {
		t.Errorf("ip route output = %q", route.Output)
	}

	// A missing tool is reported, not an error that hides the rest.
	ufw := byName(sets, "ufw")
	if ufw.Available || ufw.Error != "not installed" {
		t.Errorf("ufw = available %v, error %q; want reported as missing", ufw.Available, ufw.Error)
	}
}

// A tool that exists but refuses — no root, say — must report why rather than
// looking like it is missing.
func TestNetworkRulesReportsAFailingTool(t *testing.T) {
	dir := useFakeTools(t)
	fakeTool(t, dir, "iptables", `echo "Permission denied (you must be root)" >&2; exit 1`)

	ipt := byName(NetworkRules(context.Background()), "ufw-user-input")
	if !ipt.Available {
		t.Error("a tool that ran and failed is still present")
	}
	if ipt.Error == "" {
		t.Error("the failure was not reported")
	}
	if !strings.Contains(ipt.Output, "must be root") {
		t.Errorf("the tool's own message was dropped: %q", ipt.Output)
	}
}

func TestNetworkRulesTruncatesHugeOutput(t *testing.T) {
	dir := useFakeTools(t)
	// Built from shell builtins alone: the lookup path is replaced for this
	// test, so the fixture cannot rely on finding head or tr.
	fakeTool(t, dir, "iptables", `s=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
i=0
while [ $i -lt 14 ]; do s="$s$s"; i=$((i+1)); done
echo "$s"`)

	ipt := byName(NetworkRules(context.Background()), "ufw-user-input")
	if !ipt.Truncated {
		t.Error("oversized output was not flagged as truncated")
	}
	if len(ipt.Output) > maxOutput {
		t.Errorf("output is %d bytes, over the %d cap", len(ipt.Output), maxOutput)
	}
}

// A wedged tool must not hang the request that asked for it.
func TestNetworkRulesTimesOut(t *testing.T) {
	dir := useFakeTools(t)
	fakeTool(t, dir, "ip", `sleep 30`)

	start := time.Now()
	route := byName(NetworkRules(context.Background()), "ip route")
	if elapsed := time.Since(start); elapsed > commandTimeout+3*time.Second {
		t.Fatalf("took %s, want a timeout near %s", elapsed, commandTimeout)
	}
	if !strings.Contains(route.Error, "timed out") {
		t.Errorf("error = %q, want a timeout", route.Error)
	}
}

// Off Linux these tools mean something else or nothing at all, so nothing is run.
func TestNetworkRulesRefusesOffLinux(t *testing.T) {
	useFakeTools(t)
	supported = false

	for _, rs := range NetworkRules(context.Background()) {
		if rs.Available || rs.Output != "" {
			t.Errorf("%s ran off Linux", rs.Name)
		}
		if !strings.Contains(rs.Error, "only exist on Linux") {
			t.Errorf("%s error = %q, want it to say these are Linux tools", rs.Name, rs.Error)
		}
	}
}
