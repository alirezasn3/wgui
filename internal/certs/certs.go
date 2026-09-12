// Package certs supplies the TLS material the panel serves with. An operator can
// point the config at their own certificate; otherwise one is generated.
//
// Nothing is ever written to disk. A generated certificate is kept in the
// database next to the peer keys that are already there, so it survives a
// restart and its fingerprint stays put; in ephemeral mode it lives only in
// memory and is replaced on every start.
package certs

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"
)

// selfSignedLifetime is deliberately long: nobody wants the panel to stop
// answering because a certificate nobody validates anyway has expired.
const selfSignedLifetime = 10 * 365 * 24 * time.Hour

// Keys the generated material is stored under.
const (
	secretCert = "tls.cert"
	secretKey  = "tls.key"
)

// Store is the part of the database this package needs.
type Store interface {
	Secret(key string) ([]byte, bool, error)
	PutSecret(key string, value []byte) error
	DeleteSecret(key string) error
}

// Source says where the material being served came from, for logging.
type Source int

const (
	// FromConfig means the operator supplied their own certificate.
	FromConfig Source = iota
	// FromStore means a previously generated certificate was reused.
	FromStore
	// Generated means a new self-signed certificate was just made.
	Generated
	// Ephemeral means a new one was made and deliberately not kept.
	Ephemeral
)

// Options describes what to serve with.
type Options struct {
	// CertPath and KeyPath are the operator's own certificate. When both are
	// set and readable they win over anything generated.
	CertPath string
	KeyPath  string
	// Store persists a generated certificate. Nil, or Ephemeral, keeps it in
	// memory only.
	Store Store
	// Ephemeral regenerates on every start instead of reusing a stored
	// certificate. The fingerprint then changes at every restart.
	Ephemeral bool
	// Hosts are the addresses the panel is reached on.
	Hosts []string
}

// Ensure returns the PEM certificate and key to serve with.
func Ensure(opt Options) (certPEM, keyPEM []byte, src Source, err error) {
	if opt.CertPath != "" && opt.KeyPath != "" {
		certPEM, keyPEM, err = readPair(opt.CertPath, opt.KeyPath)
		if err == nil {
			return certPEM, keyPEM, FromConfig, nil
		}
		// Fall through to a generated certificate: refusing to start because a
		// configured path is unreadable would take the panel down over
		// something the panel can supply itself. The caller reports the source
		// so this does not pass unnoticed.
	}

	if opt.Ephemeral && opt.Store != nil {
		// Switching to ephemeral has to take the previously kept pair with it,
		// or the key the operator asked not to keep is still sitting there.
		if err := forget(opt.Store); err != nil {
			return nil, nil, 0, err
		}
	}

	if !opt.Ephemeral && opt.Store != nil {
		certPEM, keyPEM, err := loadStored(opt.Store)
		if err != nil {
			return nil, nil, 0, err
		}
		// A kept certificate is only any use for the names it was issued for.
		// Once the panel answers on a name the certificate has never heard of —
		// a public address that changed, an endpoint added later — every client
		// that checks, a node syncing among them, is turned away. Reissuing is
		// cheap and costs only the browser warning that a self-signed
		// certificate produces anyway.
		if certPEM != nil && Covers(certPEM, opt.Hosts) {
			return certPEM, keyPEM, FromStore, nil
		}
	}

	certPEM, keyPEM, err = generate(opt.Hosts)
	if err != nil {
		return nil, nil, 0, err
	}

	if opt.Ephemeral || opt.Store == nil {
		return certPEM, keyPEM, Ephemeral, nil
	}
	if err := opt.Store.PutSecret(secretCert, certPEM); err != nil {
		return nil, nil, 0, fmt.Errorf("store certificate: %w", err)
	}
	if err := opt.Store.PutSecret(secretKey, keyPEM); err != nil {
		return nil, nil, 0, fmt.Errorf("store key: %w", err)
	}
	return certPEM, keyPEM, Generated, nil
}

// forget removes any stored pair.
func forget(store Store) error {
	for _, key := range []string{secretCert, secretKey} {
		if err := store.DeleteSecret(key); err != nil {
			return fmt.Errorf("discard stored %s: %w", key, err)
		}
	}
	return nil
}

// loadStored returns a previously generated pair, or nil when there is none.
// A stored pair that no longer parses is treated as absent so the next call
// replaces it rather than leaving the panel unable to start.
func loadStored(store Store) (certPEM, keyPEM []byte, err error) {
	certPEM, okCert, err := store.Secret(secretCert)
	if err != nil {
		return nil, nil, fmt.Errorf("read stored certificate: %w", err)
	}
	keyPEM, okKey, err := store.Secret(secretKey)
	if err != nil {
		return nil, nil, fmt.Errorf("read stored key: %w", err)
	}
	if !okCert || !okKey {
		return nil, nil, nil
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, nil, nil
	}
	return certPEM, keyPEM, nil
}

// readPair loads an operator-supplied certificate into memory and checks the two
// halves actually go together.
func readPair(certPath, keyPath string) (certPEM, keyPEM []byte, err error) {
	if certPEM, err = os.ReadFile(certPath); err != nil {
		return nil, nil, err
	}
	if keyPEM, err = os.ReadFile(keyPath); err != nil {
		return nil, nil, err
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, nil, fmt.Errorf("%s and %s are not a valid key pair: %w", certPath, keyPath, err)
	}
	return certPEM, keyPEM, nil
}

// generate returns a fresh self-signed certificate covering hosts.
func generate(hosts []string) (certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "wgui", Organization: []string{"wgui"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(selfSignedLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Every address the panel answers on, or it will not match when anyone
	// checks.
	seen := map[string]bool{}
	for _, h := range hosts {
		h = hostOnly(h)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}
	template.IPAddresses = append(template.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	return encode("CERTIFICATE", der), encode("EC PRIVATE KEY", keyDER), nil
}

func encode(blockType string, der []byte) []byte {
	var buf bytes.Buffer
	// pem.Encode only fails on a failing writer, and a bytes.Buffer never does.
	_ = pem.Encode(&buf, &pem.Block{Type: blockType, Bytes: der})
	return buf.Bytes()
}

// Fingerprint is the certificate's SHA-256 fingerprint, so an operator can check
// what their browser is being shown against what the server logged.
func Fingerprint(certPEM []byte) string {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	out := make([]byte, 0, len(sum)*3)
	const hexDigits = "0123456789ABCDEF"
	for i, b := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}

// covers reports whether a certificate is valid for every host given. Unknown
// or unparsable certificates are treated as covering nothing, so the caller
// reissues rather than serving something no one can verify.
// Covers reports whether a certificate is valid for every host given.
func Covers(certPEM []byte, hosts []string) bool {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if h = hostOnly(h); h == "" {
			continue
		}
		if cert.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

// hostOnly strips the port an endpoint carries, since a certificate names hosts
// and settings write them as host:port.
func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	// A bare IPv6 address splits badly and is not host:port; keep it whole.
	return strings.Trim(h, "[]")
}
