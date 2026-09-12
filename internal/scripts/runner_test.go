package scripts

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wgui/internal/store"
)

func newRunner(t *testing.T) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewRunner(st, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

func save(t *testing.T, st *store.Store, name, body string, timeout int) *store.Script {
	t.Helper()
	s := &store.Script{Name: name, Body: body, TimeoutSec: timeout}
	if err := st.CreateScript(s); err != nil {
		t.Fatalf("create script: %v", err)
	}
	return s
}

func TestRunCapturesOutputAndExitCode(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "hello", "echo out; echo err >&2; exit 0", 10)

	run, err := r.Run(context.Background(), script, SourceManual, "admin")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.ExitCode != 0 || run.Error != "" {
		t.Errorf("exit %d, error %q; want a clean run", run.ExitCode, run.Error)
	}
	// Both streams matter: a script's complaint usually goes to stderr.
	for _, want := range []string{"out", "err"} {
		if !strings.Contains(run.Output, want) {
			t.Errorf("output %q is missing %q", run.Output, want)
		}
	}
	if run.Source != SourceManual || run.Actor != "admin" {
		t.Errorf("run = source %q, actor %q", run.Source, run.Actor)
	}
	if run.EndedAt < run.StartedAt {
		t.Error("the run ended before it started")
	}
}

// A script that exits non-zero has reported its own result; that is not a
// failure to run it, and it must be recorded rather than raised.
func TestNonZeroExitIsARecordedResult(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "fails", "echo nope >&2; exit 3", 10)

	run, err := r.Run(context.Background(), script, SourceManual, "admin")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if run.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", run.ExitCode)
	}
	if run.Error != "" {
		t.Errorf("error = %q, want empty: the script ran fine, it just failed", run.Error)
	}
}

func TestRunIsRecordedInHistory(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "recorded", "echo saved", 10)

	if _, err := r.Run(context.Background(), script, SourceMonitor, "link-check"); err != nil {
		t.Fatalf("run: %v", err)
	}

	last, err := st.LastScriptRun(script.ID)
	if err != nil || last == nil {
		t.Fatalf("last run = %v, %v", last, err)
	}
	if !strings.Contains(last.Output, "saved") || last.Source != SourceMonitor {
		t.Errorf("stored run = %+v", last)
	}
}

func TestTimeoutStopsTheScript(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "slow", "sleep 30", 1)

	start := time.Now()
	run, err := r.Run(context.Background(), script, SourceManual, "admin")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("took %s, want it cut off after about a second", elapsed)
	}
	if !strings.Contains(run.Error, "timed out") {
		t.Errorf("error = %q, want a timeout", run.Error)
	}
}

// A script must not be started again while the previous run is still going, or
// an impatient operator clicking twice runs it twice.
func TestConcurrentRunsAreRefused(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "busy", "sleep 2", 30)

	var wg sync.WaitGroup
	started := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		r.Run(context.Background(), script, SourceManual, "admin")
	}()

	<-started
	time.Sleep(300 * time.Millisecond)

	if _, err := r.Run(context.Background(), script, SourceManual, "admin"); err != ErrAlreadyRunning {
		t.Fatalf("second run = %v, want ErrAlreadyRunning", err)
	}
	if running := r.Running(); !running[script.ID] {
		t.Error("the script is not reported as running")
	}

	wg.Wait()
	if running := r.Running(); running[script.ID] {
		t.Error("the script is still reported as running after it finished")
	}
}

func TestCancelStopsARun(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "cancellable", "sleep 30", 60)

	done := make(chan *store.ScriptRun, 1)
	go func() {
		run, _ := r.Run(context.Background(), script, SourceManual, "admin")
		done <- run
	}()

	// Wait for it to actually start before cancelling.
	for i := 0; i < 50 && !r.Running()[script.ID]; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if !r.Cancel(script.ID) {
		t.Fatal("cancel found nothing running")
	}

	select {
	case run := <-done:
		if !strings.Contains(run.Error, "cancelled") {
			t.Errorf("error = %q, want cancelled", run.Error)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop")
	}
}

// The request that started a script may be long gone by the time it finishes;
// that must not kill it.
func TestRunOutlivesTheRequestContext(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "outlives", "sleep 1; echo finished", 30)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *store.ScriptRun, 1)
	go func() {
		run, _ := r.Run(ctx, script, SourceManual, "admin")
		done <- run
	}()

	time.Sleep(200 * time.Millisecond)
	cancel() // the browser navigated away

	select {
	case run := <-done:
		if !strings.Contains(run.Output, "finished") {
			t.Errorf("output = %q, error = %q; want the script to have completed", run.Output, run.Error)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the run never finished")
	}
}

func TestOutputIsCapped(t *testing.T) {
	r, st := newRunner(t)
	// Produced with shell builtins so the fixed PATH cannot matter.
	script := save(t, st, "noisy", `s=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
i=0
while [ $i -lt 14 ]; do s="$s$s"; i=$((i+1)); done
echo "$s"`, 30)

	run, err := r.Run(context.Background(), script, SourceManual, "admin")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(run.Output) > maxOutput+64 {
		t.Errorf("output is %d bytes, over the %d cap", len(run.Output), maxOutput)
	}
	if !strings.Contains(run.Output, "truncated") {
		t.Error("truncation was not signalled")
	}
}

// The history is bounded: this is a panel, not a log store.
func TestHistoryIsPruned(t *testing.T) {
	r, st := newRunner(t)
	script := save(t, st, "repeat", "echo tick", 10)

	for i := 0; i < 25; i++ {
		if _, err := r.Run(context.Background(), script, SourceManual, "admin"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	runs, err := st.ScriptRuns(script.ID, 100)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) > 20 {
		t.Errorf("kept %d runs, want at most 20", len(runs))
	}
}

// The environment is fixed, so a script does not inherit whatever happened to be
// set when the panel started.
func TestEnvironmentIsPredictable(t *testing.T) {
	r, st := newRunner(t)
	t.Setenv("WGUI_LEAKED", "should-not-be-visible")
	script := save(t, st, "env", `echo "leaked=[${WGUI_LEAKED}] wgui=[${WGUI}]"`, 10)

	run, err := r.Run(context.Background(), script, SourceManual, "admin")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(run.Output, "leaked=[]") {
		t.Errorf("output %q shows the panel's environment leaking into the script", run.Output)
	}
	if !strings.Contains(run.Output, "wgui=[1]") {
		t.Errorf("output %q is missing the marker a script can check for", run.Output)
	}
}
