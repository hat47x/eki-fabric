// Package enroll is the Agent's side of Worker enrollment and certificate
// renewal (ADR-0027 sections 2.2 and 2.4).
//
// Enrollment generates the private key on this device, pins the Control Plane
// by the fingerprint that came with the enrollment secret, and only then
// sends the secret. The key never leaves the device: the Control Plane
// receives a certificate signing request, which carries the public key.
//
// The Agent keeps one identity file (key and certificate, replaced as a unit)
// and the certificate it pinned the Control Plane with.
package enroll

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
)

const (
	// IdentityFile holds the private key and the client certificate.
	IdentityFile = "worker-identity.pem"
	// ControlAnchorFile holds the certificate that the Control Plane was
	// pinned with. Later connections trust only it.
	ControlAnchorFile = "control-anchor.pem"
	// WorkerCAFile holds the Worker CA certificate the Control Plane returned.
	// It is informational: the Agent never verifies clients.
	WorkerCAFile = "worker-ca.pem"
)

var (
	ErrNoIdentity      = errors.New("no worker identity in the state directory")
	ErrIdentityExists  = errors.New("the state directory already holds a worker identity")
	ErrInvalidIdentity = errors.New("invalid worker identity")
	// ErrExpired is returned for a certificate that is past its validity. It
	// cannot renew; the Worker enrolls again with a new credential.
	ErrExpired = errors.New("the worker certificate has expired; enroll again with a new enrollment credential")
)

// Identity is the Worker's key and client certificate.
type Identity struct {
	Key  *ecdsa.PrivateKey
	Leaf *x509.Certificate

	WorkerIdentity ids.WorkerIdentity
	TrustDomain    string
}

// NewIdentity checks that cert belongs to key and carries a Worker name, and
// returns the Identity.
func NewIdentity(key *ecdsa.PrivateKey, cert *x509.Certificate) (Identity, error) {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return Identity{}, fmt.Errorf("%w: the certificate is not for this key", ErrInvalidIdentity)
	}
	hasClientAuth := false
	for _, u := range cert.ExtKeyUsage {
		hasClientAuth = hasClientAuth || u == x509.ExtKeyUsageClientAuth
	}
	if !hasClientAuth || len(cert.URIs) != 1 {
		return Identity{}, fmt.Errorf("%w: not a Worker client certificate", ErrInvalidIdentity)
	}
	td, id, err := workercert.ParseURI(cert.URIs[0])
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %v", ErrInvalidIdentity, err)
	}
	return Identity{Key: key, Leaf: cert, WorkerIdentity: id, TrustDomain: td}, nil
}

// TLSCertificate is the certificate in the form crypto/tls uses.
func (i Identity) TLSCertificate() *tls.Certificate {
	return &tls.Certificate{Certificate: [][]byte{i.Leaf.Raw}, PrivateKey: i.Key, Leaf: i.Leaf}
}

// RenewAt is when this identity renews: one third of its lifetime before it
// expires.
func (i Identity) RenewAt() time.Time { return workercert.RenewAt(i.Leaf.NotBefore, i.Leaf.NotAfter) }

// Expired reports whether the certificate is past its validity at now.
func (i Identity) Expired(now time.Time) bool { return !now.Before(i.Leaf.NotAfter) }

// Save writes the identity file atomically, with mode 0600. A reader sees the
// old key with the old certificate or the new key with the new certificate.
func Save(dir string, id Identity) error {
	keyDER, err := x509.MarshalPKCS8PrivateKey(id.Key)
	if err != nil {
		return fmt.Errorf("encode the private key: %w", err)
	}
	var buf bytes.Buffer
	_ = pem.Encode(&buf, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	_ = pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: id.Leaf.Raw})
	return writeAtomic(filepath.Join(dir, IdentityFile), buf.Bytes())
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	name := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	// CreateTemp creates the file with mode 0600 already; say it again for
	// platforms where that is not guaranteed.
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return cleanup(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}

// Load reads the identity file. On a platform with Unix permissions it
// refuses a file that anyone but the owner can read.
func Load(dir string) (Identity, error) {
	path := filepath.Join(dir, IdentityFile)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, ErrNoIdentity
	}
	if err != nil {
		return Identity{}, err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return Identity{}, fmt.Errorf("%w: %s is accessible to other users (mode %v); run chmod 600", ErrInvalidIdentity, IdentityFile, info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Identity{}, err
	}
	var key *ecdsa.PrivateKey
	var cert *x509.Certificate
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			break
		}
		switch block.Type {
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			ec, ok := k.(*ecdsa.PrivateKey)
			if err != nil || !ok {
				return Identity{}, fmt.Errorf("%w: unreadable private key", ErrInvalidIdentity)
			}
			key = ec
		case "CERTIFICATE":
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return Identity{}, fmt.Errorf("%w: unreadable certificate", ErrInvalidIdentity)
			}
			cert = c
		}
	}
	if key == nil || cert == nil {
		return Identity{}, fmt.Errorf("%w: the file needs a private key and a certificate", ErrInvalidIdentity)
	}
	return NewIdentity(key, cert)
}

// LoadAnchor reads the certificate the Control Plane was pinned with.
func LoadAnchor(dir string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ControlAnchorFile))
	if err != nil {
		return nil, fmt.Errorf("read the pinned Control Plane certificate: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: %s holds no certificate", ErrInvalidIdentity, ControlAnchorFile)
	}
	return x509.ParseCertificate(block.Bytes)
}

// Holder gives the current identity to connections and replaces it on
// renewal. A new connection uses the newest certificate; a connection that
// exists keeps the one it was made with, which the Control Plane accepts
// until it expires.
type Holder struct {
	mu sync.RWMutex
	id Identity
}

func NewHolder(id Identity) *Holder { return &Holder{id: id} }

func (h *Holder) Get() Identity { h.mu.RLock(); defer h.mu.RUnlock(); return h.id }

func (h *Holder) Set(id Identity) { h.mu.Lock(); defer h.mu.Unlock(); h.id = id }

// ClientCertificate is a tls.Config.GetClientCertificate callback.
func (h *Holder) ClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	return h.Get().TLSCertificate(), nil
}
