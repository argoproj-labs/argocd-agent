// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tlsutil

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

const (
	// DefaultLeafCertValidityDays is the default validity period for leaf certificates (~6 months).
	DefaultLeafCertValidityDays = 180
	// DefaultCACertValidityDays is the default validity period for CA certificates (~10 years).
	DefaultCACertValidityDays = 3650
)

// GenerateCaCertificate generates a certificate and private key that will be
// configured to be usable as a Certificate Authority (CA). It returns both,
// the public certificate and the private key in PEM format.
//
// The certificate will be valid for validityDays days.
//
// DO NOT USE THE RESULTING CERTIFICATE OR KEY OR ANY CERTIFICATES SIGNED BY
// THIS CA FOR PRODUCTION PURPOSES. NEVER. YOU HAVE BEEN WARNED.
//
// And sorry for shouting.
func GenerateCaCertificate(commonName string, validityDays int, opts KeyGenOptions) (string, string, error) {
	if err := validateValidityDays(validityDays); err != nil {
		return "", "", err
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, validityDays),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	key, err := GeneratePrivateKey(opts)
	if err != nil {
		return "", "", err
	}
	pub, err := publicKey(key)
	if err != nil {
		return "", "", err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		return "", "", fmt.Errorf("error creating cert: %w", err)
	}

	certPem := new(bytes.Buffer)
	err = pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding certificate: %w", err)
	}

	keyPem, err := PrivateKeyToPEM(key)
	if err != nil {
		return "", "", fmt.Errorf("error encoding key: %w", err)
	}

	return certPem.String(), keyPem, nil
}

// GenerateClientCertificate generates a TLS certificate, signed with the
// given signer cert and signer key, that is suitable for client side
// authentication.
//
// It will return the certificate and its private key as PEM encoded strings.
func GenerateClientCertificate(name string, signerCert *x509.Certificate, signerKey crypto.PrivateKey, validityDays int, opts KeyGenOptions) (string, string, error) {
	if err := validateValidityDays(validityDays); err != nil {
		return "", "", err
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, validityDays),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	return GenerateCertificate(cert, signerCert, signerKey, opts)
}

// GenerateServerCertificate generates a TLS certificate, signed with the
// given signer cert and signer key, that is suitable for server side
// authentication.
//
// It will return the certificate and its private key as PEM encoded strings.
func GenerateServerCertificate(name string, signerCert *x509.Certificate, signerKey crypto.PrivateKey, ips []string, dns []string, validityDays int, opts KeyGenOptions) (string, string, error) {
	if err := validateValidityDays(validityDays); err != nil {
		return "", "", err
	}
	dnsNames := dns
	ipAddresses := []net.IP{}
	for _, ip := range ips {
		sAddr := strings.TrimSpace(ip)
		addr := net.ParseIP(sAddr)
		if addr == nil {
			return "", "", fmt.Errorf("invalid IP address: %s", sAddr)
		}
		ipAddresses = append(ipAddresses, addr)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 0, validityDays),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	return GenerateCertificate(cert, signerCert, signerKey, opts)
}

// ValidateLeafValidityDays returns an error if a leaf cert issued today for
// validityDays would expire after signerCert.NotAfter.
func ValidateLeafValidityDays(signerCert *x509.Certificate, validityDays int) error {
	if err := validateValidityDays(validityDays); err != nil {
		return err
	}
	requestedNotAfter := time.Now().AddDate(0, 0, validityDays)
	if requestedNotAfter.After(signerCert.NotAfter) {
		maxDays := int(time.Until(signerCert.NotAfter).Hours() / 24)
		return fmt.Errorf(
			"requested validity of %d days exceeds CA expiry (%s); maximum is approximately %d days",
			validityDays, signerCert.NotAfter.Format(time.RFC1123Z), maxDays,
		)
	}
	return nil
}

func validateValidityDays(validityDays int) error {
	if validityDays <= 0 {
		return fmt.Errorf("validity days must be greater than 0, got %d", validityDays)
	}
	return nil
}

// GenerateCertificate generates a certificate from template cert and signs it
// using signerCert and signerKey.
//
// It will return the certificate and its private key as PEM encoded strings.
func GenerateCertificate(cert *x509.Certificate, signerCert *x509.Certificate, signerKey crypto.PrivateKey, opts KeyGenOptions) (string, string, error) {
	key, err := GeneratePrivateKey(opts)
	if err != nil {
		return "", "", err
	}
	pub, err := publicKey(key)
	if err != nil {
		return "", "", err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, signerCert, pub, signerKey)
	if err != nil {
		return "", "", fmt.Errorf("error creating cert: %w", err)
	}

	certPem := new(bytes.Buffer)
	err = pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding certificate: %w", err)
	}

	keyPem, err := PrivateKeyToPEM(key)
	if err != nil {
		return "", "", fmt.Errorf("error encoding key: %w", err)
	}

	return certPem.String(), keyPem, nil
}
