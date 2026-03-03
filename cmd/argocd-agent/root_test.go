package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

const (
	expectedVersionUsage = `  -o, --output string   Output format. One of: text, yaml, json (default "text")
  -s, --short           Print the version number only
`
	expectedAgentUsage = `      --agent-mode string                   Mode of operation (default "autonomous")
      --allowed-namespaces strings          List of additional namespaces the agent is allowed to manage applications in (used with applications in any namespace feature)
      --cache-refresh-interval duration     Interval to refresh cluster cache info in principal (default 10s)
      --create-namespace                    Create target namespace if it doesn't exist when syncing applications (used with destination-based-mapping)
      --creds string                        Credentials to use when connecting to server
      --destination-based-mapping           Enable destination-based mapping. When enabled, applications are synced to their original namespace and the agent watches all namespaces
      --enable-compression                  Use compression while sending data between Principal and Agent using gRPC
      --enable-resource-proxy               Enable resource proxy (default true)
      --enable-websocket                    Agent will rely on gRPC over WebSocket to stream events to the Principal
      --grpc-max-message-size int           Maximum gRPC message size in bytes for send and receive (default: 200MB) (default 209715200)
      --healthz-port int                    Port the health check server will listen on (default 8001)
      --heartbeat-interval duration         Interval for application-level heartbeats over the Subscribe stream (e.g., 30s). Set to 0 to disable. Useful to keep connections alive through service meshes like Istio.
      --insecure-plaintext                  INSECURE: Connect without TLS (use with Istio or similar service mesh)
      --insecure-tls                        INSECURE: Do not verify remote TLS certificate
      --keep-alive-ping-interval duration   Ping interval to keep connection alive with Principal
      --kubeconfig string                   Path to a kubeconfig file to use
      --kubecontext string                  Override the default kube context
      --log-format string                   The log format to use (one of: text, json) (default "text")
      --log-level strings                   The log level to use. Comma-separated list of components in the format [<component>=]level (default [info])
      --metrics-port int                    Port the metrics server will listen on (default 8181)
  -n, --namespace string                    Namespace to manage applications in (default "argocd")
      --otlp-address string                 Experimental: OpenTelemetry collector address for sending traces (e.g., localhost:4317)
      --otlp-insecure                       Experimental: Use insecure connection to OpenTelemetry collector endpoint
      --pprof-port int                      Port the pprof server will listen on
      --redis-addr string                   The redis host to connect to (default "argocd-redis:6379")
      --redis-creds-dir-path string         The redis directory with 'auth_username' file for Redis username (optional) and 'auth' for Redis password
      --redis-password string               The password to connect to redis with
      --redis-username string               The username to connect to redis with
      --root-ca-path string                 Path to a file containing root CA certificate for verifying remote TLS
      --root-ca-secret-name string          Name of the secret containing the root CA certificate (default "argocd-agent-ca")
      --server-address string               Address of the server to connect to
      --server-port int                     Port on the server to connect to (default 443)
      --tls-ciphersuites strings            Comma-separated list of TLS cipher suites to use. Use 'list' to show available cipher suites and exit
      --tls-client-cert string              Path to TLS client certificate
      --tls-client-key string               Path to TLS client key
      --tls-max-version string              Maximum TLS version to use (tls1.1, tls1.2, tls1.3)
      --tls-min-version string              Minimum TLS version to use (tls1.1, tls1.2, tls1.3)
      --tls-secret-name string              Name of the secret containing the TLS certificate (default "argocd-agent-client-tls")
`
	expectedPrincipalUsage = `      --allowed-namespaces strings                    List of namespaces the server is allowed to operate in
      --auth string                                   Authentication method and the corresponding configuration
      --client-cert-subject-match                     Whether a client cert's subject must match the agent name
      --destination-based-mapping                     Map applications to agents based on spec.destination.name instead of namespace
      --enable-resource-proxy                         Whether to enable the resource proxy (default true)
      --enable-self-cluster-registration              Allow agents with valid credentials to self-register on connection (requires --self-registration-client-cert-secret)
      --enable-websocket                              Principal will rely on gRPC over WebSocket to stream events to the Agent
      --grpc-max-message-size int                     Maximum gRPC message size in bytes for send and receive (default: 200MB) (default 209715200)
      --healthz-port int                              Port the health check server will listen on (default 8003)
      --insecure-jwt-generate                         INSECURE: Generate and use temporary JWT signing key
      --insecure-plaintext                            INSECURE: Run gRPC server without TLS (use with Istio or similar service mesh)
      --insecure-tls-generate                         INSECURE: Generate and use temporary TLS cert and key
      --jwt-key string                                Use JWT signing key from path
      --jwt-secret-name string                        Secret name of the JWT signing key (default "argocd-agent-jwt")
      --keepalive-min-interval duration               Drop agent connections that send keepalive pings more often than the specified interval
      --kubeconfig string                             Path to a kubeconfig file to use
      --kubecontext string                            Override the default kube context
      --listen-host string                            Name of the host to listen on
      --listen-port int                               Port the gRPC server will listen on (default 8443)
      --log-format string                             The log format to use (one of: text, json) (default "text")
      --log-level strings                             The log level to use. Comma-separated list of components in the format [<component>=]level (default [info])
      --metrics-port int                              Port the metrics server will listen on (default 8000)
  -n, --namespace string                              The namespace the server will use for configuration. Set only when running out of cluster.
      --namespace-create-enable                       Whether to allow automatic namespace creation for autonomous agents
      --namespace-create-labels strings               Labels to apply to auto-created namespaces
      --namespace-create-pattern string               Only automatically create namespaces matching pattern
      --otlp-address string                           Experimental: OpenTelemetry collector address for sending traces (e.g., localhost:4317)
      --otlp-insecure                                 Experimental: Use insecure connection to OpenTelemetry collector endpoint
      --pprof-port int                                Port the pprof server will listen on
      --redis-compression-type string                 Compression algorithm required by Redis. (possible values: gzip, none. Default value: gzip) (default "gzip")
      --redis-creds-dir-path string                   The redis directory with 'auth' file for Redis password
      --redis-password string                         The password to connect to redis with
      --redis-server-address string                   Redis server hostname and port (e.g. argocd-redis:6379). (default "argocd-redis:6379")
      --require-client-certs                          Whether to require agents to present a client certificate
      --resource-proxy-address string                 Resource proxy address on principal side (default "argocd-agent-resource-proxy:9090")
      --resource-proxy-ca-path string                 Path to a file containing the resource proxy's TLS CA data
      --resource-proxy-ca-secret-name string          Secret name of the resource proxy's CA certificate (default "argocd-agent-ca")
      --resource-proxy-cert-path string               Path to a file containing the resource proxy's TLS certificate
      --resource-proxy-key-path string                Path to a file containing the resource proxy's TLS private key
      --resource-proxy-secret-name string             Secret name of the resource proxy (default "argocd-agent-resource-proxy-tls")
      --root-ca-path string                           Path to a file containing the root CA certificate for verifying client certs of agents
      --self-registration-client-cert-secret string   TLS secret containing shared client cert for self-registered cluster secrets (must have tls.crt, tls.key, ca.crt)
      --tls-ca-secret-name string                     Secret name of TLS CA certificate (default "argocd-agent-ca")
      --tls-cert string                               Use TLS certificate from path
      --tls-ciphersuites strings                      Comma-separated list of TLS cipher suites to use. Use 'list' to show available cipher suites and exit
      --tls-key string                                Use TLS private key from path
      --tls-max-version string                        Maximum TLS version to accept (tls1.1, tls1.2, tls1.3)
      --tls-min-version string                        Minimum TLS version to accept (tls1.1, tls1.2, tls1.3). Default: tls1.3 (default "tls1.3")
      --tls-secret-name string                        Secret name of TLS certificate and key (default "argocd-agent-principal-tls")
`
)

func TestNewRootCommand(t *testing.T) {
	cobra.EnableCommandSorting = true
	for _, command := range NewRootCommand().Commands() {
		switch command.Name() {
		case "version":
			assert.Len(t, command.Commands(), 0)
			assert.Equal(t, expectedVersionUsage, command.Flags().FlagUsages())
		case "agent":
			assert.Len(t, command.Commands(), 0)
			assert.Equal(t, expectedAgentUsage, command.Flags().FlagUsages())
		case "principal":
			assert.Len(t, command.Commands(), 0)
			assert.Equal(t, expectedPrincipalUsage, command.Flags().FlagUsages())
		default:
			assert.Fail(t, "Unknown subcommand "+command.Name())
		}
	}
}
