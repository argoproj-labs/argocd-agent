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
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/versionapi"
	"github.com/argoproj-labs/argocd-agent/principal/apis/auth"
	"github.com/argoproj-labs/argocd-agent/principal/apis/eventstream"
	"github.com/argoproj-labs/argocd-agent/principal/apis/version"
)

const listenerRetries = 5

var listenerBackoff = wait.Backoff{
	Steps:    listenerRetries,
	Duration: 2 * time.Second,
	Factor:   1.0,
	Jitter:   0.1,
}

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
			log().Debugf("Starting TCP listener on %s", bind)
		}
		// Even though we load TLS configuration here, we will not use create
		// a TLS listener. TLS will be setup using the appropriate grpc-go API
		// functions.
		s.tlsConfig, lerr = s.loadTLSConfig()
		if lerr != nil {
			return false, lerr
		}
		c, lerr = net.Listen("tcp", bind)
		if lerr != nil {
			log().WithError(err).Debugf("Retrying to start TCP listener on %s (retry %d/%d)", bind, try, listenerRetries)
			try += 1
			return false, lerr
		}
		return true, nil
	})
	// The following condition will probably never be true
	if err != nil {
		return err
	}

	log().Infof("Now listening on %s", c.Addr().String())
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

func (s *Server) serveGRPC(ctx context.Context, errch chan error) error {
	err := s.Listen(ctx, listenerBackoff)
	if err != nil {
		return fmt.Errorf("could not start listener: %w", err)
	}

	s.grpcServer = grpc.NewServer(
		grpc.ChainStreamInterceptor(
			streamRequestLogger(),
			// logging.StreamServerInterceptor(InterceptorLogger(logrus.New()),
			// 	logging.WithLogOnEvents(logging.StartCall, logging.FinishCall),
			// ),
			s.streamAuthInterceptor,
			// grpc_auth.StreamServerInterceptor(func(ctx context.Context) (context.Context, error) {
			// 	return s.authenticate(ctx)
			// }),
		),
		grpc.ChainUnaryInterceptor(
			unaryRequestLogger(),
			// logging.UnaryServerInterceptor(InterceptorLogger(logrus.New()),
			// 	logging.WithLogOnEvents(logging.StartCall, logging.FinishCall),
			// ),
			s.unaryAuthInterceptor,
		),
		grpc.Creds(credentials.NewTLS(s.tlsConfig)),
	)
	authSrv, err := auth.NewServer(s.queues, s.authMethods, s.issuer)
	if err != nil {
		return fmt.Errorf("could not create new auth server: %w", err)
	}
	authapi.RegisterAuthenticationServer(s.grpcServer, authSrv)
	versionapi.RegisterVersionServer(s.grpcServer, version.NewServer(s.authenticate))
	eventstreamapi.RegisterEventStreamServer(s.grpcServer, eventstream.NewServer(s.queues))

	// The gRPC server lives in its own go routine
	go func() {
		err = s.grpcServer.Serve(s.listener.l)
		errch <- err
	}()

	return nil
}

// func (s *Server) ServeHTTP(ctx context.Context, errch chan error) error {
// 	err := s.Listen(ctx, listenerBackoff)
// 	if err != nil {
// 		return fmt.Errorf("could not start listener: %w", err)
// 	}
// 	mux := http.NewServeMux()
// 	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
// 		w.WriteHeader(200)
// 		w.Write([]byte("hello workd"))
// 	})
// 	s.server = &http.Server{
// 		BaseContext: func(l net.Listener) context.Context {
// 			return s.listener.ctx
// 		},
// 		TLSConfig: s.tlsConfig,
// 		ErrorLog:  golog.New(log.New().WriterLevel(log.WarnLevel), "", 0),
// 		Handler:   mux,
// 	}
// 	go func() {
// 		err = s.server.Serve(s.listener.l)
// 		errch <- err
// 	}()
// 	return nil
// }

func (l *Listener) Host() string {
	return l.host
}

func (l *Listener) Port() int {
	return l.port
}

func (l *Listener) Address() string {
	return fmt.Sprintf("%s:%d", l.host, l.port)
}
