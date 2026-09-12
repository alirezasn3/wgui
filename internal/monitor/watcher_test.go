package monitor

import (
	"context"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"testing"
	"time"

	"wgui/internal/scripts"
	"wgui/internal/store"
)

func newWatcher(t *testing.T) (*Watcher, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewWatcher(st, scripts.NewRunner(st, log), log), st
}

// listener gives a target that is definitely reachable, and a way to make it
// definitely unreachable.
func listener(t *testing.T) (addr string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return l.Addr().String(), func() { l.Close() }
}

func mustMonitor(t *testing.T, st *store.Store, m *store.Monitor) *store.Monitor {
	t.Helper()
	if err := st.CreateMonitor(m); err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	return m
}

func reload(t *testing.T, st *store.Store, id int64) *store.Monitor {
	t.Helper()
	m, err := st.GetMonitor(id)
	if err != nil {
		t.Fatalf("get monitor: %v", err)
	}
	return m
}

// A target with a port is checked by connecting, which says whether the service
// is actually up rather than whether the host answers ping.
func TestCheckUsesTCPWhenThePortIsGiven(t *testing.T) {
	addr, stop := listener(t)

	if p := Check(context.Background(), addr, time.Second); !p.OK {
		t.Fatalf("a live listener was reported down: %v", p.Err)
	}

	stop()
	if p := Check(context.Background(), addr, time.Second); p.OK {
		t.Fatal("a closed port was reported up")
	}
}

func TestCheckRejectsAnEmptyTarget(t *testing.T) {
	if p := Check(context.Background(), "   ", time.Second); p.OK || p.Err == nil {
		t.Fatal("an empty target should be an error, not a pass")
	}
}

func TestWatcherRunsTheScriptAfterEnoughFailures(t *testing.T) {
	w, st := newWatcher(t)

	script := &store.Script{Name: "recover", Body: "echo recovering", TimeoutSec: 10}
	if err := st.CreateScript(script); err != nil {
		t.Fatalf("create script: %v", err)
	}

	addr, stop := listener(t)
	stop() // nothing is listening, so every check fails

	m := mustMonitor(t, st, &store.Monitor{
		Name: "link", Target: addr, IntervalSec: 5, TimeoutSec: 1,
		FailuresBefore: 3, ScriptID: script.ID, RearmOnRecovery: true, Enabled: true,
	})

	// Two failures are below the threshold, so nothing should run yet.
	for i := 1; i <= 2; i++ {
		w.probe(context.Background(), reload(t, st, m.ID))
		got := reload(t, st, m.ID)
		if got.ConsecutiveFailures != i {
			t.Fatalf("after %d checks failures = %d", i, got.ConsecutiveFailures)
		}
		if got.FiredAt != 0 {
			t.Fatalf("fired after %d failures, want it to wait for 3", i)
		}
	}
	if run, _ := st.LastScriptRun(script.ID); run != nil {
		t.Fatal("the script ran before the threshold was reached")
	}

	// The third crosses it.
	w.probe(context.Background(), reload(t, st, m.ID))
	got := reload(t, st, m.ID)
	if got.FiredAt == 0 {
		t.Fatal("the monitor did not fire on the third failure")
	}
	run, err := st.LastScriptRun(script.ID)
	if err != nil || run == nil {
		t.Fatalf("the script did not run: %v", err)
	}
	if run.Source != scripts.SourceMonitor || run.Actor != "link" {
		t.Errorf("run = source %q, actor %q; want it attributed to the monitor", run.Source, run.Actor)
	}
}

// One outage should run the script once, not on every tick for as long as it
// lasts.
func TestWatcherDoesNotRepeatWhileStillDown(t *testing.T) {
	w, st := newWatcher(t)

	script := &store.Script{Name: "once", Body: "echo tick", TimeoutSec: 10}
	if err := st.CreateScript(script); err != nil {
		t.Fatalf("create script: %v", err)
	}

	addr, stop := listener(t)
	stop()

	m := mustMonitor(t, st, &store.Monitor{
		Name: "flaky", Target: addr, IntervalSec: 5, TimeoutSec: 1,
		FailuresBefore: 1, ScriptID: script.ID, RearmOnRecovery: true, Enabled: true,
	})

	for i := 0; i < 4; i++ {
		w.probe(context.Background(), reload(t, st, m.ID))
	}

	runs, err := st.ScriptRuns(script.ID, 10)
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("the script ran %d times during one outage, want 1", len(runs))
	}
}

// Recovering must clear the failure count and re-arm, so the next outage fires
// again.
func TestWatcherRearmsAfterRecovery(t *testing.T) {
	w, st := newWatcher(t)

	script := &store.Script{Name: "again", Body: "echo tick", TimeoutSec: 10}
	if err := st.CreateScript(script); err != nil {
		t.Fatalf("create script: %v", err)
	}

	addr, stop := listener(t)
	stop()

	m := mustMonitor(t, st, &store.Monitor{
		Name: "link", Target: addr, IntervalSec: 5, TimeoutSec: 1,
		FailuresBefore: 1, ScriptID: script.ID, RearmOnRecovery: true, Enabled: true,
	})

	w.probe(context.Background(), reload(t, st, m.ID))
	if reload(t, st, m.ID).FiredAt == 0 {
		t.Fatal("the first outage did not fire")
	}

	// Bring the target back.
	recovered, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("could not rebind %s: %v", addr, err)
	}
	w.probe(context.Background(), reload(t, st, m.ID))

	got := reload(t, st, m.ID)
	if got.ConsecutiveFailures != 0 || got.FiredAt != 0 {
		t.Fatalf("after recovery: failures %d, fired %d; want both cleared",
			got.ConsecutiveFailures, got.FiredAt)
	}
	if got.LastOKAt == 0 {
		t.Error("recovery was not recorded")
	}

	// A second outage has to fire again.
	recovered.Close()
	w.probe(context.Background(), reload(t, st, m.ID))

	runs, _ := st.ScriptRuns(script.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("the script ran %d times across two outages, want 2", len(runs))
	}
}

// A monitor with no script is a plain reachability check; it must still record
// state rather than erroring.
func TestWatcherWithoutAScriptJustWatches(t *testing.T) {
	w, st := newWatcher(t)

	addr, stop := listener(t)
	stop()

	m := mustMonitor(t, st, &store.Monitor{
		Name: "watch only", Target: addr, IntervalSec: 5, TimeoutSec: 1,
		FailuresBefore: 1, Enabled: true,
	})

	w.probe(context.Background(), reload(t, st, m.ID))
	got := reload(t, st, m.ID)
	if got.ConsecutiveFailures != 1 {
		t.Errorf("failures = %d, want 1", got.ConsecutiveFailures)
	}
	if got.LastError == "" {
		t.Error("the failure was not described")
	}
}

// Disabled monitors are skipped entirely.
func TestSweepSkipsDisabledMonitors(t *testing.T) {
	w, st := newWatcher(t)

	addr, stop := listener(t)
	stop()
	m := mustMonitor(t, st, &store.Monitor{
		Name: "off", Target: addr, IntervalSec: 5, TimeoutSec: 1,
		FailuresBefore: 1, Enabled: false,
	})

	w.sweep(context.Background())
	time.Sleep(200 * time.Millisecond)

	if got := reload(t, st, m.ID); got.LastCheckedAt != 0 {
		t.Error("a disabled monitor was probed")
	}
}
