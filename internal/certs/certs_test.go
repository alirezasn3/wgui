package certs

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// memStore is the database, reduced to what this package uses.
type memStore struct {
	data  map[string][]byte
	reads int
}

func newMemStore() *memStore { return &memStore{data: map[string][]byte{}} }

func (m *memStore) Secret(key string) ([]byte, bool, error) {
	m.reads++
	v, ok := m.data[key]
	return v, ok, nil
}

func (m *memStore) PutSecret(key string, value []byte) error {
	m.data[key] = value
	return nil
}

func (m *memStore) DeleteSecret(key string) error {
	delete(m.data, key)
	return nil
}

func parse(t *testing.T, certPEM, keyPEM []byte) *x509.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("the pair does not load: %v", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return leaf
}

func TestGeneratesAValidCertificateCoveringTheHosts(t *testing.T) {
	store := newMemStore()

	certPEM, keyPEM, src, err := Ensure(Options{
		Store: store,
		Hosts: []string{"10.0.0.1", "vpn.example.com"},
	})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if src != Generated {
		t.Fatalf("source = %v, want Generated", src)
	}

	leaf := parse(t, certPEM, keyPEM)
	if err := leaf.VerifyHostname("vpn.example.com"); err != nil {
		t.Errorf("certificate does not cover the configured hostname: %v", err)
	}
	for _, want := range []string{"10.0.0.1", "127.0.0.1"} {
		if !hasIP(leaf.IPAddresses, want) {
			t.Errorf("certificate does not cover %s", want)
		}
	}
}

// The point of the exercise: a generated certificate must not leave a file
// behind anywhere.
func TestNothingIsWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })

	if _, _, _, err := Ensure(Options{Store: newMemStore(), Hosts: []string{"10.0.0.1"}}); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("files were created: %v", names)
	}
}

// A restart must serve the same certificate, or every admin's browser throws the
// interstitial again after each upgrade.
func TestStoredCertificateIsReusedAcrossRestarts(t *testing.T) {
	store := newMemStore()

	first, firstKey, src, err := Ensure(Options{Store: store, Hosts: []string{"10.0.0.1"}})
	if err != nil || src != Generated {
		t.Fatalf("first = %v, %v", src, err)
	}

	second, secondKey, src, err := Ensure(Options{Store: store, Hosts: []string{"10.0.0.1"}})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if src != FromStore {
		t.Errorf("source = %v, want FromStore", src)
	}
	if string(first) != string(second) || string(firstKey) != string(secondKey) {
		t.Error("the certificate changed between starts")
	}
	if Fingerprint(first) != Fingerprint(second) {
		t.Error("the fingerprint changed between starts")
	}
}

func TestEphemeralKeepsNothingAndChangesEveryStart(t *testing.T) {
	store := newMemStore()
	// Something kept from an earlier run in stored mode; switching to ephemeral
	// has to take it away, or the key the operator asked not to keep is still
	// there.
	store.data[secretCert] = []byte("left over")
	store.data[secretKey] = []byte("left over")

	first, _, src, err := Ensure(Options{Store: store, Ephemeral: true, Hosts: []string{"10.0.0.1"}})
	if err != nil || src != Ephemeral {
		t.Fatalf("first = %v, %v", src, err)
	}
	if len(store.data) != 0 {
		t.Fatalf("ephemeral mode persisted %d secrets, want none", len(store.data))
	}

	second, _, _, err := Ensure(Options{Store: store, Ephemeral: true, Hosts: []string{"10.0.0.1"}})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if Fingerprint(first) == Fingerprint(second) {
		t.Error("ephemeral mode served the same certificate twice")
	}
}

// Without a store there is nowhere to keep it, so it is ephemeral by necessity.
func TestNoStoreMeansEphemeral(t *testing.T) {
	_, _, src, err := Ensure(Options{Hosts: []string{"10.0.0.1"}})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if src != Ephemeral {
		t.Errorf("source = %v, want Ephemeral", src)
	}
}

func TestSuppliedCertificateWins(t *testing.T) {
	dir := t.TempDir()
	store := newMemStore()

	// Produce a real pair to stand in for the operator's own certificate.
	certPEM, keyPEM, _, err := Ensure(Options{Hosts: []string{"own.example.com"}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	certPath := filepath.Join(dir, "own.pem")
	keyPath := filepath.Join(dir, "own.key")
	os.WriteFile(certPath, certPEM, 0o600)
	os.WriteFile(keyPath, keyPEM, 0o600)

	got, gotKey, src, err := Ensure(Options{CertPath: certPath, KeyPath: keyPath, Store: store})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if src != FromConfig {
		t.Fatalf("source = %v, want FromConfig", src)
	}
	if string(got) != string(certPEM) || string(gotKey) != string(keyPEM) {
		t.Error("the supplied certificate was not the one returned")
	}
	if len(store.data) != 0 {
		t.Error("a supplied certificate must not cause anything to be stored")
	}
}

// A half-configured or broken pair must not take the panel down; falling back to
// a working self-signed certificate beats refusing to start.
func TestFallsBackWhenTheSuppliedPairIsUnusable(t *testing.T) {
	dir := t.TempDir()

	good, goodKey, _, _ := Ensure(Options{Hosts: []string{"a"}})
	_, otherKey, _, _ := Ensure(Options{Hosts: []string{"b"}})

	certPath := filepath.Join(dir, "c.pem")
	keyPath := filepath.Join(dir, "c.key")

	for _, tc := range []struct {
		name      string
		cert, key []byte
		writeKey  bool
	}{
		{name: "missing key", cert: good, writeKey: false},
		{name: "mismatched key", cert: good, key: otherKey, writeKey: true},
		{name: "garbage", cert: []byte("not a certificate"), key: goodKey, writeKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(keyPath)
			os.WriteFile(certPath, tc.cert, 0o600)
			if tc.writeKey {
				os.WriteFile(keyPath, tc.key, 0o600)
			}

			certPEM, keyPEM, src, err := Ensure(Options{
				CertPath: certPath, KeyPath: keyPath, Store: newMemStore(), Hosts: []string{"10.0.0.1"},
			})
			if err != nil {
				t.Fatalf("ensure: %v", err)
			}
			if src == FromConfig {
				t.Fatal("an unusable pair was accepted")
			}
			parse(t, certPEM, keyPEM) // the fallback has to actually work
		})
	}
}

// A stored pair that no longer parses must be replaced rather than leaving the
// panel unable to start.
func TestCorruptStoredCertificateIsReplaced(t *testing.T) {
	store := newMemStore()
	store.data[secretCert] = []byte("corrupt")
	store.data[secretKey] = []byte("corrupt")

	certPEM, keyPEM, src, err := Ensure(Options{Store: store, Hosts: []string{"10.0.0.1"}})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if src != Generated {
		t.Fatalf("source = %v, want Generated", src)
	}
	parse(t, certPEM, keyPEM)
}

func TestFingerprintIsStableAndFormatted(t *testing.T) {
	certPEM, _, _, err := Ensure(Options{Hosts: []string{"10.0.0.1"}})
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	fp := Fingerprint(certPEM)
	if len(fp) != 32*3-1 {
		t.Errorf("fingerprint %q is not a colon-separated sha256", fp)
	}
	if Fingerprint(certPEM) != fp {
		t.Error("fingerprint is not stable")
	}
	if Fingerprint([]byte("nonsense")) != "" {
		t.Error("a non-certificate should have no fingerprint")
	}
}

func hasIP(list []net.IP, want string) bool {
	target := net.ParseIP(want)
	for _, ip := range list {
		if ip.Equal(target) {
			return true
		}
	}
	return false
}
