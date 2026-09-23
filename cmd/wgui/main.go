// Command wgui serves the WireGuard management panel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"wgui/internal/api"
	"wgui/internal/certs"
	"wgui/internal/config"
	"wgui/internal/engine"
	"wgui/internal/mongomig"
	"wgui/internal/monitor"
	"wgui/internal/nodesync"
	"wgui/internal/scripts"
	"wgui/internal/store"
	"wgui/internal/system"
	"wgui/internal/update"
	"wgui/internal/version"
	"wgui/internal/wgdev"
)

func main() {
	var (
		install    = flag.Bool("install", false, "create the wgui systemd service and exit")
		uninstall  = flag.Bool("uninstall", false, "remove the wgui systemd service and exit")
		repair     = flag.Bool("repair", false, "rebuild the database's indexes and exit")
		fakeWG     = flag.Bool("fake-wg", false, "use an in-memory WireGuard device, for developing the panel on a machine without WireGuard")
		peersFile  = flag.String("import-peers", "", "import peers from a mongoexport file and exit")
		groupsFile = flag.String("import-groups", "", "groups mongoexport file to import alongside --import-peers")
		force      = flag.Bool("force", false, "allow the import to run against a database that already has peers")
		devHTTP    = flag.Bool("dev-http", false, "serve plain HTTP instead of TLS, for local development only")
		verbose    = flag.Bool("verbose", false, "log at debug level")
		showVer    = flag.Bool("version", false, "print the version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version.String())
		return
	}

	// Where this binary lives has to be read now: once an update has replaced
	// the file, the kernel reports the running process's executable as
	// deleted, and the update needs the path to start the new one from.
	self, selfErr := resolveSelf()

	err := run(options{
		install:    *install,
		uninstall:  *uninstall,
		repair:     *repair,
		fakeWG:     *fakeWG,
		devHTTP:    *devHTTP,
		peersFile:  *peersFile,
		groupsFile: *groupsFile,
		force:      *force,
		verbose:    *verbose,
		self:       self,
	})
	if errors.Is(err, errRestart) {
		restartInto(self, selfErr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// updatePlatform is the operating system the updater fetches releases for;
// empty means this one. Only the updatee2e build sets it, see update_e2e.go.
var updatePlatform string

// errRestart is how run says an update has been installed and this process
// should become the new binary.
var errRestart = errors.New("restarting into the update")

// resolveSelf is the path of the running binary, with symlinks followed so the
// update replaces the file itself rather than a link to it.
func resolveSelf() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// restartInto replaces this process with the binary now at self, keeping its
// arguments, environment and process ID — so systemd sees the same service
// carry on rather than one that stopped. If that cannot be done, exiting with
// an error has systemd start the service again, which runs the new binary all
// the same.
func restartInto(self string, resolveErr error) {
	if resolveErr != nil {
		fmt.Fprintln(os.Stderr, "error: cannot restart into the update:", resolveErr)
		os.Exit(1)
	}
	err := syscall.Exec(self, os.Args, os.Environ())
	fmt.Fprintln(os.Stderr, "error: cannot restart into the update:", err)
	os.Exit(1)
}

// options collects the command line, so adding a flag does not mean threading
// another positional argument through.
type options struct {
	install    bool
	uninstall  bool
	repair     bool
	fakeWG     bool
	devHTTP    bool
	peersFile  string
	groupsFile string
	force      bool
	verbose    bool
	// self is the running binary's path, for updates. Empty when it could not
	// be determined, which leaves updating unavailable rather than guessing.
	self string
}

func run(opt options) error {
	install, uninstall, fakeWG, devHTTP := opt.install, opt.uninstall, opt.fakeWG, opt.devHTTP
	verbose := opt.verbose

	if install {
		return installService()
	}
	if uninstall {
		if err := system.UninstallService(); err != nil {
			return err
		}
		fmt.Println("wgui service removed")
		return nil
	}

	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	log.Info("starting", "version", version.String())

	dir, err := executableDir()
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	log.Info("configuration loaded", "path", filepath.Join(dir, "config.json"))

	if opt.repair {
		return repairDatabase(cfg.Resolve(cfg.DBPath), log)
	}

	// A server being made a node keeps nothing of its own; see the comment on
	// archiveStandaloneDB for why the old file is moved rather than emptied.
	if cfg.Master.IsNode() {
		if err := archiveStandaloneDB(cfg.Resolve(cfg.DBPath), log); err != nil {
			return err
		}
	}

	st, err := store.Open(cfg.Resolve(cfg.DBPath))
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("database ready", "path", cfg.Resolve(cfg.DBPath))

	if opt.peersFile != "" || opt.groupsFile != "" {
		return mongomig.Run(mongomig.Options{
			Store:      st,
			PeersFile:  opt.peersFile,
			GroupsFile: opt.groupsFile,
			Force:      opt.force,
			Log:        log,
		})
	}

	settings, err := st.LoadSettings()
	if err != nil {
		return err
	}
	// The public address is only known from the config on a fresh install, so
	// seed it once rather than making the operator type it twice.
	if settings.PublicAddress == "" && len(settings.Endpoints) == 0 {
		if err := st.SaveSettings(settings); err != nil {
			return err
		}
	}

	dev, err := openDevice(cfg.InterfaceName, fakeWG, log)
	if err != nil {
		return err
	}
	defer dev.Close()

	if err := bootstrapAdmin(st, cfg, dev, log); err != nil {
		return err
	}

	runner := scripts.NewRunner(st, log)
	srv := api.New(st, nil, runner, cfg, settings, log)
	eng := engine.New(st, dev, log, srv.FlushInterval)
	srv.SetEngine(eng)

	if err := eng.Sync(); err != nil {
		return fmt.Errorf("initial device sync: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	engineDone := make(chan struct{})
	go func() {
		eng.Run(ctx)
		close(engineDone)
	}()
	go monitor.NewWatcher(st, runner, log).Run(ctx)

	// An installed update asks for a restart; it is carried out below, after
	// the same orderly shutdown a stop gets, so no counted traffic is lost.
	restart := make(chan struct{})
	var restartOnce sync.Once
	updater := update.New(update.Options{
		Repo:    version.Repo,
		API:     version.ReleaseAPI,
		GOOS:    updatePlatform,
		Current: version.Short(),
		Exe:     opt.self,
		Log:     log,
		Backup: func(v string) (string, error) {
			path := cfg.Resolve(cfg.DBPath) + ".before-" + v
			return path, st.Backup(path)
		},
		Restart: func() { restartOnce.Do(func() { close(restart) }) },
	})
	srv.SetUpdater(updater)
	go updater.Run(ctx)

	// A server with a master is a node: it serves that master's peers and
	// reports what it has counted, rather than being edited in its own right.
	if cfg.Master.IsNode() {
		sync, err := nodesync.New(st, cfg, log)
		if err != nil {
			return err
		}
		sync.OnSettingsChanged(srv.ReloadSettings)
		sync.OnSynced(eng.Kick)
		sync.OnName(srv.LocalName)
		// Writes made on this node's panel travel to the master rather than
		// being applied here and replaced at the next sync.
		srv.SetForwarder(sync)
		sync.OnLiveTotals(func() (int64, int64, int64) {
			t := eng.Totals()
			return int64(t.Online), t.TXSpeed, t.RXSpeed
		})
		go sync.Run(ctx)
	} else if secret, err := st.SyncSecret(); err != nil {
		return err
	} else {
		log.Debug("nodes may sync with this server", "secret", secret)
	}

	e := srv.Handler()

	certPEM, keyPEM, err := resolveTLS(cfg, settings, st, log)
	if err != nil {
		return err
	}
	srv.SetCertFingerprint(certs.Fingerprint(certPEM))

	serveErr := make(chan error, 1)
	go func() {
		if devHTTP {
			// Only for working on the panel locally; the real thing always
			// speaks TLS.
			log.Warn("serving plain HTTP because --dev-http was passed")
			log.Info("listening", "address", cfg.ListenAddress, "tls", false)
			serveErr <- e.Start(cfg.ListenAddress)
			return
		}
		log.Info("listening", "address", cfg.ListenAddress, "tls", true)
		serveErr <- e.StartTLS(cfg.ListenAddress, certPEM, keyPEM)
	}()

	restarting := false
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		log.Info("shutting down")
	case <-restart:
		log.Info("shutting down to restart into the update")
		restarting = true
	}

	stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "error", err)
	}
	// The engine writes what it has counted since the last flush on its way
	// out; wait for that rather than for a guess at how long it takes.
	select {
	case <-engineDone:
	case <-time.After(10 * time.Second):
		log.Error("the engine did not finish its final flush in time")
	}
	if restarting {
		return errRestart
	}
	return nil
}

func openDevice(name string, fake bool, log *slog.Logger) (wgdev.Device, error) {
	if fake {
		log.Warn("using a fake WireGuard device; no real traffic is being managed", "interface", name)
		return wgdev.OpenStub(name), nil
	}
	dev, err := wgdev.Open(name)
	if err != nil {
		return nil, fmt.Errorf("%w (use --fake-wg to run the panel without a real interface)", err)
	}
	return dev, nil
}

func installService() error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	// Resolve the link a packaged binary is often reached through, so the unit
	// names the real file and survives that link being replaced.
	if resolved, err := filepath.EvalSymlinks(execPath); err == nil {
		execPath = resolved
	}
	if err := system.InstallService(execPath); err != nil {
		return err
	}
	fmt.Println("wgui service installed, enabled at boot and started")
	fmt.Println("check it with: systemctl status wgui")
	return nil
}

func executableDir() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(execPath), nil
}

// resolveTLS picks the certificate to serve with. Nothing reaches the disk: an
// operator's own certificate is read into memory, and a generated one is kept in
// the database, or nowhere at all in ephemeral mode.
func resolveTLS(cfg *config.Config, settings config.Settings, st *store.Store, log *slog.Logger) (certPEM, keyPEM []byte, err error) {
	configured := cfg.TLS.Cert != "" && cfg.TLS.Key != ""

	opt := certs.Options{
		Store:     st,
		Ephemeral: cfg.TLS.Ephemeral,
		// Name the certificate after every address the panel is actually
		// reached on, so the hostname matches for anyone who checks — a node
		// syncing with its master most of all, since it is the only client that
		// refuses outright rather than asking a human to click through.
		Hosts: append([]string{cfg.InterfaceAddress, settings.PublicAddress},
			settings.Endpoints...),
	}
	if configured {
		opt.CertPath, opt.KeyPath = cfg.Resolve(cfg.TLS.Cert), cfg.Resolve(cfg.TLS.Key)
	}

	certPEM, keyPEM, src, err := certs.Ensure(opt)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare TLS certificate: %w", err)
	}

	if configured && src != certs.FromConfig {
		log.Warn("the configured certificate could not be used, falling back to a self-signed one",
			"cert", opt.CertPath, "key", opt.KeyPath)
	}

	switch src {
	case certs.FromConfig:
		log.Info("serving TLS with the configured certificate", "cert", opt.CertPath)
	case certs.FromStore:
		log.Info("serving TLS with the stored self-signed certificate",
			"fingerprint", certs.Fingerprint(certPEM))
	case certs.Generated:
		log.Warn("generated a self-signed certificate and stored it in the database",
			"fingerprint", certs.Fingerprint(certPEM))
		log.Warn("browsers will warn about it once; set tls.cert and tls.key in config.json to use your own")
	case certs.Ephemeral:
		log.Warn("generated a throwaway self-signed certificate; it changes at every restart",
			"fingerprint", certs.Fingerprint(certPEM))
	}
	return certPEM, keyPEM, nil
}
