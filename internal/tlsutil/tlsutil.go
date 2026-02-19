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
	"os"
	"time"
)

// TLSCertFromFile loads a TLS certificate and RSA private key from the
// files specified as certPath and keyPath, applying some basic validation
// before returning the certificate.
func TLSCertFromFile(certPath string, keyPath string, strict bool) (tls.Certificate, error) {
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

// TLSCertFromX509 generates a TLS certificate for the x509 certificate cert
// and the private key key.
// This function supports RSA and EC types of private keys.
func TLSCertFromX509(cert *x509.Certificate, key crypto.PrivateKey) (tls.Certificate, error) {
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

func X509CertPoolFromFile(path string) (*x509.CertPool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(b)
	if !ok {
		return nil, fmt.Errorf("%s: invalid PEM data", path)
	}

	return pool, nil
}

func CertDataToPEM(b []byte) (string, error) {
	certPem := new(bytes.Buffer)
	err := pem.Encode(certPem, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: b,
	})
	return certPem.String(), err
}

func KeyDataToPEM(k crypto.PrivateKey) (string, error) {
	keyPem := new(bytes.Buffer)
	var keyBytes []byte
	var err error

	if keyBytes, err = x509.MarshalPKCS8PrivateKey(k); err != nil {
		return "", err
	}

	err = pem.Encode(keyPem, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyBytes,
	})

	return keyPem.String(), err

}

// supportedTLSVersions maps TLS version names to their constants.
var supportedTLSVersions = map[string]uint16{
	"tls1.1": tls.VersionTLS11,
	"tls1.2": tls.VersionTLS12,
	"tls1.3": tls.VersionTLS13,
}

// TLSVersionName returns the human-readable name for a TLS version constant.
// Returns a hex representation if the version is unknown.
func TLSVersionName(version uint16) string {
	for name, v := range supportedTLSVersions {
		if v == version {
			return name
		}
	}
	return fmt.Sprintf("unknown (0x%04x)", version)
}

// TLSVersionFromName parses a TLS version string (e.g., "tls1.2") and returns
// the corresponding TLS version constant. Returns an error if the version
// string is not recognized.
func TLSVersionFromName(name string) (uint16, error) {
	v, ok := supportedTLSVersions[name]
	if !ok {
		return 0, fmt.Errorf("TLS version %s is not supported", name)
	}
	return v, nil
}

// ParseCipherSuites converts a list of cipher suite names to their corresponding
// uint16 IDs. Returns an error if any cipher suite name is not recognized.
func ParseCipherSuites(names []string) ([]uint16, error) {
	if len(names) == 0 {
		return nil, nil
	}
	availableCiphers := tls.CipherSuites()
	cipherIDs := make([]uint16, 0, len(names))
	for _, name := range names {
		found := false
		for _, cs := range availableCiphers {
			if cs.Name == name {
				cipherIDs = append(cipherIDs, cs.ID)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("no such cipher suite: %s", name)
		}
	}
	return cipherIDs, nil
}

// ValidateTLSConfig validates the TLS configuration parameters.
// It checks that:
// - The minimum TLS version is not greater than the maximum TLS version
// - All configured cipher suites are compatible with the minimum TLS version
func ValidateTLSConfig(minVersion, maxVersion uint16, cipherSuites []uint16) error {
	// Check that min version <= max version (if both are set)
	if minVersion != 0 && maxVersion != 0 {
		if minVersion > maxVersion {
			return fmt.Errorf("minimum TLS version (%s) cannot be higher than maximum TLS version (%s)",
				TLSVersionName(minVersion), TLSVersionName(maxVersion))
		}
	}

	// Check that all cipher suites are compatible with the minimum TLS version
	if len(cipherSuites) > 0 && minVersion != 0 {
		availableCiphers := tls.CipherSuites()
		for _, cipherID := range cipherSuites {
			for _, cs := range availableCiphers {
				if cs.ID == cipherID {
					// Check if this cipher supports the minimum TLS version
					supported := false
					for _, v := range cs.SupportedVersions {
						if v == minVersion {
							supported = true
							break
						}
					}
					if !supported {
						return fmt.Errorf("cipher suite %s is not supported by minimum TLS version %s",
							cs.Name, TLSVersionName(minVersion))
					}
					break
				}
			}
		}
	}

	return nil
}

// SetTLSConfigFromFlags sets the TLS configuration parameters from the command line flags.
// It returns an error if any of the parameters are invalid.
// tlsConfig must be a pointer to an initialized tls.Config struct and will be modified in place.
func SetTLSConfigFromFlags(tlsConfig *tls.Config, minVersion, maxVersion string, cipherSuites []string) error {
	var err error
	if tlsConfig == nil {
		return fmt.Errorf("tlsConfig is nil")
	}
	if minVersion != "" {
		tlsConfig.MinVersion, err = TLSVersionFromName(minVersion)
		if err != nil {
			return err
		}
	}
	if maxVersion != "" {
		tlsConfig.MaxVersion, err = TLSVersionFromName(maxVersion)
		if err != nil {
			return err
		}
	}
	if len(cipherSuites) > 0 && (len(cipherSuites) != 1 || cipherSuites[0] != "") {
		tlsConfig.CipherSuites, err = ParseCipherSuites(cipherSuites)
		if err != nil {
			return err
		}
	}
	return ValidateTLSConfig(tlsConfig.MinVersion, tlsConfig.MaxVersion, tlsConfig.CipherSuites)
}
