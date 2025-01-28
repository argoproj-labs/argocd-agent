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
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"
)

// GenerateCaCertificate generates a certificate and private key that will be
// configured to be usable as a Certificate Authority (CA). It returns both,
// the public certificate and the private key in PEM format.
//
// The certificate will be valid for 10 days.
//
// DO NOT USE THE RESULTING CERTIFICATE OR KEY OR ANY CERTIFICATES SIGNED BY
// THIS CA FOR PRODUCTION PURPOSES. NEVER. YOU HAVE BEEN WARNED.
//
// And sorry for shouting.
func GenerateCaCertificate(commonName string) (string, string, error) {
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return "", "", fmt.Errorf("error creating cert: %w", err)
	}

	certPem := new(bytes.Buffer)
	err = pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding certificate: %v", err)
	}

	keyPem := new(bytes.Buffer)
	err = pem.Encode(keyPem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding key: %v", err)
	}

	return certPem.String(), keyPem.String(), nil
}

// GenerateClientCertificate generates a
func GenerateClientCertificate(name string, signerCert *x509.Certificate, signerKey crypto.PrivateKey) (string, string, error) {
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 6, 0),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	return GenerateCertificate(cert, signerCert, signerKey)
}

func GenerateServerCertificate(name string, signerCert *x509.Certificate, signerKey crypto.PrivateKey, san []string) (string, string, error) {
	var err error
	dnsNames := []string{}
	ipAddresses := []net.IP{}
	for _, sanEntry := range san {
		sanTok := strings.SplitN(sanEntry, ":", 2)
		if len(sanTok) != 2 {
			return "", "", fmt.Errorf("invalid SAN entry: %s", sanEntry)
		}
		switch strings.ToLower(sanTok[0]) {
		case "ip":
			sAddr := strings.TrimSpace(sanTok[1])
			addr := net.ParseIP(sAddr)
			if addr == nil {
				return "", "", fmt.Errorf("invalid IP address: %s", sAddr)
			}
			ipAddresses = append(ipAddresses, addr)
		case "dns":
			dnsNames = append(dnsNames, strings.TrimSpace(sanTok[1]))
		default:
			return "", "", fmt.Errorf("unknown san specifier: %s", sanTok[1])
		}
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"DO NOT USE IN PRODUCTION"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(0, 6, 0),
		IsCA:                  false,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	extSubjectAltName := pkix.Extension{}
	extSubjectAltName.Id = asn1.ObjectIdentifier{2, 5, 29, 17}
	extSubjectAltName.Critical = false
	extSubjectAltName.Value, err = asn1.Marshal(san)
	if err != nil {
		return "", "", err
	}
	cert.Extensions = []pkix.Extension{extSubjectAltName}

	return GenerateCertificate(cert, signerCert, signerKey)
}

func GenerateCertificate(cert *x509.Certificate, signerCert *x509.Certificate, signerKey crypto.PrivateKey) (string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}
	certBytes, err := x509.CreateCertificate(rand.Reader, cert, signerCert, &key.PublicKey, signerKey)
	if err != nil {
		return "", "", fmt.Errorf("error creating cert: %w", err)
	}

	certPem := new(bytes.Buffer)
	err = pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding certificate: %v", err)
	}

	keyPem := new(bytes.Buffer)
	err = pem.Encode(keyPem, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err != nil {
		return "", "", fmt.Errorf("error encoding key: %v", err)
	}

	return certPem.String(), keyPem.String(), nil
}
