package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wgui/internal/update"
)

func TestUpdatesAreForAdminsOnly(t *testing.T) {
	f := newFixture(t)
	f.peer("admin", "adm", "10.0.0.2/32", "admin", nil)
	f.peer("dist", "dst", "10.0.0.3/32", "distributor", nil)

	// A GitHub with nothing published yet.
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("[]"))
	}))
	defer gh.Close()
	f.srv.SetUpdater(update.New(update.Options{
		Repo: "owner/wgui", Current: "v2.0.0", Exe: "/opt/wgui/wgui",
		API: gh.URL, GOOS: "linux", GOARCH: "amd64",
	}))

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/update", ""},
		{http.MethodPost, "/api/update/check", ""},
		{http.MethodPost, "/api/update/install", `{"version":"v2.1.0"}`},
	} {
		if rec := f.as("10.0.0.3", tc.method, tc.path, tc.body); rec.Code != http.StatusForbidden {
			t.Errorf("distributor %s %s: got %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	var status update.Status
	rec := f.as("10.0.0.2", http.MethodPost, "/api/update/check", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rec.Code, rec.Body.String())
	}
	f.decode(rec, &status)
	if status.Current != "v2.0.0" || status.Available || status.CheckError != "" {
		t.Errorf("status = %+v, want a successful check that found nothing newer", status)
	}

	// Nothing newer was listed, so there is nothing to install.
	if rec := f.as("10.0.0.2", http.MethodPost, "/api/update/install", `{"version":"v2.1.0"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("installing an unlisted version: got %d, want 400", rec.Code)
	}
	if rec := f.as("10.0.0.2", http.MethodPost, "/api/update/install", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("installing without a version: got %d, want 400", rec.Code)
	}
}
