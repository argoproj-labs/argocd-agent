package fixture

import (
	"net"
	"net/http"
	"os"
	"time"

	toxiproxy "github.com/Shopify/toxiproxy/v2"
	toxiproxyClient "github.com/Shopify/toxiproxy/v2/client"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
)

// SetupToxiproxy configures and starts a Toxiproxy server on the specified host and port. It also sets the environment
// variable for the remote port and provides a cleanup function to stop the server and remove the variable.
func SetupToxiproxy(t require.TestingT, agentName string, proxyAddress string) (*toxiproxyClient.Proxy, func(), error) {

	// Start the Toxiproxy server
	proxyServer, err := startToxiproxyServer("localhost", "8474")
	if err != nil {
		return nil, nil, err
	}

	// Wait for the Toxiproxy server to be ready
	require.Eventually(t, func() bool {
		conn, err := net.Dial("tcp", "localhost:8474")
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 10*time.Second, 500*time.Millisecond)

	// Create a proxy
	client := toxiproxyClient.NewClient("127.0.0.1:8474")
	proxy, err := client.CreateProxy("test", proxyAddress, "127.0.0.1:8443")
	if err != nil {
		return nil, nil, err
	}

	// Write the remote port variable to the config file
	envVar := `ARGOCD_AGENT_REMOTE_PORT=8475`
	err = os.WriteFile(EnvVariablesFromE2EFile, []byte(envVar+"\n"), 0644)
	if err != nil {
		return nil, nil, err
	}

	// Cleanup function
	cleanup := func() {
		if err := os.Remove(EnvVariablesFromE2EFile); err != nil {
			t.Errorf("failed to remove env file: %v", err)
		}

		if err = proxy.Delete(); err != nil {
			t.Errorf("failed to delete proxy: %v", err)
		}

		if err = proxyServer.Close(); err != nil {
			t.Errorf("failed to close proxy server: %v", err)
		}

		// Restart the agent process
		RestartAgent(t, agentName)

		// Give some time for the agent to be ready
		WaitForAgent(t, agentName)
	}

	return proxy, cleanup, nil
}

func startToxiproxyServer(host, port string) (*http.Server, error) {
	server := toxiproxy.NewServer(toxiproxy.NewMetricsContainer(nil), zerolog.New(os.Stdout))
	addr := net.JoinHostPort(host, port)

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	proxyServer := &http.Server{
		Addr:    addr,
		Handler: server.Routes(),
	}

	//nolint:errcheck
	go proxyServer.Serve(l)

	return proxyServer, nil
}

func RestartAgent(t require.TestingT, agentName string) {
	err := StopProcess(agentName)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return !IsProcessRunning(agentName)
	}, 30*time.Second, 1*time.Second)

	err = StartProcess(agentName)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return IsProcessRunning(agentName)
	}, 30*time.Second, 1*time.Second)
}

func WaitForAgent(t require.TestingT, agentName string) {
	healthzAddr := "http://localhost:8002/healthz"
	if agentName == "agent-managed" {
		healthzAddr = "http://localhost:8001/healthz"
	}

	require.Eventually(t, func() bool {
		resp, err := http.Get(healthzAddr)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, 1*time.Second)
}
