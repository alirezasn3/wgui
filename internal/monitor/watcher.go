package monitor

import (
	"context"
	"log/slog"
	"time"

	"wgui/internal/scripts"
	"wgui/internal/store"
)

// Watcher checks every enabled monitor on its own schedule and runs the
// attached script when a destination has failed enough times in a row.
type Watcher struct {
	store  *store.Store
	runner *scripts.Runner
	log    *slog.Logger

	// tick is how often the set of monitors is re-read; each monitor is still
	// only probed once its own interval has elapsed.
	tick time.Duration
	// lastProbe remembers when each monitor was last checked, so changing an
	// interval takes effect without restarting anything.
	lastProbe map[int64]time.Time
}

func NewWatcher(st *store.Store, runner *scripts.Runner, log *slog.Logger) *Watcher {
	return &Watcher{
		store:     st,
		runner:    runner,
		log:       log,
		tick:      time.Second,
		lastProbe: map[int64]time.Time{},
	}
}

// Run watches until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep probes every monitor whose interval has come round.
func (w *Watcher) sweep(ctx context.Context) {
	monitors, err := w.store.ListMonitors()
	if err != nil {
		w.log.Error("reading monitors failed", "error", err)
		return
	}

	live := make(map[int64]bool, len(monitors))
	for _, m := range monitors {
		live[m.ID] = true
		if !m.Enabled {
			continue
		}

		interval := time.Duration(max(m.IntervalSec, 5)) * time.Second
		if last, ok := w.lastProbe[m.ID]; ok && time.Since(last) < interval {
			continue
		}
		w.lastProbe[m.ID] = time.Now()

		// Each monitor gets its own goroutine so a slow destination does not
		// hold up the others.
		go w.probe(ctx, m)
	}

	for id := range w.lastProbe {
		if !live[id] {
			delete(w.lastProbe, id)
		}
	}
}

func (w *Watcher) probe(ctx context.Context, m *store.Monitor) {
	timeout := time.Duration(max(m.TimeoutSec, 1)) * time.Second
	result := Check(ctx, m.Target, timeout)

	outcome := store.MonitorResult{
		ID:        m.ID,
		OK:        result.OK,
		CheckedAt: time.Now().UnixMilli(),
		RTTMS:     result.RTT.Milliseconds(),
	}

	if result.OK {
		if m.ConsecutiveFailures > 0 {
			w.log.Info("monitor recovered", "monitor", m.Name, "target", m.Target,
				"after", m.ConsecutiveFailures)
		}
		if err := w.store.RecordMonitorResult(outcome); err != nil {
			w.log.Error("recording a monitor result failed", "monitor", m.Name, "error", err)
		}
		return
	}

	outcome.Failures = m.ConsecutiveFailures + 1
	if result.Err != nil {
		outcome.Error = result.Err.Error()
	}

	threshold := max(m.FailuresBefore, 1)
	// Fire on the attempt that crosses the threshold, and then stay quiet until
	// the target answers again, so one outage runs the script once.
	shouldFire := outcome.Failures >= threshold && m.ScriptID != 0 &&
		(m.FiredAt == 0 || !m.RearmOnRecovery)

	w.log.Warn("monitor check failed", "monitor", m.Name, "target", m.Target,
		"failures", outcome.Failures, "threshold", threshold, "error", outcome.Error)

	if !shouldFire {
		if err := w.store.RecordMonitorResult(outcome); err != nil {
			w.log.Error("recording a monitor result failed", "monitor", m.Name, "error", err)
		}
		return
	}

	outcome.Fired = true
	if err := w.store.RecordMonitorResult(outcome); err != nil {
		w.log.Error("recording a monitor result failed", "monitor", m.Name, "error", err)
	}

	script, err := w.store.GetScript(m.ScriptID)
	if err != nil {
		w.log.Error("the monitor's script could not be loaded", "monitor", m.Name, "error", err)
		return
	}

	w.log.Warn("monitor is running its script", "monitor", m.Name, "script", script.Name)
	if _, err := w.runner.Run(ctx, script, scripts.SourceMonitor, m.Name); err != nil {
		w.log.Error("the monitor's script did not run", "monitor", m.Name, "error", err)
	}
}
