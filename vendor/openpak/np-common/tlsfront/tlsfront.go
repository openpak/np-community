// Package tlsfront terminates the PS3 firmware's TLS. The firmware offers only
// RSA key exchange (TLS 1.0-1.2) and checks the leaf against the OpenPak PS3
// CA its trust store carries, so a standard front door cannot serve it:
// Traefik passes these SNIs through and this listener mints an exact leaf per
// requested name from that CA.
package tlsfront

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Front holds the CA and the per-name leaves minted from it.
type Front struct {
	ca      *x509.Certificate
	caKey   any
	leafKey *rsa.PrivateKey
	// Fallback names the leaf for a client that sends no SNI (XMPP).
	Fallback string

	mu    sync.Mutex
	cache map[string]*tls.Certificate // ponytail: unbounded; Traefik only passes the PS3 families through
}

// Load reads the CA certificate and key (PKCS#1 or PKCS#8 PEM).
func Load(certPath, keyPath string) (*Front, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("tlsfront: CA certificate is not PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("tlsfront: CA key is not PEM")
	}
	var caKey any
	if caKey, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		if caKey, err = x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
			return nil, fmt.Errorf("tlsfront: CA key: %w", err)
		}
	}
	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Front{ca: ca, caKey: caKey, leafKey: leafKey, cache: map[string]*tls.Certificate{}}, nil
}

// Config is the server configuration the firmware can negotiate.
func (f *Front) Config() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_3DES_EDE_CBC_SHA,
			tls.TLS_RSA_WITH_RC4_128_SHA,
		},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			// Exactly as sent: the lab leaves kept the firmware's case
			// (IV0001-NPXS01001-00.auth...), and its name check may not fold it.
			name := strings.TrimSuffix(hello.ServerName, ".")
			if name == "" {
				name = f.Fallback
			}
			if name == "" {
				return nil, errors.New("tlsfront: no server name")
			}
			return f.leaf(name)
		},
	}
}

// Serve wraps srv for the firmware: HTTP/1.1 only. Go's automatic HTTP/2
// refuses a cipher list without ECDHE-GCM, and the PS3 never asks for h2.
func (f *Front) Serve(srv *http.Server) *http.Server {
	srv.TLSConfig = f.Config()
	srv.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){}
	return srv
}

func (f *Front) leaf(host string) (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.cache[host]; ok {
		return c, nil
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 63))
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"OpenPak"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, f.ca, &f.leafKey.PublicKey, f.caKey)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der, f.ca.Raw}, PrivateKey: f.leafKey}
	f.cache[host] = c
	return c, nil
}
