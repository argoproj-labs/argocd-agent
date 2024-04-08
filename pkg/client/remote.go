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

	"github.com/golang-jwt/jwt/v5"
	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/jannfis/argocd-agent/internal/tlsutil"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/authapi"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/versionapi"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/wait"
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

// Remote represents a remote argocd-agent server component
type Remote struct {
	hostname     string
	port         int
	tlsConfig    *tls.Config
	accessToken  *token
	refreshToken *token
	authMethod   string
	creds        auth.Credentials
	backoff      wait.Backoff
	conn         *grpc.ClientConn
	clientID     string
	clientMode   types.AgentMode
	timeouts     timeouts
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
		c, err := tlsutil.TlsCertFromX509(cert, key)
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
		c, err := tlsutil.TlsCertFromFile(certPath, keyPath, true)
		if err != nil {
			return fmt.Errorf("unable to read TLS client cert: %v", err)
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

func NewRemote(hostname string, port int, opts ...RemoteOption) (*Remote, error) {
	r := &Remote{
		hostname:  hostname,
		port:      port,
		tlsConfig: &tls.Config{},
		backoff: wait.Backoff{
			Steps:    math.MaxInt,
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
	var nCtx context.Context = ctx
	if r.accessToken != nil && r.accessToken.RawToken != "" {
		nCtx = metadata.AppendToOutgoingContext(ctx, "authorization", r.accessToken.RawToken)
	}
	return invoker(nCtx, method, req, reply, cc, opts...)
}

func (r *Remote) streamAuthInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	log().Infof("Outgoing stream call to %s", method)
	var nCtx context.Context = ctx
	if r.accessToken != nil && r.accessToken.RawToken != "" {
		nCtx = metadata.AppendToOutgoingContext(ctx, "authorization", r.accessToken.RawToken)
	}
	return streamer(nCtx, desc, cc, method, opts...)
}

// Connect connects this Remote to the remote host and performs authentication.
// If the remote is configured with a retry, Connect will keep trying to
// establish a connection to the remote host until either the number of maximum
// retries has been reached or the context ctx is canceled or expired.
//
// When Connect returns nil, the connection was successfully established and an
// authentication token has been received.
func (r *Remote) Connect(ctx context.Context, forceReauth bool) error {
	creds := credentials.NewTLS(r.tlsConfig)
	cparams := grpc.ConnectParams{
		Backoff: backoff.DefaultConfig,
	}

	// Some default options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithConnectParams(cparams),
		grpc.WithUserAgent("argocd-agent/v0.0.1"),
		grpc.WithUnaryInterceptor(r.unaryAuthInterceptor),
		grpc.WithStreamInterceptor(r.streamAuthInterceptor),
	}

	conn, err := grpc.NewClient(r.Addr(), opts...)
	if err != nil {
		return err
	}

	authC := authapi.NewAuthenticationClient(conn)

	// We try to authenticate to the remote repeatedly, until either of the
	// following events happen:
	//
	// 1) The retry attempts are used up or
	// 2) The context expires or is canceled
	retries := r.backoff.Steps
	err = retry.OnError(r.backoff, r.retriable, func() error {
		retries -= 1
		select {
		case <-ctx.Done():
			return status.Error(codes.Canceled, "context canceled")
		default:
			resp, ierr := authC.Authenticate(ctx, &authapi.AuthRequest{Method: r.authMethod, Credentials: r.creds, Mode: r.clientMode.String()})
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, r.backoff.Step())
				return ierr
			}
			r.accessToken, ierr = NewToken(resp.AccessToken)
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, r.backoff.Step())
				return ierr
			}
			r.refreshToken, ierr = NewToken(resp.RefreshToken)
			if ierr != nil {
				logrus.Warnf("Auth failure: %v (retrying in %v)", ierr, r.backoff.Step())
				return ierr
			}
			r.clientID, ierr = r.accessToken.Claims.GetSubject()
			if ierr != nil {
				return err
			}
			return nil
		}
	})
	if err != nil {
		conn.Close()
		return err
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

func (r *Remote) Conn() *grpc.ClientConn {
	return r.conn
}

func (r *Remote) ClientID() string {
	return r.clientID
}

func (r *Remote) SetClientMode(mode types.AgentMode) {
	r.clientMode = mode
}

func log() *logrus.Entry {
	return logrus.WithField("module", "Connector")
}
