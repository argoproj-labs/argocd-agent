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

package client

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/logging"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/versionapi"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	grpchttp1client "golang.stackrox.io/grpc-http1/client"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

type timeouts struct {
	dialTimeout         time.Duration
	tlsHandshakeTimeout time.Duration
}

type token struct {
	RawToken string
	Claims   jwt.Claims
}

func NewToken(tok string) (*token, error) {
	r := &token{}
	r.RawToken = tok
	parser := jwt.NewParser()
	t, _, err := parser.ParseUnverified(tok, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	r.Claims = t.Claims
	return r, nil
}

// Remote represents a remote argocd-agent server component. Remote is used only by the agent component, and not by principal.
type Remote struct {
	hostname          string
	port              int
	tlsConfig         *tls.Config
	accessToken       *token
	refreshToken      *token
	authMethod        string
	creds             auth.Credentials
	backoff           wait.Backoff
	conn              *grpc.ClientConn
	clientID          string
	clientMode        types.AgentMode
	timeouts          timeouts
	enableWebSocket   bool
	enableCompression bool
	// insecurePlaintext disables TLS for the connection. Use when Istio sidecar
	// handles mTLS termination.
	insecurePlaintext bool

	// Time interval for agent to principal ping
	keepAlivePingInterval time.Duration
}

type RemoteOption func(r *Remote) error

func (r *Remote) WithBackoff(backoff wait.Backoff) RemoteOption {
	return func(r *Remote) error {
		r.backoff = backoff
		return nil
	}
}

func (r *Remote) WithDialTimeout(t time.Duration) RemoteOption {
	return func(r *Remote) error {
		r.timeouts.dialTimeout = t
		return nil
	}
}

func (r *Remote) WithTLSHandShakeTimeout(t time.Duration) RemoteOption {
	return func(r *Remote) error {
		r.timeouts.tlsHandshakeTimeout = t
		return nil
	}
}

func WithTLSClientCert(cert *x509.Certificate, key crypto.PrivateKey) RemoteOption {
	return func(r *Remote) error {
		c, err := tlsutil.TLSCertFromX509(cert, key)
		if err != nil {
			return err
		}
		r.tlsConfig.Certificates = append(r.tlsConfig.Certificates, c)
		return nil
	}
}

// WithTLSClientCertFromBytes configures the Remote to present the client cert
// given as certData and private key given as keyData on every outbound
// connection. Both, certData and keyData must be PEM encoded.
func WithTLSClientCertFromBytes(certData []byte, keyData []byte) RemoteOption {
	return func(r *Remote) error {
		c, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			return err
		}
		r.tlsConfig.Certificates = append(r.tlsConfig.Certificates, c)
		return nil
	}
}

// WithTLSClientCertFromFile configures the Remote to present the client cert
// loaded from certPath and private key loaded from keyPath on every outbound
// connection.
func WithTLSClientCertFromFile(certPath, keyPath string) RemoteOption {
	return func(r *Remote) error {
		c, err := tlsutil.TLSCertFromFile(certPath, keyPath, true)
		if err != nil {
			return fmt.Errorf("unable to read TLS client cert: %v", err)
		}
		r.tlsConfig.Certificates = append(r.tlsConfig.Certificates, c)
		return nil
	}
}

// WithTLSClientCertFromSecret configures the remote to present the client cert
// loaded from the secret on every outbound connection.
func WithTLSClientCertFromSecret(kube kubernetes.Interface, namespace, name string) RemoteOption {
	return func(r *Remote) error {
		c, err := tlsutil.TLSCertFromSecret(context.Background(), kube, namespace, name)
		if err != nil {
			return fmt.Errorf("unable to read TLS client from secret: %v", err)
		}
		r.tlsConfig.Certificates = append(r.tlsConfig.Certificates, c)
		return nil
	}
}

// WithRootAuthorities configures the Remote to use TLS certificate authorities
// from PEM data in caData for verifying server certificates.
func WithRootAuthorities(caData []byte) RemoteOption {
	return func(r *Remote) error {
		pool := x509.NewCertPool()
		ok := pool.AppendCertsFromPEM(caData)
		if !ok {
			return fmt.Errorf("invalid PEM data")
		}
		r.tlsConfig.RootCAs = pool
		return nil
	}
}

// WithRootAuthoritiesFromFile configures the Remote to use TLS certificate
// authorities from file caPath for verifying server certificates.
func WithRootAuthoritiesFromFile(caPath string) RemoteOption {
	return func(r *Remote) error {
		pemData, err := os.ReadFile(caPath)
		if err != nil {
			return err
		}
		pool := x509.NewCertPool()
		ok := pool.AppendCertsFromPEM(pemData)
		if !ok {
			return fmt.Errorf("invalid PEM data in %s", caPath)
		}
		r.tlsConfig.RootCAs = pool
		//nolint:staticcheck
		log().Infof("Loaded %d cert(s) into the root CA pool", len(r.tlsConfig.RootCAs.Subjects()))
		return nil
	}
}

// WithRootAuthoritiesFromSecret configures the remote to use TLS certificate
// authorities from the secret specified by name and namespace. If field is
// non-empty, the root CA's certificate will be loaded only from the given
// field. Otherwise, the ConfigMap is expected to contain one or more
// certificates in each field of the ConfigMap, and all certificates will be
// loaded into the certificate pool.
func WithRootAuthoritiesFromSecret(kube kubernetes.Interface, namespace, name, field string) RemoteOption {
	return func(r *Remote) error {
		pool, err := tlsutil.X509CertPoolFromSecret(context.Background(), kube, namespace, name, field)
		if err != nil {
			return err
		}
		r.tlsConfig.RootCAs = pool
		return nil
	}
}

// WithInsecureSkipTLSVerify configures the Remote to skip verification of the
// TLS server certificate
func WithInsecureSkipTLSVerify() RemoteOption {
	return func(r *Remote) error {
		r.tlsConfig.InsecureSkipVerify = true
		return nil
	}
}

// WithInsecurePlaintext disables TLS for the connection. This should only be
// used when running behind a service mesh (e.g., Istio) that handles mTLS
// termination at the sidecar level.
//
// INSECURE: Do not use this without a service mesh providing transport security.
func WithInsecurePlaintext() RemoteOption {
	return func(r *Remote) error {
		log().Warn("INSECURE: Agent will connect without TLS - ensure Istio or similar service mesh provides mTLS")
		r.insecurePlaintext = true
		return nil
	}
}

// The agent will rely on gRPC over WebSocket for bi-directional streaming. This option could be enabled
// when there is an intermediate component that is HTTP/2 incompatible and downgrades the incoming request to HTTP/1.1
func WithWebSocket(enableWebSocket bool) RemoteOption {
	return func(r *Remote) error {
		r.enableWebSocket = enableWebSocket
		return nil
	}
}

func WithAuth(method string, creds auth.Credentials) RemoteOption {
	return func(r *Remote) error {
		r.authMethod = method
		r.creds = creds
		return nil
	}
}

func WithClientMode(mode types.AgentMode) RemoteOption {
	return func(r *Remote) error {
		r.clientMode = mode
		return nil
	}
}

func WithKeepAlivePingInterval(interval time.Duration) RemoteOption {
	return func(r *Remote) error {
		r.keepAlivePingInterval = interval
		return nil
	}
}

func WithCompression(flag bool) RemoteOption {
	return func(r *Remote) error {
		r.enableCompression = flag
		return nil
	}
}

// WithMinimumTLSVersion configures the minimum TLS version the client will accept.
func WithMinimumTLSVersion(version string) RemoteOption {
	return func(r *Remote) error {
		v, err := tlsutil.TLSVersionFromName(version)
		if err != nil {
			return err
		}
		r.tlsConfig.MinVersion = v
		return nil
	}
}

// WithMaximumTLSVersion configures the maximum TLS version the client will use.
func WithMaximumTLSVersion(version string) RemoteOption {
	return func(r *Remote) error {
		v, err := tlsutil.TLSVersionFromName(version)
		if err != nil {
			return err
		}
		r.tlsConfig.MaxVersion = v
		return nil
	}
}

// WithTLSCipherSuites configures the TLS cipher suites the client will use.
// If an unknown cipher suite is specified, an error is returned.
func WithTLSCipherSuites(cipherSuites []string) RemoteOption {
	return func(r *Remote) error {
		cipherIDs, err := tlsutil.ParseCipherSuites(cipherSuites)
		if err != nil {
			return err
		}
		r.tlsConfig.CipherSuites = cipherIDs
		return nil
	}
}

func NewRemote(hostname string, port int, opts ...RemoteOption) (*Remote, error) {
	r := &Remote{
		hostname: hostname,
		port:     port,
		tlsConfig: &tls.Config{
			ServerName: hostname,
		},
		backoff: wait.Backoff{
			Duration: 1 * time.Second,
			Factor:   2,
			Cap:      1 * time.Minute,
		},
		clientMode: types.AgentModeAutonomous,
	}
	for _, o := range opts {
		if err := o(r); err != nil {
			return nil, err
		}
	}

	// Validate TLS configuration after all options have been applied
	if err := tlsutil.ValidateTLSConfig(r.tlsConfig.MinVersion, r.tlsConfig.MaxVersion, r.tlsConfig.CipherSuites); err != nil {
		return nil, err
	}

	return r, nil
}

// Hostname returns the name of the host this remote connects to
func (r *Remote) Hostname() string {
	return r.hostname
}

// Port returns the port number this remote connects to on the remote host
func (r *Remote) Port() int {
	return r.port
}

// Addr returns a string representation of the address this remote connects to
func (r *Remote) Addr() string {
	return fmt.Sprintf("%s:%d", r.hostname, r.port)
}

// Creds returns the credentials this Remote uses to connect to the remote host
func (r *Remote) Creds() auth.Credentials {
	return r.creds
}

func (r *Remote) retriable(err error) bool {
	st, ok := status.FromError(err)
	if ok && st.Code() == codes.Canceled {
		return false
	}
	return true
}

func (r *Remote) unaryAuthInterceptor(ctx context.Context, method string, req interface{}, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	log().Infof("Outgoing unary call to %s", method)
	nCtx := ctx
	if r.accessToken != nil && r.accessToken.RawToken != "" {
		nCtx = metadata.AppendToOutgoingContext(ctx, "authorization", r.accessToken.RawToken)
	}
	return invoker(nCtx, method, req, reply, cc, opts...)
}

func (r *Remote) streamAuthInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	log().Infof("Outgoing stream call to %s", method)
	nCtx := ctx
	if r.accessToken != nil && r.accessToken.RawToken != "" {
		nCtx = metadata.AppendToOutgoingContext(ctx, "authorization", r.accessToken.RawToken)
	}
	return streamer(nCtx, desc, cc, method, opts...)
}

func connectBackoff() wait.Backoff {
	return wait.Backoff{
		Steps:    math.MaxInt,
		Duration: 1 * time.Second,
		Factor:   1.2,
		Cap:      1 * time.Minute,
	}
}

// Connect connects this Remote to the remote host and performs authentication.
// If the remote is configured with a retry, Connect will keep trying to
// establish a connection to the remote host until either the number of maximum
// retries has been reached or the context ctx is canceled or expired.
//
// When Connect returns nil, the connection was successfully established and an
// authentication token has been received.
func (r *Remote) Connect(ctx context.Context, forceReauth bool) error {
	cparams := grpc.ConnectParams{
		MinConnectTimeout: 365 * 24 * time.Hour,
	}

	// Some default options
	opts := []grpc.DialOption{
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		grpc.WithConnectParams(cparams),
		grpc.WithUserAgent("argocd-agent/v0.0.1"),
		grpc.WithUnaryInterceptor(r.unaryAuthInterceptor),
		grpc.WithStreamInterceptor(r.streamAuthInterceptor),
	}

	if r.enableCompression {
		log().Debug("gRPC compression is enabled.")
		opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor(gzip.Name)))
	}

	var (
		conn *grpc.ClientConn
		err  error
	)
	if r.enableWebSocket {
		grpcHTTP1Opts := []grpchttp1client.ConnectOption{
			grpchttp1client.UseWebSocket(true),
			grpchttp1client.DialOpts(opts...),
		}

		// Use nil TLS config for plaintext mode (WebSocket over HTTP)
		var tlsCfg *tls.Config
		if !r.insecurePlaintext {
			tlsCfg = r.tlsConfig
		}
		conn, err = grpchttp1client.ConnectViaProxy(ctx, r.Addr(), tlsCfg, grpcHTTP1Opts...)
		if err != nil {
			return err
		}
	} else {
		// Use insecure credentials for plaintext mode (e.g., behind Istio)
		if r.insecurePlaintext {
			opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
		} else {
			opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(r.tlsConfig)))
		}

		if r.keepAlivePingInterval != 0 {
			log().Debugf("Agent ping to principal is enabled, agent will send a ping event after every %s.", r.keepAlivePingInterval)
			opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: r.keepAlivePingInterval}))
		}

		conn, err = grpc.NewClient(r.Addr(), opts...)
		if err != nil {
			return err
		}
	}

	authC := authapi.NewAuthenticationClient(conn)

	authenticated := false
	cBackoff := connectBackoff()

	// We try to authenticate to the remote repeatedly, until either of the
	// following events happen:
	//
	// 1) The retry attempts are used up or
	// 2) The context expires or is canceled
	err = retry.OnError(cBackoff, r.retriable, func() error {
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "context canceled")
		default:
			resp, ierr := authC.Authenticate(ctx, &authapi.AuthRequest{Method: r.authMethod, Credentials: r.creds, Mode: r.clientMode.String()})
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, cBackoff.Step())
				return ierr
			}
			r.accessToken, ierr = NewToken(resp.AccessToken)
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, cBackoff.Step())
				return ierr
			}
			r.refreshToken, ierr = NewToken(resp.RefreshToken)
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, cBackoff.Step())
				return ierr
			}
			r.clientID, ierr = r.accessToken.Claims.GetSubject()
			if ierr != nil {
				return err
			}
			authenticated = true
			return nil
		}
	})
	if err != nil {
		conn.Close()
		return err
	}

	// Gotta make sure we went through the retry.OnError loop at least once and
	// successfully authenticated during execution.
	if !authenticated {
		conn.Close()
		return fmt.Errorf("unknown authentication failure")
	}
	log().Infof("Authentication successful")
	versionC := versionapi.NewVersionClient(conn)
	vr, err := versionC.Version(ctx, &versionapi.VersionRequest{})
	if err != nil {
		conn.Close()
		return err
	}
	log().Infof("Connected to %s", vr.Version)
	r.conn = conn
	return nil
}

// Conn returns this remote's underlying gRPC connection object. It should
// be treated as read-only.
func (r *Remote) Conn() *grpc.ClientConn {
	return r.conn
}

// ClientID returns the client ID used by this remote
func (r *Remote) ClientID() string {
	return r.clientID
}

// AuthMethod returns the name of the auth method configured for this remote
func (r *Remote) AuthMethod() string {
	return r.authMethod
}

// SetClientMode sets the client mode to be used by this remote
func (r *Remote) SetClientMode(mode types.AgentMode) {
	r.clientMode = mode
}

// SetClientID sets the client ID for this remote
// The only use case for this is to be used in unit testing.
func (r *Remote) SetClientID(id string) {
	r.clientID = id
}

func log() *logrus.Entry {
	return logging.ModuleLogger("Connector")
}
