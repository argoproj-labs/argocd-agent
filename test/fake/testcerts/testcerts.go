package testcerts

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

var DefaultCertTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-1 * time.Hour),
	NotAfter:              time.Now().Add(1 * time.Hour),
}

var ExpiredCertTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-2 * time.Hour),
	NotAfter:              time.Now().Add(-1 * time.Hour),
}

var NotYetValidCertTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(1 * time.Hour),
	NotAfter:              time.Now().Add(2 * time.Hour),
}

// CreateSelfSignedCert is a test helper that creates a self-signed certificate
// from a template and returns the PEM encoded certificate and key.
func CreateSelfSignedCert(t *testing.T, keyType string, template x509.Certificate) (certBytes []byte, keyBytes []byte) {
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
func WriteSelfSignedCert(t *testing.T, keyType string, basePath string, templ x509.Certificate) {
	t.Helper()
	certBytes, keyBytes := CreateSelfSignedCert(t, keyType, templ)
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

func GeneratePrivateKey(t *testing.T, keyType string) crypto.PrivateKey {
	t.Helper()
	var key crypto.PrivateKey
	var err error

	switch keyType {
	case "rsa":
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	case "ecdsa":
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdh":
		key, err = ecdh.P256().GenerateKey(rand.Reader)
	case "ed25519":
		key, _, err = ed25519.GenerateKey(rand.Reader)
	default:
		t.Fatalf("unknown key type: %s", keyType)
	}
	if err != nil {
		t.Fatalf("error generating %s key: %v", keyType, err)
	}

	return key

}

func WritePrivateKey(t *testing.T, keyType string, path string) crypto.PrivateKey {
	t.Helper()
	key := GeneratePrivateKey(t, keyType)
	var keyPem bytes.Buffer
	kBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("could not marshal key: %v", err)
	}
	err = pem.Encode(&keyPem, &pem.Block{Type: "PRIVATE KEY", Bytes: kBytes})
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
