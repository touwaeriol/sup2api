package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TLS mode for vendor hosts the platform plugins address directly (Bedrock,
// Vertex AI, the Codex backend): the E2E
// network gives the mock those host names as aliases, and the nodes trust
// the mock's CA, so the real plugins reach the mock without a test endpoint.
//
//	MOCK_TLS_ADDR   address of the TLS listener (e.g. ":443"); unset = no TLS
//	MOCK_TLS_DIR    directory of ca.pem (what clients trust) and
//	                private/ca-key.pem; the CA is created once and reused
//
// The server certificate of a host name is issued, by that CA, for the name
// the client asks for (SNI) the first time it asks, so the host names are
// listed once, in the network's aliases. Only the CA persists.

// serveTLS serves h on MOCK_TLS_ADDR when it is set, and on the CONNECT
// tunnels (connect.go); it returns nil at once otherwise.
func serveTLS(h http.Handler, tunnels *tunnelListener) error {
	addr := os.Getenv("MOCK_TLS_ADDR")
	if addr == "" {
		return nil
	}
	dir := os.Getenv("MOCK_TLS_DIR")
	if dir == "" {
		dir = "/tls"
	}
	cfg, err := tlsConfig(dir)
	if err != nil {
		return err
	}
	errc := make(chan error, 2)
	go func() { errc <- tunnelServer(h, cfg).ServeTLS(tunnels, "", "") }()
	go func() {
		srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, TLSConfig: cfg}
		errc <- srv.ListenAndServeTLS("", "")
	}()
	return <-errc
}

// tlsConfig issues server certificates by SNI from the CA in dir.
func tlsConfig(dir string) (*tls.Config, error) {
	ca, caKey, err := loadOrCreateCA(dir)
	if err != nil {
		return nil, err
	}
	certs := &certIssuer{ca: ca, caKey: caKey, byName: map[string]*tls.Certificate{}}
	return &tls.Config{GetCertificate: certs.get, MinVersion: tls.VersionTLS12}, nil
}

// certIssuer issues and keeps one server certificate per host name.
type certIssuer struct {
	ca     *x509.Certificate
	caKey  *ecdsa.PrivateKey
	mu     sync.Mutex
	byName map[string]*tls.Certificate
}

func (c *certIssuer) get(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.ToLower(hello.ServerName)
	if name == "" {
		return nil, errors.New("mock TLS: the client sent no server name")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cert, ok := c.byName[name]; ok {
		return cert, nil
	}
	cert, err := issue(c.ca, c.caKey, name)
	if err != nil {
		return nil, err
	}
	c.byName[name] = &cert
	return &cert, nil
}

func loadOrCreateCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "private", "ca-key.pem")
	if certPEM, err := os.ReadFile(certPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, nil, err
		}
		return parseCA(certPEM, keyPEM)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "sub2api e2e mock CA"},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, nil, err
	}
	// Written last: its presence means the CA is complete (the health check).
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("mock CA: bad PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	return cert, key, err
}

func issue(ca *x509.Certificate, caKey *ecdsa.PrivateKey, host string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue mock certificate: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.Raw}, PrivateKey: key}, nil
}
