// Package scripts runs the shell scripts an operator has saved in the panel.
//
// These run as whatever wgui runs as, which is root — the same privilege that
// lets the panel configure WireGuard and change kernel settings. Only admins can
// reach them, every run is recorded with who or what set it off, and a script
// cannot be started again while it is still running.
package scripts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"wgui/internal/store"
)

// ErrAlreadyRunning is returned when a script is asked to start while a previous
// run of the same script is still going.
var ErrAlreadyRunning = errors.New("this script is already running")

// maxOutput caps what is kept from a run. A script that prints without end
// should not fill the database.
const maxOutput = 128 << 10

// defaultTimeout applies when a script does not set its own.
const defaultTimeout = 60 * time.Second

// maxTimeout bounds what a script may ask for, so a mistyped timeout cannot pin
// a process open indefinitely.
const maxTimeout = 30 * time.Minute

// Sources a run can come from.
const (
	SourceManual  = "manual"
	SourceMonitor = "monitor"
)

type Runner struct {
	store *store.Store
	log   *slog.Logger

	mu      sync.Mutex
	running map[int64]context.CancelFunc
}

func NewRunner(st *store.Store, log *slog.Logger) *Runner {
	return &Runner{store: st, log: log, running: map[int64]context.CancelFunc{}}
}

// Running reports which scripts are executing right now.
func (r *Runner) Running() map[int64]bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make(map[int64]bool, len(r.running))
	for id := range r.running {
		out[id] = true
	}
	return out
}

// Cancel stops a running script.
func (r *Runner) Cancel(scriptID int64) bool {
	r.mu.Lock()
	cancel, ok := r.running[scriptID]
	r.mu.Unlock()

	if ok {
		cancel()
	}
	return ok
}

// Run executes a script and records the result. It blocks until the script
// finishes or its timeout expires.
func (r *Runner) Run(ctx context.Context, script *store.Script, source, actor string) (*store.ScriptRun, error) {
	timeout := time.Duration(script.TimeoutSec) * time.Second
	switch {
	case timeout <= 0:
		timeout = defaultTimeout
	case timeout > maxTimeout:
		timeout = maxTimeout
	}

	// The timeout has to outlive the request that asked for it, so a browser
	// navigating away does not kill a half-finished script.
	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)

	r.mu.Lock()
	if _, busy := r.running[script.ID]; busy {
		r.mu.Unlock()
		cancel()
		return nil, ErrAlreadyRunning
	}
	r.running[script.ID] = cancel
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.running, script.ID)
		r.mu.Unlock()
		cancel()
	}()

	run := &store.ScriptRun{
		ScriptID:  script.ID,
		StartedAt: time.Now().UnixMilli(),
		Source:    source,
		Actor:     actor,
	}
	r.log.Info("running script", "script", script.Name, "source", source, "actor", actor)

	output, exitCode, runErr := execute(runCtx, script.Body)
	run.EndedAt = time.Now().UnixMilli()
	run.ExitCode = exitCode
	run.Output = output

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		run.Error = fmt.Sprintf("timed out after %s", timeout)
	case errors.Is(runCtx.Err(), context.Canceled):
		run.Error = "cancelled"
	case runErr != nil:
		run.Error = runErr.Error()
	}

	if err := r.store.RecordScriptRun(run); err != nil {
		r.log.Error("recording the script run failed", "script", script.Name, "error", err)
	}

	r.log.Info("script finished", "script", script.Name,
		"exit", run.ExitCode, "ms", run.EndedAt-run.StartedAt, "error", run.Error)
	return run, nil
}

// execute feeds the script to a shell on standard input, so nothing is written
// to disk to run it.
func execute(ctx context.Context, body string) (output string, exitCode int, err error) {
	shell := shellPath()

	cmd := exec.CommandContext(ctx, shell, "-s")
	cmd.Stdin = strings.NewReader(body)

	// Give the script its own process group and kill the group, not just the
	// shell. A script that starts a child — the common case, since that is what
	// a script is for — would otherwise leave it orphaned, still holding the
	// output pipe open, and Wait would block until it happened to finish.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// And stop waiting on the pipes shortly after, in case anything escaped the
	// group and is still holding them.
	cmd.WaitDelay = 2 * time.Second
	// A predictable environment: a script should not inherit whatever happened
	// to be set when the panel was started.
	cmd.Env = []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME=/root",
		"LC_ALL=C",
		"WGUI=1",
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err = cmd.Run()

	out := buf.String()
	if len(out) > maxOutput {
		out = out[:maxOutput] + "\n… output truncated"
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// A non-zero exit is the script's own result, not a failure to run it.
		return out, exitErr.ExitCode(), nil
	}
	if err != nil {
		return out, -1, err
	}
	return out, 0, nil
}

// shellPath prefers bash, because a script pasted in with a #!/bin/bash line
// expects bash behaviour and would fail confusingly under dash.
func shellPath() string {
	for _, candidate := range []string{"/bin/bash", "/usr/bin/bash"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	return "/bin/sh"
}
