package certs

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

// CreateSelfSignedCert is a test helper that creates a self-signed certificate
// from a template and returns the PEM encoded certificate and key.
func CreateSelfSignedCert(t *testing.T, template x509.Certificate) (certBytes []byte, keyBytes []byte) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("error generating key: %v", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("error generating cert: %v", err)
	}
	var keyPem, certPem bytes.Buffer
	err = pem.Encode(&keyPem, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	if err != nil {
		t.Fatalf("error encoding key: %v", err)
	}
	err = pem.Encode(&certPem, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err != nil {
		t.Fatalf("error encoding certificate: %v", err)
	}
	return certPem.Bytes(), keyPem.Bytes()
}

// WriteSelfSignedCert is a test helper to write a self-signed certificate, as
// defined by templ, and its corresponding key in PEM formats to files suffixed
// by ".crt" and ".key" respectively to the path specified by basePath.
func WriteSelfSignedCert(t *testing.T, basePath string, templ x509.Certificate) {
	t.Helper()
	certBytes, keyBytes := CreateSelfSignedCert(t, templ)
	certFile := basePath + ".crt"
	keyFile := basePath + ".key"
	err := os.WriteFile(certFile, certBytes, 0644)
	if err != nil {
		t.Fatalf("error writing certfile: %v", err)
	}
	err = os.WriteFile(keyFile, keyBytes, 0600)
	if err != nil {
		t.Fatalf("error writing keyfile: %v", err)
	}
}

func WriteRSAPrivateKey(t *testing.T, path string) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("error generate key: %v", err)
	}

	var keyPem bytes.Buffer
	err = pem.Encode(&keyPem, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err != nil {
		t.Fatalf("error encoding key: %v", err)
	}
	err = os.WriteFile(path, keyPem.Bytes(), 0600)
	if err != nil {
		t.Fatalf("error writing key: %v", err)
	}
	return key
}

func WriteRSAPublicKey(t *testing.T, path string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("error generate key: %v", err)
	}

	var keyPem bytes.Buffer
	err = pem.Encode(&keyPem, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)})
	if err != nil {
		t.Fatalf("error encoding key: %v", err)
	}
	err = os.WriteFile(path, keyPem.Bytes(), 0600)
	if err != nil {
		t.Fatalf("error writing key: %v", err)
	}
}
