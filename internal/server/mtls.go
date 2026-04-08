package server

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// rsaKeyBits is the required RSA modulus size for TLS certificates (RFD: RSA 4096 only).
const rsaKeyBits = 4096

// builds a TLS config for the gRPC server with mutual TLS
func LoadServerTLS(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load server cert/key: %w", err)
	}
	if err := requireRSA4096Certificate(&cert, "server"); err != nil {
		return nil, err
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
	}

	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             caPool,
		MinVersion:            tls.VersionTLS13,
		VerifyPeerCertificate: verifyClientPeerCertificate(),
	}, nil
}

// requireRSA4096Certificate checks the leaf uses RSA with a 4096-bit modulus.
func requireRSA4096Certificate(cert *tls.Certificate, role string) error {
	if len(cert.Certificate) == 0 {
		return fmt.Errorf("%s certificate: chain is empty", role)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("%s certificate: %w", role, err)
	}
	if err := requireRSA4096Leaf(leaf); err != nil {
		return fmt.Errorf("%s certificate: %w", role, err)
	}
	return nil
}

func requireRSA4096Leaf(leaf *x509.Certificate) error {
	pub, ok := leaf.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("must use RSA-%d, got %T", rsaKeyBits, leaf.PublicKey)
	}
	if got := pub.N.BitLen(); got != rsaKeyBits {
		return fmt.Errorf("must use RSA-%d, got RSA-%d", rsaKeyBits, got)
	}
	return nil
}

// ensure ExtKeyUsageClientAuth EKU and RSA 4096 (RFD).
func verifyClientPeerCertificate() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			return fmt.Errorf("no verified client certificate chain")
		}

		leaf := verifiedChains[0][0]
		if err := requireRSA4096Leaf(leaf); err != nil {
			return fmt.Errorf("client certificate: %w", err)
		}
		for _, eku := range leaf.ExtKeyUsage {
			if eku == x509.ExtKeyUsageClientAuth {
				return nil
			}
		}

		return fmt.Errorf("client certificate is not valid for client authentication (missing ClientAuth EKU)")
	}
}

// builds a TLS config for the gRPC client with mTLS
func LoadClientTLS(certFile, keyFile, caFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load client cert/key: %w", err)
	}
	if err := requireRSA4096Certificate(&cert, "client"); err != nil {
		return nil, err
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA cert: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate from %s", caFile)
	}

	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		RootCAs:               caPool,
		MinVersion:            tls.VersionTLS13,
		VerifyPeerCertificate: verifyServerEKU(),
	}, nil
}

// ensure ExtKeyUsageServerAuth EKU and RSA 4096 (RFD).
func verifyServerEKU() func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			return fmt.Errorf("no verified server certificate chain")
		}

		leaf := verifiedChains[0][0]
		if err := requireRSA4096Leaf(leaf); err != nil {
			return fmt.Errorf("server certificate: %w", err)
		}
		for _, eku := range leaf.ExtKeyUsage {
			if eku == x509.ExtKeyUsageServerAuth {
				return nil
			}
		}

		return fmt.Errorf("server certificate is not valid for server authentication (missing ServerAuth EKU)")
	}
}
