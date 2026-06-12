// Copyright 2025 The argocd-agent Authors
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
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"slices"
)

const (
	DefaultKeyAlgorithm = "rsa"
	DefaultRSABits      = 4096
)

var supportedRSAKeySizes = []int{2048, 3072, 4096}

// KeyGenOptions configures the algorithm and size of a generated private key.
type KeyGenOptions struct {
	Algorithm string
	RSABits   int
}

// WithDefaults returns opts with zero values replaced by package defaults.
func (o KeyGenOptions) WithDefaults() KeyGenOptions {
	if o.Algorithm == "" {
		o.Algorithm = DefaultKeyAlgorithm
	}
	if o.RSABits == 0 {
		o.RSABits = DefaultRSABits
	}
	return o
}

// ParseKeyAlgorithm validates and normalizes algorithm and RSA key size flags.
func ParseKeyAlgorithm(algorithm string, rsaBits int) (KeyGenOptions, error) {
	opts := KeyGenOptions{Algorithm: algorithm, RSABits: rsaBits}.WithDefaults()

	switch opts.Algorithm {
	case "rsa":
		if !slices.Contains(supportedRSAKeySizes, opts.RSABits) {
			return KeyGenOptions{}, fmt.Errorf(
				"unsupported RSA key size %d; supported values are %v",
				opts.RSABits, supportedRSAKeySizes,
			)
		}
	case "ecdsa-p256", "ecdsa-p384", "ecdsa-p521", "ed25519":
	default:
		return KeyGenOptions{}, fmt.Errorf(
			"unsupported key algorithm %q; supported values are rsa, ecdsa-p256, ecdsa-p384, ecdsa-p521, ed25519",
			algorithm,
		)
	}

	return opts, nil
}

// GeneratePrivateKey creates a private key according to opts.
func GeneratePrivateKey(opts KeyGenOptions) (crypto.PrivateKey, error) {
	opts = opts.WithDefaults()

	switch opts.Algorithm {
	case "rsa":
		return rsa.GenerateKey(rand.Reader, opts.RSABits)
	case "ecdsa-p256":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "ecdsa-p384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "ecdsa-p521":
		return ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "ed25519":
		_, key, err := ed25519.GenerateKey(rand.Reader)
		return key, err
	default:
		return nil, fmt.Errorf("unsupported key algorithm: %s", opts.Algorithm)
	}
}

// PrivateKeyToPEM encodes a private key as PKCS#8 PEM.
func PrivateKeyToPEM(key crypto.PrivateKey) (string, error) {
	return KeyDataToPEM(key)
}

// PublicKey returns the public key for a private key.
func publicKey(key crypto.PrivateKey) (crypto.PublicKey, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return &k.PublicKey, nil
	case *ecdsa.PrivateKey:
		return &k.PublicKey, nil
	case ed25519.PrivateKey:
		return k.Public().(ed25519.PublicKey), nil
	default:
		return nil, fmt.Errorf("unsupported private key type: %T", key)
	}
}

// PublicKeySummary returns a human-readable key type and effective bit length.
func PublicKeySummary(pub crypto.PublicKey) (keyType string, keyLength int) {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return "RSA", k.N.BitLen()
	case *ecdsa.PublicKey:
		return "ECDSA", k.Curve.Params().BitSize
	case ed25519.PublicKey:
		return "Ed25519", 256
	default:
		return fmt.Sprintf("%T", pub), 0
	}
}
