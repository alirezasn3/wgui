package system

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// commandTimeout bounds each command, so a wedged tool cannot hang the request
// that asked for it.
const commandTimeout = 5 * time.Second

// maxOutput caps what is returned per section. A busy firewall can produce a lot
// of text, and nothing useful comes of shipping megabytes of it to a browser.
const maxOutput = 256 << 10

// RuleSet is one command's output, as an operator would see it on the server.
type RuleSet struct {
	Name      string `json:"name"`
	Command   string `json:"command"`
	Output    string `json:"output"`
	Error     string `json:"error"`
	Available bool   `json:"available"`
	Truncated bool   `json:"truncated"`
}

// networkCommands are fixed: nothing from a request ever reaches them, so there
// is no argument for the panel to be tricked into running.
var networkCommands = []struct {
	name string
	// argv is tried in order, so a system with only the older tool still works.
	argv [][]string
}{
	// The ufw-user-* chains are where ufw puts the rules an operator actually
	// wrote. A full iptables-save is mostly ufw's own generated scaffolding,
	// which buries them.
	{"ufw-user-input", [][]string{{"iptables", "-S", "ufw-user-input"}}},
	{"ufw-user-forward", [][]string{{"iptables", "-S", "ufw-user-forward"}}},
	{"ufw-user-output", [][]string{{"iptables", "-S", "ufw-user-output"}}},
	{"ip rule", [][]string{{"ip", "rule", "show"}}},
	{"ip route", [][]string{{"ip", "route", "show"}}},
	{"ufw", [][]string{{"ufw", "status", "verbose"}}},
}

// sbinDirs are searched as well as PATH: a systemd unit does not always inherit
// one that includes the administrative directories these tools live in.
var sbinDirs = []string{"/usr/local/sbin", "/usr/sbin", "/sbin", "/usr/local/bin", "/usr/bin", "/bin"}

// NetworkRules reports the firewall and routing configuration of the machine.
// Every command is read-only; nothing here changes the system.
func NetworkRules(ctx context.Context) []RuleSet {
	out := make([]RuleSet, 0, len(networkCommands))
	for _, c := range networkCommands {
		out = append(out, run(ctx, c.name, c.argv))
	}
	return out
}

func run(ctx context.Context, name string, candidates [][]string) RuleSet {
	rs := RuleSet{Name: name}

	if !supported {
		// Nothing is being changed here, so ErrUnsupported's wording would be
		// wrong: these are simply Linux tools.
		rs.Error = "these tools only exist on Linux"
		return rs
	}

	for _, argv := range candidates {
		path, err := lookPath(argv[0])
		if err != nil {
			continue
		}
		rs.Available = true
		rs.Command = strings.Join(argv, " ")

		ctx, cancel := context.WithTimeout(ctx, commandTimeout)
		cmd := exec.CommandContext(ctx, path, argv[1:]...)
		// A predictable environment keeps the output stable and readable.
		cmd.Env = []string{"PATH=" + strings.Join(sbinDirs, ":"), "LC_ALL=C"}
		output, err := cmd.CombinedOutput()
		cancel()

		text := strings.TrimRight(string(output), "\n")
		if len(text) > maxOutput {
			text = text[:maxOutput]
			rs.Truncated = true
		}
		rs.Output = text

		if err != nil {
			// The tool exists but refused. Its own message is on stdout or
			// stderr and is more useful than the exit status, so keep both.
			rs.Error = err.Error()
			if ctx.Err() != nil {
				rs.Error = "timed out after " + commandTimeout.String()
			}
			// Try the next candidate only when the tool is missing entirely,
			// not when it ran and failed.
			return rs
		}
		return rs
	}

	rs.Error = "not installed"
	return rs
}

// lookPath finds a tool on PATH, then in the usual administrative directories.
func lookPath(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, dir := range sbinDirs {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", exec.ErrNotFound
}
