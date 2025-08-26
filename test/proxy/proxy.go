package proxy

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"testing"
)

// StartHTTP2DowngradingProxy starts a proxy that downgrades HTTP/2.0 to HTTP/1.1
// This simulates a real HTTP/1.1-only proxy environment.
func StartHTTP2DowngradingProxy(t *testing.T, addr string, target string, agentClientCert, principalServerCert tls.Certificate) *http.Server {

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		TLSConfig: &tls.Config{
			// Use principal's server certificate for incoming connections
			// This makes the agent think it's connecting to the real principal
			Certificates: []tls.Certificate{principalServerCert},
			NextProtos:   []string{"h2", "http/1.1"},
		},
		Handler: downgradeToHTTP1Handler(target, agentClientCert),
		Addr:    lis.Addr().String(),
	}

	//nolint:errcheck
	go func() {
		tlsListener := tls.NewListener(lis, server.TLSConfig)
		if err := server.Serve(tlsListener); err != nil {
			log.Printf("Proxy server error: %v", err)
		}
	}()

	return server
}

func downgradeToHTTP1Handler(target string, agentCert tls.Certificate) http.Handler {
	printHeaders := func(header http.Header) {
		for name, values := range header {
			for _, value := range values {
				fmt.Printf("%s: %s\n", name, value)
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Printf("Intercepting a %s request to %s\n", req.Method, req.URL)

		// Check if this is a gRPC request
		isGRPC := req.Header.Get("Content-Type") == "application/grpc"
		fmt.Printf("Is gRPC request: %t, Protocol: %s\n", isGRPC, req.Proto)

		fmt.Println("Request Headers:")
		printHeaders(req.Header)

		// Downgrade all requests to HTTP/1.1
		if req.Proto == "HTTP/2.0" {
			fmt.Println("Downgrading request from HTTP/2.0 to HTTP/1.1")
			req.ProtoMajor, req.ProtoMinor, req.Proto = 1, 1, "HTTP/1.1"
		}

		// For WebSocket upgrade requests, allow them through
		if req.Header.Get("Upgrade") == "websocket" {
			fmt.Println("Allowing WebSocket upgrade request")
		} else if isGRPC && req.Proto == "HTTP/1.1" {
			fmt.Println("Forwarding downgraded gRPC request over HTTP/1.1")
		}

		// Use reverse proxy with HTTP/1.1 enforcement
		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Host = target
				if req.URL.Scheme == "" {
					req.URL.Scheme = "https"
				}

				// Ensure the request is downgraded to HTTP/1.1
				req.ProtoMajor, req.ProtoMinor, req.Proto = 1, 1, "HTTP/1.1"

				fmt.Printf("Proxying %s request to %s (downgraded to %s)\n", req.Method, req.URL, req.Proto)
			},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
					// Use agent's certificate for outgoing connections
					// This makes the principal think it's connecting to the real agent
					Certificates: []tls.Certificate{agentCert},
				},
				ForceAttemptHTTP2: false,
			},
			ModifyResponse: func(res *http.Response) error {
				if res != nil {
					// Ensure response is also HTTP/1.1
					res.ProtoMajor, res.ProtoMinor, res.Proto = 1, 1, "HTTP/1.1"
					fmt.Printf("Response: %s (proto: %s)\n", res.Status, res.Proto)
				}
				return nil
			},
		}

		proxy.ServeHTTP(w, req)
	})
}
