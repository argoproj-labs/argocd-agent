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
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/logstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/terminalstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/versionapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/auth"
	"github.com/argoproj-labs/argocd-agent/principal/apis/eventstream"
	"github.com/argoproj-labs/argocd-agent/principal/apis/version"
	grpchttp1server "golang.stackrox.io/grpc-http1/server"
	_ "google.golang.org/grpc/encoding/gzip"
)

const listenerRetries = 5

var listenerBackoff = wait.Backoff{
	Steps:    listenerRetries,
	Duration: 2 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

// Listener is a utility wrapper around net.Listener and associated data
type Listener struct {
	host   string
	port   int
	l      net.Listener
	ctx    context.Context
	cancel context.CancelFunc
}

func parseAddress(address string) (string, int, error) {
	splitter := ":"
	if strings.Contains(address, "]:") {
		splitter = "]:"
	}
	toks := strings.Split(address, splitter)
	if len(toks) != 2 {
		return "", 0, fmt.Errorf("unexpected data: %s", address)
	}
	port, err := strconv.ParseUint(toks[1], 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("unexpected port specification: %s", toks[1])
	}
	host := toks[0]
	if splitter == "]:" {
		host += "]"
	}
	// Type conversion is safe because we limited the bitsize for ParseUint
	return host, int(port), nil
}

func addrToListener(l net.Listener) (*Listener, error) {
	host, port, err := parseAddress(l.Addr().String())
	if err != nil {
		return nil, err
	}
	return &Listener{host: host, port: port, l: l}, nil
}

// Listen configures and starts the server's TCP listener.
func (s *Server) Listen(ctx context.Context, backoff wait.Backoff) error {
	var c net.Listener
	var err error
	try := 1
	bind := fmt.Sprintf("%s:%d", s.options.address, s.options.port)
	// It should not be a fatal failure if the listener could not be started.
	// Instead, retry with backoff until the context has expired or the
	// number of maximum retries has been exceeded.
	err = wait.ExponentialBackoff(backoff, func() (done bool, err error) {
		var lerr error
		if try == 1 {
			s.logGrpcEvent().Debugf("Starting TCP listener on %s", bind)
		}
		// Even though we load TLS configuration here, we will not yet create
		// a TLS listener. TLS will be setup using the appropriate grpc-go API
		// functions.
		s.tlsConfig, lerr = s.loadTLSConfig()
		if lerr != nil {
			return false, lerr
		}
		// Start the TCP listener and bail out on errors.
		c, lerr = net.Listen("tcp", bind)
		if lerr != nil {
			s.logGrpcEvent().WithError(err).Debugf("Retrying to start TCP listener on %s (retry %d/%d)", bind, try, listenerRetries)
			try += 1
			return false, lerr
		}
		return true, nil
	})
	// The following condition will probably never be true
	if err != nil {
		return err
	}

	s.logGrpcEvent().Infof("Now listening on %s", c.Addr().String())
	s.listener, err = addrToListener(c)
	if err == nil {
		if ctx == nil {
			s.listener.ctx, s.listener.cancel = context.WithCancel(context.Background())
		} else {
			s.listener.ctx, s.listener.cancel = context.WithCancel(ctx)
		}
	}
	return err
}

func (s *Server) serveGRPC(ctx context.Context, metrics *metrics.PrincipalMetrics, errch chan error) error {
	err := s.Listen(ctx, listenerBackoff)
	if err != nil {
		return fmt.Errorf("could not start listener: %w", err)
	}

	grpcOpts := []grpc.ServerOption{
		// Global stats handler for tracing
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		// Global interceptors for gRPC streams
		grpc.ChainStreamInterceptor(
			s.streamRequestLogger(), // logging
			s.streamAuthInterceptor, // auth
		),
		// Global interceptors for gRPC unary calls
		grpc.ChainUnaryInterceptor(
			s.unaryRequestLogger(), // logging
			s.unaryAuthInterceptor, // auth
		),
	}

	// Add TLS credentials unless running in plaintext mode (e.g., behind Istio)
	if s.tlsConfig != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(s.tlsConfig)))
	} else {
		log().Warn("gRPC server running without TLS - ensure service mesh provides transport security")
	}

	if s.keepAliveMinimumInterval != 0 {
		s.logGrpcEvent().Debugf("Agent ping to principal is enabled, agent should wait at least %s before sending next ping event to principal", s.keepAliveMinimumInterval)
		grpcOpts = append(grpcOpts, grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: s.keepAliveMinimumInterval}))
	}

	// Instantiate server with given opts
	s.grpcServer = grpc.NewServer(grpcOpts...)

	// Register all gRPC services with the server
	if err := s.registerGrpcServices(metrics); err != nil {
		return fmt.Errorf("could not register gRPC services: %w", err)
	}

	// If the server is configured with HTTP/1 support enabled, configured and
	// start the downgrading proxy. Otherwise, start the gRPC server.
	if s.enableWebSocket {
		opts := []grpchttp1server.Option{grpchttp1server.PreferGRPCWeb(true)}

		downgradingHandler := grpchttp1server.CreateDowngradingHandler(s.grpcServer, http.NotFoundHandler(), opts...)
		downgradingServer := &http.Server{
			TLSConfig: s.tlsConfig,
			Handler:   h2c.NewHandler(downgradingHandler, &http2.Server{}),
		}

		go func() {
			// Use plaintext HTTP if TLS is disabled (e.g., behind Istio)
			if s.tlsConfig != nil {
				err = downgradingServer.ServeTLS(s.listener.l, s.options.tlsCertPath, s.options.tlsKeyPath)
			} else {
				err = downgradingServer.Serve(s.listener.l)
			}
			errch <- err
		}()
	} else {
		// The gRPC server lives in its own go routine
		go func() {
			err = s.grpcServer.Serve(s.listener.l)
			errch <- err
		}()
	}

	return nil
}

func (l *Listener) Host() string {
	return l.host
}

func (l *Listener) Port() int {
	return l.port
}

func (l *Listener) Address() string {
	return fmt.Sprintf("%s:%d", l.host, l.port)
}

// registerGrpcServices registers all required gRPC services to the server s.
// This method should be called after the server is configured, and has all
// required configuration properties set.
func (s *Server) registerGrpcServices(metrics *metrics.PrincipalMetrics) error {
	authSrv, err := auth.NewServer(s.queues, s.authMethods, s.issuer)
	if err != nil {
		return fmt.Errorf("could not create new auth server: %w", err)
	}
	authapi.RegisterAuthenticationServer(s.grpcServer, authSrv)
	versionapi.RegisterVersionServer(s.grpcServer, version.NewServer(s.authenticate))

	opts := []eventstream.ServerOption{}
	opts = append(opts, eventstream.WithNotifyOnConnect(s.notifyOnConnect))
	opts = append(opts, eventstream.WithLogger(s.options.grpcEventLogger))

	eventstreamapi.RegisterEventStreamServer(s.grpcServer, eventstream.NewServer(s.queues, s.eventWriters, metrics, s.clusterMgr, opts...))
	// Proposal: register LogStream gRPC service for data-plane (use singleton instance)
	logstreamapi.RegisterLogStreamServiceServer(s.grpcServer, s.logStream)
	// Register TerminalStream gRPC service for web terminal sessions
	terminalstreamapi.RegisterTerminalStreamServiceServer(s.grpcServer, s.terminalStreamServer)
	return nil
}
