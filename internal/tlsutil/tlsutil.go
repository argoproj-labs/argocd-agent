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
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
)

// TlsCertFromFile loads a TLS certificate and RSA private key from the
// files specified as certPath and keyPath, applying some basic validation
// before returning the certificate.
func TlsCertFromFile(certPath string, keyPath string, strict bool) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return cert, err
	}
	for _, c := range cert.Certificate {
		cc, err := x509.ParseCertificate(c)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("could not parse certificate from %s: %w", certPath, err)
		}
		if strict && !cc.NotAfter.After(time.Now()) {
			return tls.Certificate{}, fmt.Errorf("server certificate has expired on %s", cc.NotAfter.Format(time.RFC1123Z))
		}
		if strict && !cc.NotBefore.Before(time.Now()) {
			return tls.Certificate{}, fmt.Errorf("server certificate not yet valid, valid from %s", cc.NotAfter.Format(time.RFC1123Z))
		}
	}
	return cert, nil
}

// TlsCertFromX509 generates a TLS certificate for the x509 certificate cert
// and the private key key.
// This function supports RSA and EC types of private keys.
func TlsCertFromX509(cert *x509.Certificate, key crypto.PrivateKey) (tls.Certificate, error) {
	cBytes := &bytes.Buffer{}
	kBytes := &bytes.Buffer{}
	if cert == nil || key == nil {
		return tls.Certificate{}, fmt.Errorf("unexpected input")
	}
	err := pem.Encode(cBytes, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error encoding cert: %w", err)
	}

	switch pk := key.(type) {
	case *rsa.PrivateKey, *ecdsa.PrivateKey, *ecdh.PrivateKey, ed25519.PrivateKey:
		kdata, err := x509.MarshalPKCS8PrivateKey(pk)
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("error marshaling private key: %w", err)
		}
		err = pem.Encode(kBytes, &pem.Block{Type: "PRIVATE KEY", Bytes: kdata})
		if err != nil {
			return tls.Certificate{}, fmt.Errorf("error encoding private key to PEM: %w", err)
		}
	default:
		return tls.Certificate{}, fmt.Errorf("unknown private key type: %T", pk)
	}

	tlsCert, err := tls.X509KeyPair(cBytes.Bytes(), kBytes.Bytes())
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("error creating key pair: %w", err)
	}

	return tlsCert, nil
}

// func X509CertFromFile(path string) (*x509.Certificate, error) {
// 	b, err := os.ReadFile(path)
// 	if err != nil {
// 		return nil, err
// 	}

// }
