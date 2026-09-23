// Package update finds newer releases of wgui on GitHub and installs one in
// place of the running binary.
//
// Installing is deliberately conservative, because a panel that updates itself
// into a broken state has also taken away the means of fixing it: the new binary
// is checked against the release's published checksums, run once to prove it
// works on this machine and is the version it claims to be, and only then swapped
// in — with the database backed up and the old binary kept beside it, so going
// back is a matter of moving two files.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// checkEvery is how often a server asks GitHub about new releases. Releases are
// rare, and an operator who wants to know sooner has a button for it.
const checkEvery = 6 * time.Hour

// firstCheckAfter delays the first check after start, so a service caught in a
// restart loop does not ask GitHub on every attempt.
const firstCheckAfter = 30 * time.Second

// installMarker separates a release's changelog from the install instructions
// the release workflow appends after it. Only what comes before it is news to
// somebody who already has wgui installed.
const installMarker = "<!-- wgui:install -->"

// Size limits on what is read from the network, so a misbehaving server cannot
// fill the disk or memory.
const (
	maxListing = 8 << 20
	maxSums    = 64 << 10
	maxBinary  = 256 << 20
)

// Job states.
const (
	StateIdle        = "idle"
	StateDownloading = "downloading"
	StateVerifying   = "verifying"
	StateInstalling  = "installing"
	StateRestarting  = "restarting"
	StateFailed      = "failed"
)

var (
	ErrUnsupported = errors.New("this server cannot update itself")
	ErrBusy        = errors.New("an update is already being installed")
	ErrUnknown     = errors.New("that version is not a newer release; check for updates first")
)

// Release is one published version newer than the running one.
type Release struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	// Notes is the release's changelog, in Markdown.
	Notes       string `json:"notes"`
	URL         string `json:"url"`
	PublishedAt string `json:"publishedAt"`

	assets map[string]string // file name -> download URL
}

// Job is the progress of an install.
type Job struct {
	State   string `json:"state"`
	Version string `json:"version,omitempty"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"`
	Error   string `json:"error,omitempty"`
}

// Status is everything the panel shows about updates.
type Status struct {
	Current string `json:"current"`
	// Latest is the newest published release, "" until a check has succeeded.
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	// Releases are the versions newer than this one, newest first, so the
	// changelog covers everything an update would bring rather than only the
	// last step of it.
	Releases   []Release `json:"releases"`
	CheckedAt  int64     `json:"checkedAt"`
	CheckError string    `json:"checkError,omitempty"`
	// Unsupported says why this server cannot install updates itself, "" when
	// it can.
	Unsupported string `json:"unsupported,omitempty"`
	Job         Job    `json:"job"`
}

// Options wires an Updater to the server it runs in.
type Options struct {
	Repo    string // owner/name on GitHub
	Current string // the running version
	// Exe is the running binary's path, resolved at startup. It has to be
	// captured before any update: once the file has been replaced, the kernel
	// reports the running process's executable as deleted.
	Exe string
	// Backup writes a consistent copy of the database for the given version
	// and returns where. Called just before the binary is swapped.
	Backup func(version string) (string, error)
	// Restart shuts the server down cleanly and starts the binary at Exe.
	Restart func()
	Log     *slog.Logger

	// For tests: where the GitHub API is, which platform to fetch for, and
	// the client to fetch with.
	API    string
	GOOS   string
	GOARCH string
	Client *http.Client
}

type Updater struct {
	opt Options

	mu        sync.Mutex
	latest    string
	releases  []Release
	checkedAt int64
	checkErr  string
	job       Job
}

func New(opt Options) *Updater {
	if opt.API == "" {
		opt.API = "https://api.github.com"
	}
	if opt.GOOS == "" {
		opt.GOOS = runtime.GOOS
	}
	if opt.GOARCH == "" {
		opt.GOARCH = runtime.GOARCH
	}
	if opt.Client == nil {
		opt.Client = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		}}
	}
	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}
	return &Updater{opt: opt, job: Job{State: StateIdle}}
}

// Run checks for updates now and then until ctx is cancelled.
func (u *Updater) Run(ctx context.Context) {
	t := time.NewTimer(firstCheckAfter)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := u.Check(ctx); err != nil {
				u.opt.Log.Warn("checking for updates failed", "error", err)
			}
			t.Reset(checkEvery)
		}
	}
}

// ghRelease is the part of GitHub's release object that matters here.
type ghRelease struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub which releases exist. A failure is recorded for the panel
// to show and leaves what was learned last time in place.
func (u *Updater) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	list, err := u.fetchReleases(ctx)

	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkedAt = time.Now().UnixMilli()
	if err != nil {
		u.checkErr = err.Error()
		return err
	}
	u.checkErr = ""

	u.latest = ""
	u.releases = nil
	for _, r := range list {
		// A pre-release is for whoever went looking for it, not something to
		// put in front of every operator.
		if r.Draft || r.Prerelease {
			continue
		}
		if _, ok := parseVersion(r.TagName); !ok {
			continue
		}
		if u.latest == "" || newer(r.TagName, u.latest) {
			u.latest = r.TagName
		}
		if !newer(r.TagName, u.opt.Current) {
			continue
		}
		rel := Release{
			Version:     r.TagName,
			Name:        r.Name,
			Notes:       changelog(r.Body),
			URL:         r.HTMLURL,
			PublishedAt: r.PublishedAt,
			assets:      map[string]string{},
		}
		for _, a := range r.Assets {
			rel.assets[a.Name] = a.URL
		}
		u.releases = append(u.releases, rel)
	}
	sort.Slice(u.releases, func(i, j int) bool {
		return newer(u.releases[i].Version, u.releases[j].Version)
	})
	return nil
}

func (u *Updater) fetchReleases(ctx context.Context) ([]ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=30", strings.TrimSuffix(u.opt.API, "/"), u.opt.Repo)
	body, _, err := u.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(body, maxListing)).Decode(&list); err != nil {
		return nil, fmt.Errorf("reading the release list: %w", err)
	}
	return list, nil
}

// changelog is the part of a release's notes that describes the release,
// without the install instructions that follow it.
func changelog(body string) string {
	if i := strings.Index(body, installMarker); i >= 0 {
		body = body[:i]
	}
	return strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
}

// Status reports what is known about updates right now.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := Status{
		Current:     u.opt.Current,
		Latest:      u.latest,
		Releases:    append([]Release{}, u.releases...),
		CheckedAt:   u.checkedAt,
		CheckError:  u.checkErr,
		Unsupported: u.unsupported(),
		Job:         u.job,
	}
	s.Available = len(s.Releases) > 0
	return s
}

// unsupported says why this server cannot install an update itself, or "".
func (u *Updater) unsupported() string {
	switch {
	case u.opt.GOOS != "linux":
		return "updating in place is only supported on Linux, which is what releases are built for"
	case u.opt.GOARCH != "amd64" && u.opt.GOARCH != "arm64":
		return fmt.Sprintf("releases are not built for %s", u.opt.GOARCH)
	case !isRelease(u.opt.Current):
		return fmt.Sprintf("this is a development build (%s); only release builds update themselves", u.opt.Current)
	case u.opt.Exe == "":
		return "the running binary could not be located"
	}
	return ""
}

func (s Job) active() bool {
	switch s.State {
	case StateDownloading, StateVerifying, StateInstalling, StateRestarting:
		return true
	}
	return false
}

// Install starts installing a release in the background. Its progress is in
// Status; on success the server restarts into the new version.
func (u *Updater) Install(version string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	if why := u.unsupported(); why != "" {
		return fmt.Errorf("%w: %s", ErrUnsupported, why)
	}
	if u.job.active() {
		return ErrBusy
	}
	var rel *Release
	for i := range u.releases {
		if u.releases[i].Version == version {
			rel = &u.releases[i]
		}
	}
	if rel == nil {
		return ErrUnknown
	}

	u.job = Job{State: StateDownloading, Version: rel.Version}
	go u.install(*rel)
	return nil
}

func (u *Updater) install(rel Release) {
	u.opt.Log.Info("installing update", "from", u.opt.Current, "to", rel.Version)
	if err := u.doInstall(rel); err != nil {
		u.opt.Log.Error("update failed", "version", rel.Version, "error", err)
		u.mu.Lock()
		u.job.State, u.job.Error = StateFailed, err.Error()
		u.mu.Unlock()
		return
	}
	u.opt.Log.Info("update installed, restarting", "version", rel.Version)
	if u.opt.Restart != nil {
		u.opt.Restart()
	}
}

func (u *Updater) setState(state string) {
	u.mu.Lock()
	u.job.State = state
	u.mu.Unlock()
}

func (u *Updater) doInstall(rel Release) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	name := fmt.Sprintf("wgui-%s-%s", u.opt.GOOS, u.opt.GOARCH)
	binURL, ok := rel.assets[name]
	if !ok {
		return fmt.Errorf("release %s has no %s", rel.Version, name)
	}
	sumsURL, ok := rel.assets["SHA256SUMS"]
	if !ok {
		return fmt.Errorf("release %s publishes no SHA256SUMS, so its binary cannot be checked", rel.Version)
	}
	want, err := u.checksum(ctx, sumsURL, name)
	if err != nil {
		return err
	}

	// Written beside the binary so the final step is a rename within one
	// directory, which either happens completely or not at all.
	dir := filepath.Dir(u.opt.Exe)
	tmp, err := os.CreateTemp(dir, ".wgui-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to the binary in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	installed := false
	defer func() {
		if !installed {
			os.Remove(tmpPath)
		}
	}()

	got, err := u.download(ctx, binURL, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("the downloaded %s does not match the release's checksum (got %s, want %s)", name, got, want)
	}

	u.setState(StateVerifying)
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	if err := u.selfCheck(ctx, tmpPath, rel.Version); err != nil {
		return err
	}

	u.setState(StateInstalling)
	if u.opt.Backup != nil {
		path, err := u.opt.Backup(rel.Version)
		if err != nil {
			return fmt.Errorf("backing up the database: %w", err)
		}
		u.opt.Log.Info("database backed up before the update", "path", path)
	}
	previous := u.opt.Exe + ".previous"
	if err := keepPrevious(u.opt.Exe, previous); err != nil {
		return fmt.Errorf("keeping the current binary as %s: %w", previous, err)
	}
	if err := os.Rename(tmpPath, u.opt.Exe); err != nil {
		return fmt.Errorf("putting the new binary in place: %w", err)
	}
	installed = true
	u.setState(StateRestarting)
	return nil
}

// checksum reads the release's SHA256SUMS and returns the entry for name.
func (u *Updater) checksum(ctx context.Context, url, name string) (string, error) {
	body, _, err := u.get(ctx, url, "")
	if err != nil {
		return "", fmt.Errorf("fetching SHA256SUMS: %w", err)
	}
	defer body.Close()

	sc := bufio.NewScanner(io.LimitReader(body, maxSums))
	for sc.Scan() {
		// "<hex>  <name>", or "<hex> *<name>" for a binary-mode sum.
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			sum := strings.ToLower(fields[0])
			if len(sum) != sha256.Size*2 {
				return "", fmt.Errorf("SHA256SUMS has a malformed entry for %s", name)
			}
			return sum, nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading SHA256SUMS: %w", err)
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", name)
}

// download writes url into w, recording progress, and returns the SHA-256 of
// what it wrote.
func (u *Updater) download(ctx context.Context, url string, w io.Writer) (string, error) {
	body, size, err := u.get(ctx, url, "")
	if err != nil {
		return "", fmt.Errorf("downloading the update: %w", err)
	}
	defer body.Close()
	if size > maxBinary {
		return "", fmt.Errorf("the download is %d bytes, more than a wgui binary should ever be", size)
	}

	u.mu.Lock()
	u.job.Total, u.job.Done = size, 0
	u.mu.Unlock()

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, hash, progress{u}), io.LimitReader(body, maxBinary+1))
	if err != nil {
		return "", fmt.Errorf("downloading the update: %w", err)
	}
	if n > maxBinary {
		return "", errors.New("the download is larger than a wgui binary should ever be")
	}
	if size > 0 && n != size {
		return "", fmt.Errorf("the download ended after %d of %d bytes", n, size)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type progress struct{ u *Updater }

func (p progress) Write(b []byte) (int, error) {
	p.u.mu.Lock()
	p.u.job.Done += int64(len(b))
	p.u.mu.Unlock()
	return len(b), nil
}

// selfCheck runs the new binary once. It proves the file executes on this
// machine at all — the right architecture, not truncated — and that it is the
// version it was downloaded as.
func (u *Updater) selfCheck(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new binary does not run here: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	if got := strings.TrimSpace(string(out)); !strings.HasPrefix(got, "wgui "+version+" ") {
		return fmt.Errorf("the new binary reports %q, not wgui %s", got, version)
	}
	return nil
}

// keepPrevious leaves the running binary at previous, replacing whatever an
// earlier update left there. A hard link costs nothing and leaves the binary
// in place until the rename replaces it; a copy is the fallback where links
// are not allowed.
func keepPrevious(exe, previous string) error {
	if err := os.Remove(previous); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(exe, previous); err == nil {
		return nil
	}
	src, err := os.Open(exe)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(previous, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(previous)
		return err
	}
	return dst.Close()
}

// get fetches url, failing on anything but a 200.
func (u *Updater) get(ctx context.Context, url, accept string) (io.ReadCloser, int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	// GitHub refuses API requests without a User-Agent.
	req.Header.Set("User-Agent", "wgui/"+u.opt.Current)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := u.opt.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		res.Body.Close()
		return nil, 0, fmt.Errorf("%s answered %s: %s", req.URL.Host, res.Status, strings.TrimSpace(string(msg)))
	}
	return res.Body, res.ContentLength, nil
}
