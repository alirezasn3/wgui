// Package nodesync keeps a node's peers and usage in step with its master.
//
// The node does all the talking. It sends what it has counted and receives the
// peers to serve plus what every other server has counted, in one exchange, so
// nothing has to reach the node from outside — no inbound port, no keepalive to
// hold a path open, and a node behind NAT works the same as one that is not.
package nodesync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"wgui/internal/api"
	"wgui/internal/config"
	"wgui/internal/store"
	"wgui/internal/version"
)

// DefaultInterval is how often a node syncs when the config does not say.
const DefaultInterval = 15 * time.Second

type Client struct {
	store    *store.Store
	log      *slog.Logger
	url      string
	secret   string
	interval time.Duration
	http     *http.Client
	// name is read at each sync rather than captured once: a server is named
	// after the address it answers on, which is a setting and can change.
	name func() string
	// onSettings is called after a sync that changed the settings, so the panel
	// can drop the copy it is holding in memory.
	onSettings func() error
	// liveTotals reports what this server is carrying right now, so the master
	// can show the fleet's state without polling each server in turn.
	liveTotals func() (online int64, tx, rx int64)
	// onSynced is called after every sync that landed, so whatever the master
	// sent — a peer switched off there, another server's count taking a peer
	// over its quota — reaches the device now rather than at the next tick.
	onSynced func()
}

// OnSynced registers what to call once a sync has been applied.
func (c *Client) OnSynced(fn func()) { c.onSynced = fn }

// OnName registers where to read this server's own name from.
func (c *Client) OnName(fn func() string) { c.name = fn }

// OnLiveTotals registers where to read this server's current load from.
func (c *Client) OnLiveTotals(fn func() (int64, int64, int64)) { c.liveTotals = fn }

// OnSettingsChanged registers what to call when a sync brings new settings.
func (c *Client) OnSettingsChanged(fn func() error) { c.onSettings = fn }

// New builds the client for a server configured with a master.
func New(st *store.Store, cfg *config.Config, log *slog.Logger) (*Client, error) {
	m := cfg.Master
	if !m.IsNode() {
		return nil, errors.New("this server has no master")
	}
	tr, err := transport(m)
	if err != nil {
		return nil, err
	}
	interval := DefaultInterval
	if m.IntervalSeconds > 0 {
		interval = time.Duration(m.IntervalSeconds) * time.Second
	}
	return &Client{
		store:    st,
		log:      log,
		url:      strings.TrimSuffix(m.URL, "/") + "/api/sync",
		secret:   m.Secret,
		interval: interval,
		http: &http.Client{
			Transport:     tr,
			Timeout:       30 * time.Second,
			CheckRedirect: refuseRedirect,
		},
		name: func() string { return cfg.InterfaceAddress },
	}, nil
}

// watchConn records the address the request is actually delivered to. A name
// can resolve to more than one machine, and a network in the way can answer on
// another's behalf, so "the master gave the wrong answer" and "something that
// is not the master answered" look identical in the error — which is the whole
// question when a node will not sync. The returned pointer is read only after
// the exchange has finished.
func watchConn(req *http.Request) (*http.Request, *string) {
	var remote string
	trace := &httptrace.ClientTrace{
		GotConn: func(i httptrace.GotConnInfo) {
			if i.Conn != nil {
				remote = i.Conn.RemoteAddr().String()
			}
		},
	}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), &remote
}

// withRemote names the address that answered, when one did. An empty remote
// means the connection was never established, which is already clear from the
// error itself.
func withRemote(err error, remote *string) error {
	if remote == nil || *remote == "" {
		return err
	}
	return fmt.Errorf("%w (answered by %s)", err, *remote)
}

// errRedirected is raised instead of following a redirect. A master answers
// /api/sync itself, so a redirect means something else is in front of it — and
// following one would be worse than failing: the standard library turns a POST
// into a bodyless GET on a 301, 302 or 303, which the master then sees as an
// ordinary browser request and refuses for coming from outside the tunnel. The
// secret would also travel to wherever the redirect pointed.
var errRedirected = errors.New("the master's address answered with a redirect")

func refuseRedirect(req *http.Request, _ []*http.Request) error {
	return fmt.Errorf("%w to %s", errRedirected, req.URL)
}

// transport applies the master's certificate policy. A master usually serves
// the self-signed certificate wgui generates for it, which no authority will
// vouch for, so the honest options are to pin its fingerprint or to say plainly
// that verification is off.
func transport(m config.Master) (*http.Transport, error) {
	tr := &http.Transport{}
	switch {
	case m.Fingerprint != "":
		want, err := hex.DecodeString(strings.NewReplacer(":", "", " ", "").Replace(m.Fingerprint))
		if err != nil || len(want) != sha256.Size {
			return nil, fmt.Errorf("master fingerprint %q is not a SHA-256 hash", m.Fingerprint)
		}
		tr.TLSClientConfig = &tls.Config{
			// Verified by fingerprint below instead; anything else would reject
			// the self-signed certificate before we got the chance.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				for _, der := range raw {
					sum := sha256.Sum256(der)
					if bytes.Equal(sum[:], want) {
						return nil
					}
				}
				return errors.New("the master's certificate does not match the pinned fingerprint")
			},
		}
	case m.Insecure:
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return tr, nil
}

// Run syncs until the context is cancelled. A failed sync is logged and
// retried on the next tick: the exchange carries absolute counts, so nothing
// accumulates and nothing is lost by missing one.
func (c *Client) Run(ctx context.Context) {
	c.log.Info("syncing with master", "url", c.url, "every", c.interval)
	t := time.NewTicker(c.interval)
	defer t.Stop()

	// Sync at once rather than waiting out the first interval, so a node that
	// has just started serves the right peers as soon as it can.
	c.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.once(ctx)
		}
	}
}

func (c *Client) once(ctx context.Context) {
	if err := c.sync(ctx); err != nil {
		if hint := diagnose(err); hint != "" {
			c.log.Warn("sync with master failed", "error", err, "hint", hint)
			return
		}
		c.log.Warn("sync with master failed", "error", err)
	}
}

// diagnose turns the transport errors an operator actually hits into a sentence
// about what to change, since the error the standard library raises is accurate
// but says nothing about which end is wrong.
func diagnose(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, errRedirected):
		return "the master's URL is not answered by wgui itself but by something that redirects: " +
			"point master.url straight at the panel, or stop the proxy rewriting it"
	case strings.Contains(msg, "server gave HTTP response to HTTPS client"):
		return "whatever answers that address is not speaking TLS, so it is not this master's panel: " +
			"check that the address above is the master's, that the name does not also resolve " +
			"elsewhere, and that nothing on the way is answering in its place"
	case strings.Contains(msg, "x509: certificate signed by unknown authority"),
		strings.Contains(msg, "x509: certificate is not standards compliant"):
		return "the master is serving the self-signed certificate wgui generated for it; " +
			"put its SHA-256 in the master.fingerprint of this node's config.json"
	case strings.Contains(msg, "does not match the pinned fingerprint"):
		return "the master's certificate has changed since the fingerprint was pinned; " +
			"take the new one from the master's startup log"
	case strings.Contains(msg, "x509: certificate is valid for"):
		return "the certificate is for a different name than the URL asks for; " +
			"use the name the certificate covers, or pin its fingerprint instead"
	case strings.Contains(msg, "401"):
		return "the master rejected the secret; copy it again from the master's Nodes page"
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "no such host"),
		strings.Contains(msg, "i/o timeout"):
		return "the master's address is not reachable from here; check the URL, DNS and the firewall"
	}
	return ""
}

func (c *Client) sync(ctx context.Context) error {
	usage, takenAt, err := c.store.ServerUsageSnapshot()
	if err != nil {
		return err
	}

	counts, err := c.store.CountPeers(store.Scope{Role: "admin"}, time.Now().UnixMilli())
	if err != nil {
		return err
	}

	var online, tx, rx int64
	if c.liveTotals != nil {
		online, tx, rx = c.liveTotals()
	}
	recent, err := c.store.TrafficWindows(time.Now().UnixMilli())
	if err != nil {
		return err
	}
	resetApplied, err := c.store.TrafficResetApplied()
	if err != nil {
		return err
	}

	body, err := json.Marshal(api.SyncRequest{
		ServerID:    c.store.ServerID(),
		Name:        c.name(),
		Version:     version.String(),
		PeerCount:   int64(counts.Total),
		OnlinePeers: online,
		TXSpeed:     tx,
		RXSpeed:     rx,
		Recent:      recent,
		TakenAt:     takenAt,
		Usage:       usage,

		TrafficResetApplied: resetApplied,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.secret)

	req, remote := watchConn(req)
	res, err := c.http.Do(req)
	if err != nil {
		return withRemote(err, remote)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("master answered %s: %s", res.Status, strings.TrimSpace(string(msg)))
	}

	var out api.SyncResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return err
	}
	if out.ServerID == c.store.ServerID() {
		return errors.New("this server and its master have the same identity; one is running a copy of the other's database")
	}

	// Definitions first: the usage rows reference peers, and a reset carried by
	// a definition has to land before counts are applied over it.
	if err := c.store.ApplyDefinitions(store.Definitions{
		Peers:      out.Peers,
		Groups:     out.Groups,
		Visibility: out.Visibility,
		Scripts:    out.Scripts,
		Settings:   out.Settings,
	}); err != nil {
		return fmt.Errorf("apply definitions: %w", err)
	}
	if err := c.store.ApplyRemoteUsage(out.TakenAt, out.Usage); err != nil {
		return fmt.Errorf("apply usage: %w", err)
	}
	if reset, err := c.store.ApplyTrafficReset(out.TrafficResetAt); err != nil {
		return fmt.Errorf("reset traffic: %w", err)
	} else if reset {
		c.log.Info("traffic counters reset at the master's request")
	}
	if out.Settings != nil && c.onSettings != nil {
		if err := c.onSettings(); err != nil {
			return fmt.Errorf("reload settings: %w", err)
		}
	}
	// The master is another server this one knows about, so it belongs in the
	// same table the master keeps its nodes in — a node's dashboard should be
	// able to show what it is following.
	if err := c.store.SeeNode(store.Node{
		ID:          out.ServerID,
		Role:        "master",
		Name:        out.Name,
		Version:     out.Version,
		Address:     c.url,
		PeerCount:   out.PeerCount,
		OnlinePeers: out.OnlinePeers,
		TXSpeed:     out.TXSpeed,
		RXSpeed:     out.RXSpeed,
		Recent:      out.Recent,
	}); err != nil {
		return err
	}
	// Recorded only once the data is actually in: a database that has a
	// master's id is one that will not be set aside on the next start.
	if err := c.store.SetMasterID(out.ServerID); err != nil {
		return err
	}
	if c.onSynced != nil {
		c.onSynced()
	}
	return nil
}

// base is the master's panel without the sync path, so a forwarded request can
// be aimed at the same endpoint it was made against here.
func (c *Client) base() string { return strings.TrimSuffix(c.url, "/api/sync") }

// Forward carries a write made on this node's panel to the master, as the
// operator who made it. The master applies that peer's own permissions, so a
// node cannot hand anyone authority they do not already have there.
func (c *Client) Forward(ctx context.Context, f api.Forwarded) (*api.ForwardedResponse, error) {
	req, err := http.NewRequestWithContext(ctx, f.Method, c.base()+f.Path, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	for k, vs := range f.Headers {
		for _, v := range vs {
			if v != "" {
				req.Header.Add(k, v)
			}
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set(api.OnBehalfHeader, f.PeerID)

	req, remote := watchConn(req)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, withRemote(err, remote)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return &api.ForwardedResponse{
		Status:  res.StatusCode,
		Headers: http.Header{"Content-Type": {res.Header.Get("Content-Type")}},
		Body:    body,
	}, nil
}

// SyncNow pulls the master's state at once rather than waiting for the next
// tick, so a write made here is visible here as soon as it is answered.
func (c *Client) SyncNow(ctx context.Context) {
	if err := c.sync(ctx); err != nil {
		c.log.Warn("catching up with the master after a change failed", "error", err)
	}
}
