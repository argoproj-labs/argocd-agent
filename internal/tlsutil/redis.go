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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"

	"github.com/sirupsen/logrus"
)

// CreateRedisTLSConfig creates a TLS configuration for connecting to Redis with proper CA verification.
// It handles three modes of operation:
// 1. Insecure mode (skip verification)
// 2. CA from certificate pool (loaded from Kubernetes secret)
// 3. CA from file path
//
// Parameters:
//   - enabled: Whether Redis TLS is enabled
//   - redisAddress: Redis server address (used to extract ServerName for TLS)
//   - insecure: Whether to skip TLS verification (insecure mode)
//   - caPool: Pre-loaded CA certificate pool (from Kubernetes secret)
//   - caPath: Path to CA certificate file
//   - componentName: Name of the component using Redis (for logging, e.g., "cluster manager", "cluster cache")
//
// Returns:
//   - *tls.Config: Configured TLS config, or nil if TLS is not enabled
//   - error: Error if TLS is enabled but configuration is invalid
func CreateRedisTLSConfig(
	enabled bool,
	redisAddress string,
	insecure bool,
	caPool *x509.CertPool,
	caPath string,
	componentName string,
) (*tls.Config, error) {
	if !enabled {
		return nil, nil
	}

	// Extract server name from Redis address for proper certificate validation
	serverName, _, err := net.SplitHostPort(redisAddress)
	if err != nil {
		// If no port, use the address as-is (e.g., "argocd-redis")
		serverName = redisAddress
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
	}

	if insecure {
		tlsConfig.InsecureSkipVerify = true
		logrus.Warnf("INSECURE: %s not verifying Redis TLS certificate", componentName)
	} else if caPool != nil {
		// Use CA cert pool loaded from Kubernetes secret
		tlsConfig.RootCAs = caPool
		logrus.Debugf("Using CA certificate pool for %s Redis TLS", componentName)
	} else if caPath != "" {
		// Load CA certificate from file
		caCertPEM, err := os.ReadFile(caPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read Redis CA certificate from %s: %w", caPath, err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCertPEM) {
			return nil, fmt.Errorf("failed to parse Redis CA certificate from %s", caPath)
		}
		tlsConfig.RootCAs = certPool
		logrus.WithField("caPath", caPath).Infof("Loaded Redis CA certificate for %s", componentName)
	} else {
		// No CA specified - require explicit configuration
		return nil, fmt.Errorf("redis TLS enabled but no CA certificate configured for %s: use --redis-ca-path, --redis-ca-secret-name, or --redis-tls-insecure", componentName)
	}

	return tlsConfig, nil
}
