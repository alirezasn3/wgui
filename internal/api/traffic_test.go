package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"wgui/internal/store"

	"github.com/labstack/echo/v4"
)

// syncAs plays a node syncing with this server, reporting its own traffic.
func (f *fixture) syncAs(nodeID string, recent store.UsageWindows, resetApplied int64) SyncResponse {
	f.t.Helper()
	secret, err := f.store.SyncSecret()
	if err != nil {
		f.t.Fatalf("sync secret: %v", err)
	}
	body := mustJSON(f.t, SyncRequest{
		ServerID: nodeID, Name: nodeID, Recent: recent,
		TrafficResetApplied: resetApplied, TakenAt: time.Now().UnixMilli(),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/sync", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.RemoteAddr = "203.0.113.9:40000"
	rec := httptest.NewRecorder()
	f.echo.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("sync: %d %s", rec.Code, rec.Body.String())
	}
	var out SyncResponse
	f.decode(rec, &out)
	return out
}

// fleetHour is the last hour's traffic the fleet listing shows for a server.
func (f *fixture) fleetHour(id string) int64 {
	f.t.Helper()
	rec := f.as("10.0.0.2", http.MethodGet, "/api/nodes", "")
	var out struct {
		Nodes []store.Node `json:"nodes"`
	}
	f.decode(rec, &out)
	for _, n := range out.Nodes {
		if n.ID == id {
			return n.Recent.Hour
		}
	}
	f.t.Fatalf("server %s is not in the fleet listing", id)
	return 0
}

func TestTrafficResetReachesThisServerAndItsNodes(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", "admin", nil)
	f.peer("dist", "dst", "10.0.0.3/32", "distributor", nil)
	f.peer("user", "usr", "10.0.0.4/32", "user", nil)
	self := f.store.ServerID()

	if err := f.store.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "usr", TX: 700}}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	f.syncAs("node-1", store.UsageWindows{Hour: 900, Day: 900}, 0)
	if f.fleetHour(self) != 700 || f.fleetHour("node-1") != 900 {
		t.Fatalf("before: this server %d, node %d; want 700 and 900", f.fleetHour(self), f.fleetHour("node-1"))
	}

	body := mustJSON(t, trafficResetRequest{ServerIDs: []string{self, "node-1"}})
	for _, ip := range []string{"10.0.0.3", "10.0.0.4"} {
		if rec := f.as(ip, http.MethodPost, "/api/traffic/reset", body); rec.Code != http.StatusForbidden {
			t.Errorf("non-admin %s: got %d, want 403", ip, rec.Code)
		}
	}
	if f.fleetHour(self) != 700 {
		t.Fatal("a refused reset still cleared the counters")
	}

	if rec := f.as("10.0.0.2", http.MethodPost, "/api/traffic/reset", body); rec.Code != http.StatusNoContent {
		t.Fatalf("admin reset: %d %s", rec.Code, rec.Body.String())
	}
	if f.fleetHour(self) != 0 || f.fleetHour("node-1") != 0 {
		t.Errorf("after: this server %d, node %d; want both 0 at once", f.fleetHour(self), f.fleetHour("node-1"))
	}
	var status statusResponse
	f.decode(f.as("10.0.0.2", http.MethodGet, "/api/status", ""), &status)
	if status.Recent != (store.UsageWindows{}) {
		t.Errorf("dashboard shows %+v after the reset, want zero", status.Recent)
	}

	// The node syncs before it has heard: its figures still predate the reset
	// and must not put the old numbers back, and it is told to reset.
	res := f.syncAs("node-1", store.UsageWindows{Hour: 900, Day: 900}, 0)
	if res.TrafficResetAt == 0 {
		t.Fatal("the sync response did not carry the reset to the node")
	}
	if f.fleetHour("node-1") != 0 {
		t.Errorf("node shows %d from a report that predates the reset, want 0", f.fleetHour("node-1"))
	}

	// Once the node says it has reset, what it reports is shown again.
	f.syncAs("node-1", store.UsageWindows{Hour: 15, Day: 15}, res.TrafficResetAt)
	if f.fleetHour("node-1") != 15 {
		t.Errorf("node shows %d after it caught up, want the 15 it reported", f.fleetHour("node-1"))
	}
}

// A node's panel resets its own counters and nothing else: it cannot reach the
// other servers, and its master's figures are the master's to reset.
func TestANodeResetsOnlyItsOwnTraffic(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", "admin", nil)
	f.srv.cfg.Master.URL = "https://master.example.com"

	if err := f.store.ApplyUsageDeltas([]store.UsageDelta{{PeerID: "adm", TX: 50}}); err != nil {
		t.Fatalf("flush: %v", err)
	}
	rec := f.as("10.0.0.2", http.MethodPost, "/api/traffic/reset",
		mustJSON(t, trafficResetRequest{ServerIDs: []string{"some-other-server"}}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("resetting another server from a node: got %d, want 400", rec.Code)
	}
	if rec := f.as("10.0.0.2", http.MethodPost, "/api/traffic/reset", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("own reset: %d %s", rec.Code, rec.Body.String())
	}
	if w, _ := f.store.TrafficWindows(time.Now().UnixMilli()); w.Hour != 0 {
		t.Errorf("hour = %d after the node reset itself, want 0", w.Hour)
	}
}
