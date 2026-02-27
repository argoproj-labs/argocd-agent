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

package principal

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"regexp"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/grpcutil"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

type ServerOptions struct {
	serverName    string
	port          int
	address       string
	tlsCertPath   string
	tlsKeyPath    string
	tlsCert       *x509.Certificate
	tlsKey        crypto.PrivateKey
	tlsCiphers    []uint16
	tlsMinVersion uint16
	tlsMaxVersion uint16
	gracePeriod   time.Duration
	namespaces    []string
	signingKey    crypto.PrivateKey
	// unauthMethods is not currently implemented
	unauthMethods map[string]bool
	serveGRPC     bool
	// serveREST is not currently implemented
	serveREST       bool
	eventProcessors int64
	// metricsEnabled is not currently read
	metricsEnabled         bool
	metricsPort            int
	requireClientCerts     bool
	rootCa                 *x509.CertPool
	clientCertSubjectMatch bool
	redisAddress           string
	redisPassword          string
	redisCompressionType   cacheutil.RedisCompressionType
	healthzPort            int
	redisProxyDisabled     bool
	informerSyncTimeout    time.Duration
	maxGRPCMessageSize     int

	// insecurePlaintext disables TLS on the gRPC server. Use when Istio sidecar
	// handles mTLS termination.
	insecurePlaintext bool

	// redisProxyLogger, resourceProxyLogger, and grpcEventLogger are loggers for various subsystems
	redisProxyLogger    *logging.CentralizedLogger
	resourceProxyLogger *logging.CentralizedLogger
	grpcEventLogger     *logging.CentralizedLogger

	// destinationBasedMapping enables mapping applications to agents based on
	// spec.destination.name instead of the application's namespace
	destinationBasedMapping bool

	selfAgentRegistrationEnabled bool
	resourceProxyAddress         string
	clientCertSecretName         string
	// Redis TLS configuration
	redisTLSEnabled             bool
	redisProxyServerTLSCert     *x509.Certificate
	redisProxyServerTLSKey      crypto.PrivateKey
	redisProxyServerTLSCertPath string
	redisProxyServerTLSKeyPath  string
	redisTLSCA                  *x509.CertPool
	redisTLSCAPath              string
	redisTLSInsecure            bool
}

type ServerOption func(o *Server) error

// defaultOptions returns a set of default options for the server
func defaultOptions() *ServerOptions {
	return &ServerOptions{
		port:                 443,
		address:              "",
		tlsMinVersion:        tls.VersionTLS13,
		unauthMethods:        make(map[string]bool),
		eventProcessors:      10,
		rootCa:               x509.NewCertPool(),
		informerSyncTimeout:  60 * time.Second,
		maxGRPCMessageSize:   grpcutil.DefaultGRPCMaxMessageSize,
		resourceProxyAddress: "argocd-agent-resource-proxy:9090",
	}
}

// WithEventProcessors sets the maximum number of event processors to run
// concurrently.
func WithEventProcessors(numProcessors int64) ServerOption {
	return func(o *Server) error {
		o.options.eventProcessors = numProcessors
		return nil
	}
}

// WithTokenSigningKey sets the RSA private key to use for signing the tokens
// issued by the Server
func WithTokenSigningKey(key crypto.PrivateKey) ServerOption {
	return func(o *Server) error {
		o.options.signingKey = key
		return nil
	}
}

// WithGeneratedTokenSigningKey generates a temporary JWT signing key. Note
// that this option should only be used for testing.
//
// INSECURE: Do not use this in production.
func WithGeneratedTokenSigningKey() ServerOption {
	return func(o *Server) error {
		log().Warnf("INSECURE: Generating and using a volatile token signing key - multiple replicas not possible")
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return fmt.Errorf("could not generate signing key: %w", err)
		}
		o.options.signingKey = key
		return nil
	}
}

// WithTokenSigningKey sets the RSA private key to use for signing the tokens
// issued by the Server
func WithTokenSigningKeyFromFile(path string) ServerOption {
	return func(o *Server) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		bytes, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		pemBlock, _ := pem.Decode(bytes)
		if pemBlock == nil {
			return fmt.Errorf("%s contains malformed PEM data", path)
		}
		key, err := x509.ParsePKCS8PrivateKey(pemBlock.Bytes)
		if err != nil {
			return fmt.Errorf("could not parse RSA key: %w", err)
		}
		o.options.signingKey = key
		return nil
	}
}

// WithTokenSigningKeyFromSecret sets the RSA private key to use for signing the tokens
// issued by the Server. The key will be loaded from the secret referred to by name and namespace.
// The secret should contain a JWT signing key in the "jwt.key" field.
func WithTokenSigningKeyFromSecret(kube kubernetes.Interface, namespace, name string) ServerOption {
	return func(o *Server) error {
		key, err := tlsutil.JWTSigningKeyFromSecret(context.Background(), kube, namespace, name)
		if err != nil {
			return err
		}
		o.options.signingKey = key
		return nil
	}
}

// WithListenerPort sets the listening port for the server. If the port is not
// valid, an error is returned.
func WithListenerPort(port int) ServerOption {
	return func(o *Server) error {
		if port < 0 || port > 65535 {
			return fmt.Errorf("port must be between 0 and 65535")
		}
		o.options.port = port
		return nil
	}
}

// WithListenerAddress sets the address the server should listen on.
func WithListenerAddress(host string) ServerOption {
	return func(o *Server) error {
		o.options.address = host
		return nil
	}
}

// WithClientCertSubjectMatch sets whether the subject of a client certificate
// presented by the agent must match the agent's name. Has no effect if client
// certificates are not required.
func WithClientCertSubjectMatch(match bool) ServerOption {
	return func(o *Server) error {
		o.options.clientCertSubjectMatch = match
		return nil
	}
}

// WithTLSRootCaFromFile loads the root CAs to be used to validate client
// certificates from the file at caPath.
func WithTLSRootCaFromFile(caPath string) ServerOption {
	return func(o *Server) error {
		pem, err := os.ReadFile(caPath)
		if err != nil {
			return err
		}
		ok := o.options.rootCa.AppendCertsFromPEM(pem)
		if !ok {
			return fmt.Errorf("invalid certificate data in %s", caPath)
		}
		return nil
	}
}

// WithTLSRootCaFromSecret loads the root CAs to be used to validate client
// certificates from the Kubernetes Secret referred to by namespace and name.
// If field is non-empty, only loads certificates stored in the named field.
// Otherwise, if field is empty, loads certificates from all fields in the
// Secret.
func WithTLSRootCaFromSecret(kube kubernetes.Interface, namespace, name string, fields ...string) ServerOption {
	return func(o *Server) error {
		pool, err := tlsutil.X509CertPoolFromSecret(context.Background(), kube, namespace, name, fields...)
		if err != nil {
			return err
		}
		o.options.rootCa = pool
		return nil
	}
}

// WithRequireClientCerts sets whether all incoming agent connections must
// present a valid client certificate before being accepted.
func WithRequireClientCerts(require bool) ServerOption {
	return func(o *Server) error {
		o.options.requireClientCerts = require
		return nil
	}
}

// WithTLSKeyPair configures the TLS certificate and private key to be used by
// the server. The key must not be passphrase protected.
func WithTLSKeyPair(cert *x509.Certificate, key *rsa.PrivateKey) ServerOption {
	return func(o *Server) error {
		o.options.tlsCert = cert
		o.options.tlsKey = key
		return nil
	}
}

// WithTLSKeyPairFromPath configures the TLS certificate and private key to be used by
// the server. The function will not check whether the files exists, or if they
// contain valid data because it is assumed that they may be created at a later
// point in time.
func WithTLSKeyPairFromPath(certPath, keyPath string) ServerOption {
	return func(o *Server) error {
		o.options.tlsCertPath = certPath
		o.options.tlsKeyPath = keyPath
		return nil
	}
}

// WithTLSKeyPairFromSecret configures the TLS certificate and private key to
// be used by the server. The keypair will be loaded from the secret referred
// to by name and namespace. The secret must be of type tls.
func WithTLSKeyPairFromSecret(kube kubernetes.Interface, namespace, name string) ServerOption {
	return func(o *Server) error {
		c, err := tlsutil.TLSCertFromSecret(context.Background(), kube, namespace, name)
		if err != nil {
			return err
		}
		if len(c.Certificate) == 0 || c.Certificate[0] == nil {
			return fmt.Errorf("no certificate data in secret %s/%s", namespace, name)
		}
		cert, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return fmt.Errorf("could not parse certificate: %w", err)
		}
		o.options.tlsCert = cert
		o.options.tlsKey = c.PrivateKey
		return nil
	}
}

// WithGeneratedTLS configures the server to generate and use a new TLS keypair
// upon startup.
//
// INSECURE: Do not use in production.
func WithGeneratedTLS(serverName string) ServerOption {
	log().Warnf("INSECURE: Generating and using a self-signed, volatile TLS certificate")
	return func(o *Server) error {
		templ := x509.Certificate{
			SerialNumber:          big.NewInt(1),
			Subject:               pkix.Name{CommonName: serverName},
			DNSNames:              []string{serverName},
			IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
			Issuer:                pkix.Name{CommonName: serverName},
			NotBefore:             time.Now().Add(-1 * time.Hour),
			NotAfter:              time.Now().Add(1 * time.Hour),
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			BasicConstraintsValid: true,
		}
		pKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return err
		}
		db, err := x509.CreateCertificate(rand.Reader, &templ, &templ, &pKey.PublicKey, pKey)
		if err != nil {
			return err
		}
		cert, err := x509.ParseCertificate(db)
		if err != nil {
			return err
		}
		o.options.tlsCert = cert
		o.options.tlsKey = pKey
		return nil
	}
}

// WithTLSCipherSuites configures the TLS cipher suites to be used by the server.
// If an unknown cipher suite is specified, an error is returned.
func WithTLSCipherSuites(cipherSuites []string) ServerOption {
	return func(o *Server) error {
		cipherIDs, err := tlsutil.ParseCipherSuites(cipherSuites)
		if err != nil {
			return err
		}
		o.options.tlsCiphers = cipherIDs
		return nil
	}
}

// WithMinimumTLSVersion configures the minimum TLS version to be accepted by
// the server.
func WithMinimumTLSVersion(version string) ServerOption {
	return func(o *Server) error {
		v, err := tlsutil.TLSVersionFromName(version)
		if err != nil {
			return err
		}
		o.options.tlsMinVersion = v
		return nil
	}
}

// WithMaximumTLSVersion configures the maximum TLS version to be accepted by
// the server.
func WithMaximumTLSVersion(version string) ServerOption {
	return func(o *Server) error {
		v, err := tlsutil.TLSVersionFromName(version)
		if err != nil {
			return err
		}
		o.options.tlsMaxVersion = v
		return nil
	}
}

// WithShutDownGracePeriod configures how long the server should wait for
// client connections to close during shutdown. If d is 0, the server will
// not use a grace period for shutdown but instead close immediately.
func WithShutDownGracePeriod(d time.Duration) ServerOption {
	return func(o *Server) error {
		o.options.gracePeriod = d
		return nil
	}
}

// WithNamespaces sets an
func WithNamespaces(namespaces ...string) ServerOption {
	return func(o *Server) error {
		o.options.namespaces = namespaces
		return nil
	}
}

func WithGRPC(serveGRPC bool) ServerOption {
	return func(o *Server) error {
		o.options.serveGRPC = serveGRPC
		return nil
	}
}

func WithREST(serveREST bool) ServerOption {
	return func(o *Server) error {
		o.options.serveREST = serveREST
		return nil
	}
}

func WithServerName(serverName string) ServerOption {
	return func(o *Server) error {
		o.options.serverName = serverName
		return nil
	}
}

func WithMetricsPort(port int) ServerOption {
	return func(o *Server) error {
		if port > 0 && port < 32768 {
			o.options.metricsEnabled = true
			o.options.metricsPort = port
			return nil
		} else {
			return fmt.Errorf("invalid port: %d", port)
		}
	}
}

func WithAuthMethods(am *auth.Methods) ServerOption {
	return func(o *Server) error {
		o.authMethods = am
		return nil
	}
}

func WithAutoNamespaceCreate(enabled bool, pattern string, labels map[string]string) ServerOption {
	return func(o *Server) error {
		var err error
		o.autoNamespaceAllow = enabled
		o.autoNamespaceLabels = labels
		if pattern != "" {
			o.autoNamespacePattern, err = regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("invalid auto-namespace pattern: %w", err)
			}
		}
		return nil
	}
}

func WithWebSocket(enableWebSocket bool) ServerOption {
	return func(o *Server) error {
		o.enableWebSocket = enableWebSocket
		return nil
	}
}

// WithRedisProxyDisabled disables the Redis proxy for testing.
func WithRedisProxyDisabled() ServerOption {
	return func(o *Server) error {
		o.options.redisProxyDisabled = true
		return nil
	}
}

// WithInformerSyncTimeout sets the informer sync timeout duration.
func WithInformerSyncTimeout(timeout time.Duration) ServerOption {
	return func(o *Server) error {
		o.options.informerSyncTimeout = timeout
		return nil
	}
}

func WithResourceProxyEnabled(enabled bool) ServerOption {
	return func(o *Server) error {
		o.resourceProxyEnabled = enabled
		return nil
	}
}

func WithResourceProxyTLS(tlsConfig *tls.Config) ServerOption {
	return func(o *Server) error {
		o.resourceProxyTLSConfig = tlsConfig
		return nil
	}
}

func WithKeepAliveMinimumInterval(interval time.Duration) ServerOption {
	return func(o *Server) error {
		o.keepAliveMinimumInterval = interval
		return nil
	}
}

func WithRedis(redisAddress, redisPassword, redisCompressionTypeStr string) ServerOption {
	return func(o *Server) error {
		redisCompressionType, err := cacheutil.CompressionTypeFromString(redisCompressionTypeStr)
		if err != nil {
			return err
		}
		o.options.redisCompressionType = redisCompressionType
		o.options.redisAddress = redisAddress
		o.options.redisPassword = redisPassword

		return nil
	}
}

func WithHealthzPort(port int) ServerOption {
	return func(o *Server) error {
		if port > 0 && port < 32768 {
			o.options.healthzPort = port
			return nil
		} else {
			return fmt.Errorf("invalid port: %d", port)
		}
	}
}

// WithMaxGRPCMessageSize configures the maximum gRPC message size (in bytes)
// for both sending and receiving on the principal server.
func WithMaxGRPCMessageSize(size int) ServerOption {
	return func(o *Server) error {
		if size <= 0 {
			return fmt.Errorf("grpc max message size must be greater than 0")
		}
		o.options.maxGRPCMessageSize = size
		return nil
	}
}

// WithInsecurePlaintext disables TLS on the gRPC server. This should only be
// used when running behind a service mesh (e.g., Istio) that handles mTLS
// termination at the sidecar level.
//
// INSECURE: Do not use this without a service mesh providing transport security.
func WithInsecurePlaintext() ServerOption {
	return func(o *Server) error {
		log().Warn("INSECURE: gRPC server will run in plaintext mode - ensure Istio or similar service mesh provides mTLS")
		o.options.insecurePlaintext = true
		return nil
	}
}

func WithSubsystemLoggers(resourceProxy, redisProxy, grpcEvent *logrus.Logger) ServerOption {
	return func(o *Server) error {
		if redisProxy != nil {
			o.options.redisProxyLogger = logging.New(redisProxy)
		} else {
			o.options.redisProxyLogger = logging.GetDefaultLogger()
		}

		if resourceProxy != nil {
			o.options.resourceProxyLogger = logging.New(resourceProxy)
		} else {
			o.options.resourceProxyLogger = logging.GetDefaultLogger()
		}

		if grpcEvent != nil {
			o.options.grpcEventLogger = logging.New(grpcEvent)
		} else {
			o.options.grpcEventLogger = logging.GetDefaultLogger()
		}
		return nil
	}
}

// WithDestinationBasedMapping enables destination-based mapping for applications.
// When enabled, applications are mapped to agents based on spec.destination.name
// instead of the application's namespace. This allows applications to exist in
// any namespace on the principal while still being routed to the correct agent.
func WithDestinationBasedMapping(enabled bool) ServerOption {
	return func(o *Server) error {
		o.options.destinationBasedMapping = enabled
		return nil
	}
}

func WithAgentRegistration(enabled bool) ServerOption {
	return func(o *Server) error {
		o.options.selfAgentRegistrationEnabled = enabled
		return nil
	}
}

func WithResourceProxyAddress(address string) ServerOption {
	return func(o *Server) error {
		o.options.resourceProxyAddress = address
		return nil
	}
}

func WithClientCertSecretName(name string) ServerOption {
	return func(o *Server) error {
		o.options.clientCertSecretName = name
		return nil
	}
}

// WithRedisTLSEnabled enables or disables TLS for Redis connections
func WithRedisTLSEnabled(enabled bool) ServerOption {
	return func(o *Server) error {
		o.options.redisTLSEnabled = enabled
		return nil
	}
}

// WithRedisProxyServerTLSFromPath configures the TLS certificate and private key for the Redis proxy server
func WithRedisProxyServerTLSFromPath(certPath, keyPath string) ServerOption {
	return func(o *Server) error {
		o.options.redisProxyServerTLSCertPath = certPath
		o.options.redisProxyServerTLSKeyPath = keyPath
		return nil
	}
}

// WithRedisProxyServerTLSFromSecret configures the TLS certificate and private key for the Redis proxy server from a Kubernetes secret
func WithRedisProxyServerTLSFromSecret(kube kubernetes.Interface, namespace, name string) ServerOption {
	return func(o *Server) error {
		c, err := tlsutil.TLSCertFromSecret(context.Background(), kube, namespace, name)
		if err != nil {
			return err
		}
		if len(c.Certificate) == 0 || c.Certificate[0] == nil {
			return fmt.Errorf("no certificate data in secret %s/%s", namespace, name)
		}
		cert, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return fmt.Errorf("could not parse certificate: %w", err)
		}

		o.options.redisProxyServerTLSCert = cert
		o.options.redisProxyServerTLSKey = c.PrivateKey
		return nil
	}
}

// WithRedisTLSCAFromFile loads the CA certificate to validate the Redis TLS certificate
func WithRedisTLSCAFromFile(caPath string) ServerOption {
	return func(o *Server) error {
		o.options.redisTLSCAPath = caPath
		return nil
	}
}

// WithRedisTLSCAFromSecret loads the CA certificate from a Kubernetes secret to validate the Redis TLS certificate
func WithRedisTLSCAFromSecret(kube kubernetes.Interface, namespace, name, field string) ServerOption {
	return func(o *Server) error {
		pool, err := tlsutil.X509CertPoolFromSecret(context.Background(), kube, namespace, name, field)
		if err != nil {
			return err
		}
		o.options.redisTLSCA = pool
		return nil
	}
}

// WithRedisTLSInsecure allows insecure TLS connections to Redis (for testing only)
func WithRedisTLSInsecure(insecure bool) ServerOption {
	return func(o *Server) error {
		o.options.redisTLSInsecure = insecure
		return nil
	}
}
