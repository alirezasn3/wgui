package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		newer bool
	}{
		{"v2.1.0", "v2.0.0", true},
		{"v2.0.10", "v2.0.9", true},
		{"v3.0.0", "v2.99.99", true},
		{"v2.0.0", "v2.0.0", false},
		{"v2.0.0", "v2.1.0", false},
		{"v2.1.0", "v2.1.0-rc1", true},
		{"v2.1.0-rc2", "v2.1.0-rc1", true},
		{"v2.1.0-rc1", "v2.0.0", true},
		{"dev", "v2.0.0", false},
		{"v2.1.0", "dev", false},
	} {
		if got := newer(tc.a, tc.b); got != tc.newer {
			t.Errorf("newer(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.newer)
		}
	}

	for v, want := range map[string]bool{
		"v2.0.0":                  true,
		"v2.1.0-rc1":              true,
		"v2.0.0-3-g18beeb9":       false, // built after the tag
		"v2.0.0-dirty":            false, // uncommitted changes
		"v2.0.0-3-g18beeb9-dirty": false,
		"18beeb9":                 false, // no tag at all
		"dev":                     false,
	} {
		if got := isRelease(v); got != want {
			t.Errorf("isRelease(%s) = %v, want %v", v, got, want)
		}
	}
}

// fakeGitHub serves a release listing and each release's files, the way the
// GitHub API and its download host do.
type fakeGitHub struct {
	*httptest.Server
	releases []map[string]any
	files    map[string][]byte // path -> content
	requests []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{files: map[string][]byte{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.requests = append(g.requests, r.URL.Path)
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "User-Agent required", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/repos/owner/wgui/releases" {
			json.NewEncoder(w).Encode(g.releases)
			return
		}
		if b, ok := g.files[r.URL.Path]; ok {
			w.Header().Set("Content-Length", fmt.Sprint(len(b)))
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(g.Close)
	return g
}

// script is a stand-in binary: it answers --version the way wgui does.
func script(version string) []byte {
	return []byte("#!/bin/sh\necho 'wgui " + version + " (abc123, linux/amd64)'\n")
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// publish adds a release with a binary and checksums.
func (g *fakeGitHub) publish(version, notes string, binary []byte, sums string, flags ...string) {
	base := "/dl/" + version + "/"
	g.files[base+"wgui-linux-amd64"] = binary
	if sums == "" {
		sums = sum(binary) + "  wgui-linux-amd64\n" + strings.Repeat("0", 64) + "  wgui-linux-arm64\n"
	}
	g.files[base+"SHA256SUMS"] = []byte(sums)
	rel := map[string]any{
		"tag_name":     version,
		"name":         version,
		"body":         notes,
		"html_url":     "https://github.com/owner/wgui/releases/tag/" + version,
		"published_at": "2026-09-23T10:00:00Z",
		"assets": []map[string]string{
			{"name": "wgui-linux-amd64", "browser_download_url": g.URL + base + "wgui-linux-amd64"},
			{"name": "SHA256SUMS", "browser_download_url": g.URL + base + "SHA256SUMS"},
		},
	}
	for _, f := range flags {
		rel[f] = true
	}
	g.releases = append(g.releases, rel)
}

type installFixture struct {
	u        *Updater
	exe      string
	backups  []string
	restarts chan struct{}
}

func newInstallFixture(t *testing.T, g *fakeGitHub, current string) *installFixture {
	t.Helper()
	dir := t.TempDir()
	f := &installFixture{exe: filepath.Join(dir, "wgui"), restarts: make(chan struct{}, 1)}
	if err := os.WriteFile(f.exe, script(current), 0o755); err != nil {
		t.Fatal(err)
	}
	f.u = New(Options{
		Repo:    "owner/wgui",
		Current: current,
		Exe:     f.exe,
		API:     g.URL,
		GOOS:    "linux",
		GOARCH:  "amd64",
		Backup: func(version string) (string, error) {
			f.backups = append(f.backups, version)
			return "wgui.db.before-" + version, nil
		},
		Restart: func() { f.restarts <- struct{}{} },
	})
	return f
}

// settle waits for an install to stop moving.
func (f *installFixture) settle(t *testing.T) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j := f.u.Status().Job; j.State == StateFailed || j.State == StateRestarting {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("install did not finish: %+v", f.u.Status().Job)
	return Job{}
}

func TestCheckListsNewerReleasesWithTheirChangelog(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish("v2.0.0", "## v2.0.0\n\nThe rewrite.", script("v2.0.0"), "")
	g.publish("v2.1.0", "- Updates from the panel\n\n<!-- wgui:install -->\n## Install\ncurl ...", script("v2.1.0"), "")
	g.publish("v2.2.0", "- Something else", script("v2.2.0"), "")
	g.publish("v2.3.0-rc1", "- Not ready", script("v2.3.0-rc1"), "", "prerelease")
	g.publish("v9.0.0", "- Unpublished", script("v9.0.0"), "", "draft")

	f := newInstallFixture(t, g, "v2.0.0")
	if err := f.u.Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}
	s := f.u.Status()
	if !s.Available || s.Latest != "v2.2.0" {
		t.Fatalf("available=%v latest=%q, want an update to v2.2.0", s.Available, s.Latest)
	}
	var got []string
	for _, r := range s.Releases {
		got = append(got, r.Version)
	}
	if strings.Join(got, ",") != "v2.2.0,v2.1.0" {
		t.Errorf("releases = %v, want every newer release, newest first, without drafts or pre-releases", got)
	}
	if notes := s.Releases[1].Notes; notes != "- Updates from the panel" {
		t.Errorf("notes = %q, want the changelog without the install instructions", notes)
	}
	if s.Unsupported != "" {
		t.Errorf("unsupported = %q on a Linux release build", s.Unsupported)
	}

	// Up to date: nothing to offer, but the latest is still known.
	current := newInstallFixture(t, g, "v2.2.0")
	current.u.Check(context.Background())
	if s := current.u.Status(); s.Available || s.Latest != "v2.2.0" || len(s.Releases) != 0 {
		t.Errorf("on the latest: available=%v latest=%q releases=%d", s.Available, s.Latest, len(s.Releases))
	}
}

func TestAFailedCheckKeepsWhatWasKnown(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish("v2.1.0", "- New", script("v2.1.0"), "")
	f := newInstallFixture(t, g, "v2.0.0")
	if err := f.u.Check(context.Background()); err != nil {
		t.Fatalf("check: %v", err)
	}

	g.Close() // GitHub is unreachable from here now
	if err := f.u.Check(context.Background()); err == nil {
		t.Fatal("a check against an unreachable server succeeded")
	}
	s := f.u.Status()
	if s.CheckError == "" {
		t.Error("the failure is not reported to the panel")
	}
	if !s.Available || s.Latest != "v2.1.0" {
		t.Errorf("available=%v latest=%q after a failed check, want the last good answer kept", s.Available, s.Latest)
	}
}

func TestInstallReplacesTheBinaryAndRestarts(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish("v2.1.0", "- New", script("v2.1.0"), "")
	f := newInstallFixture(t, g, "v2.0.0")
	f.u.Check(context.Background())

	if err := f.u.Install("v2.1.0"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := f.u.Install("v2.1.0"); !errors.Is(err, ErrBusy) && f.u.Status().Job.State != StateRestarting {
		t.Errorf("a second install while one runs: %v, want ErrBusy", err)
	}
	if j := f.settle(t); j.State != StateRestarting {
		t.Fatalf("job = %+v, want it to reach the restart", j)
	}
	select {
	case <-f.restarts:
	case <-time.After(5 * time.Second):
		t.Fatal("the server was not restarted into the new version")
	}

	if b, _ := os.ReadFile(f.exe); string(b) != string(script("v2.1.0")) {
		t.Errorf("binary is now %q, want the new release", b)
	}
	if b, _ := os.ReadFile(f.exe + ".previous"); string(b) != string(script("v2.0.0")) {
		t.Errorf("previous binary is %q, want the one that was running kept for going back", b)
	}
	if len(f.backups) != 1 || f.backups[0] != "v2.1.0" {
		t.Errorf("backups = %v, want the database backed up once before the swap", f.backups)
	}
	if j := f.u.Status().Job; j.Done != j.Total || j.Total == 0 {
		t.Errorf("progress %d/%d, want the whole download counted", j.Done, j.Total)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.exe), ".wgui-update-*"))
	if len(leftovers) != 0 {
		t.Errorf("left behind %v", leftovers)
	}
}

// Each refusal has to leave the running binary exactly as it was: a panel that
// half-installs an update has nothing left to recover with.
func TestInstallRefusesWhatItCannotTrust(t *testing.T) {
	for _, tc := range []struct {
		name   string
		binary []byte
		sums   string
		want   string
	}{
		{
			name:   "checksum mismatch",
			binary: script("v2.1.0"),
			sums:   strings.Repeat("ab", 32) + "  wgui-linux-amd64\n",
			want:   "does not match",
		},
		{
			name:   "no checksum for this architecture",
			binary: script("v2.1.0"),
			sums:   strings.Repeat("ab", 32) + "  wgui-linux-arm64\n",
			want:   "no entry for wgui-linux-amd64",
		},
		{
			name:   "binary claims another version",
			binary: script("v2.0.5"),
			want:   "not wgui v2.1.0",
		},
		{
			name:   "binary does not run",
			binary: []byte("\x7fELF not really"),
			want:   "does not run here",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newFakeGitHub(t)
			g.publish("v2.1.0", "- New", tc.binary, tc.sums)
			f := newInstallFixture(t, g, "v2.0.0")
			f.u.Check(context.Background())

			if err := f.u.Install("v2.1.0"); err != nil {
				t.Fatalf("install: %v", err)
			}
			j := f.settle(t)
			if j.State != StateFailed || !strings.Contains(j.Error, tc.want) {
				t.Fatalf("job = %+v, want a failure mentioning %q", j, tc.want)
			}
			if b, _ := os.ReadFile(f.exe); string(b) != string(script("v2.0.0")) {
				t.Error("the running binary was touched by a refused update")
			}
			if len(f.backups) != 0 || len(f.restarts) != 0 {
				t.Error("a refused update went on to back up or restart")
			}
			leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.exe), ".wgui-update-*"))
			if len(leftovers) != 0 {
				t.Errorf("left behind %v", leftovers)
			}

			// A failure is not a dead end: the job can be tried again.
			if err := f.u.Install("v2.1.0"); err != nil {
				t.Errorf("retry after a failure: %v", err)
			}
		})
	}
}

func TestOnlyReleaseBuildsOnLinuxUpdateThemselves(t *testing.T) {
	g := newFakeGitHub(t)
	g.publish("v2.1.0", "- New", script("v2.1.0"), "")

	dev := newInstallFixture(t, g, "v2.0.0-3-g18beeb9")
	dev.u.Check(context.Background())
	if s := dev.u.Status(); s.Unsupported == "" {
		t.Error("a development build offered to replace itself")
	}
	if err := dev.u.Install("v2.1.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("install on a development build: %v, want ErrUnsupported", err)
	}

	mac := newInstallFixture(t, g, "v2.0.0")
	mac.u.opt.GOOS = "darwin"
	mac.u.Check(context.Background())
	if err := mac.u.Install("v2.1.0"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("install on macOS: %v, want ErrUnsupported", err)
	}

	f := newInstallFixture(t, g, "v2.0.0")
	f.u.Check(context.Background())
	if err := f.u.Install("v2.0.9"); !errors.Is(err, ErrUnknown) {
		t.Errorf("install of a version that was never listed: %v, want ErrUnknown", err)
	}
}
