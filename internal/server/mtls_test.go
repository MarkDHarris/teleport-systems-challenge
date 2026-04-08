package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const tlsTestCertsDir = "../../scripts/certs/"

func requireTestCerts(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(tlsTestCertsDir + "server.crt"); err != nil {
		t.Skip("test TLS fixtures missing (run `make certs` or keep scripts/certs/)")
	}
}

// test loading server TLS config with missing cert file
func TestLoadServerTLSMissingServerCert(t *testing.T) {
	requireTestCerts(t)

	tmp := t.TempDir()
	_, err := LoadServerTLS(
		filepath.Join(tmp, "missing.crt"),
		tlsTestCertsDir+"server.key",
		tlsTestCertsDir+"ca.crt",
	)
	if err == nil {
		t.Fatal("expected error when server certificate file is missing")
	}
	if !strings.Contains(err.Error(), "load server cert/key") {
		t.Errorf("error = %v, want message containing %q", err, "load server cert/key")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrapped os.ErrNotExist, got: %v", err)
	}
}

// test loading server TLS config with missing CA file
func TestLoadServerTLSMissingCAFile(t *testing.T) {
	requireTestCerts(t)

	tmp := t.TempDir()
	_, err := LoadServerTLS(
		tlsTestCertsDir+"server.crt",
		tlsTestCertsDir+"server.key",
		filepath.Join(tmp, "no-ca.crt"),
	)
	if err == nil {
		t.Fatal("expected error when CA file is missing")
	}
	if !strings.Contains(err.Error(), "read CA cert") {
		t.Errorf("error = %v, want message containing %q", err, "read CA cert")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrapped os.ErrNotExist, got: %v", err)
	}
}

// test loading server TLS config with malformed CA PEM
// test fixtures use RSA 4096 as required by LoadServerTLS
func TestLoadServerTLSAcceptsRSA4096Fixture(t *testing.T) {
	requireTestCerts(t)

	_, err := LoadServerTLS(
		tlsTestCertsDir+"server.crt",
		tlsTestCertsDir+"server.key",
		tlsTestCertsDir+"ca.crt",
	)
	if err != nil {
		t.Fatalf("LoadServerTLS: %v", err)
	}
}

// test server TLS rejects RSA keys smaller than 4096 bits
func TestLoadServerTLSRejectsRSA2048(t *testing.T) {
	requireTestCerts(t)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	tmp := t.TempDir()
	certPath := filepath.Join(tmp, "server.crt")
	keyPath := filepath.Join(tmp, "server.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = LoadServerTLS(certPath, keyPath, tlsTestCertsDir+"ca.crt")
	if err == nil {
		t.Fatal("expected error for RSA-2048 server certificate")
	}
	if !strings.Contains(err.Error(), "RSA-4096") && !strings.Contains(err.Error(), "must use RSA") {
		t.Errorf("error = %v, want RSA key size rejection", err)
	}
}

func TestLoadServerTLSMalformedCAPEM(t *testing.T) {
	requireTestCerts(t)

	badCA := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(badCA, []byte("not valid pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadServerTLS(
		tlsTestCertsDir+"server.crt",
		tlsTestCertsDir+"server.key",
		badCA,
	)
	if err == nil {
		t.Fatal("expected error when CA PEM does not parse")
	}
	if !strings.Contains(err.Error(), "failed to parse CA certificate") {
		t.Errorf("error = %v, want message containing %q", err, "failed to parse CA certificate")
	}
}
