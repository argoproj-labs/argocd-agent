package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
)

// Start a reverse proxy that downgrades the incoming HTTP/2 requests to HTTP/1.1
func StartHTTP2DowngradingProxy(t *testing.T, addr string, target string) *http.Server {
	tempDir := t.TempDir()
	basePath := path.Join(tempDir, "certs")
	testcerts.WriteSelfSignedCert(t, "rsa", basePath, x509.Certificate{SerialNumber: big.NewInt(1)})

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	server := &http.Server{
		TLSConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		Handler: downgradeToHTTP1Handler(target),
		Addr:    lis.Addr().String(),
	}

	//nolint:errcheck
	go server.ServeTLS(lis, basePath+".crt", basePath+".key")

	return server
}

func downgradeToHTTP1Handler(target string) http.Handler {
	printHeaders := func(header http.Header) {
		for name, values := range header {
			for _, value := range values {
				fmt.Printf("%s: %s\n", name, value)
			}
		}
	}
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			fmt.Println("Intercepting a request")
			req.URL.Host = target
			if req.URL.Scheme == "" {
				req.URL.Scheme = "https"
			}

			// Request is downgraded to HTTP/1.1
			req.ProtoMajor, req.ProtoMinor, req.Proto = 1, 1, "HTTP/1.1"

			// Log headers for debugging
			fmt.Println("Request Headers:")
			printHeaders(req.Header)
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			ForceAttemptHTTP2: false,
		},
		ModifyResponse: func(res *http.Response) error {
			// Ensure the response is sent as HTTP/1.1
			res.ProtoMajor = 1
			res.ProtoMinor = 1
			res.Proto = "HTTP/1.1"

			// Log response headers for debugging
			fmt.Println("Response Headers:")
			printHeaders(res.Header)

			return nil
		},
	}
}
