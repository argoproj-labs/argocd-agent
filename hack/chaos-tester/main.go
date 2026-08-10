package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"time"

	toxiproxyClient "github.com/Shopify/toxiproxy/v2/client"
	"k8s.io/apimachinery/pkg/util/wait"
)

// See 'ACTION-REQUIRED' comments for items you can configure before running the test.

func main() {

	// ACTION-REQUIRED: Uncomment the function for the specific behaviour you want to test

	// A) Periodically start/stop principal/agent/managed-agent via goreman
	// enableDisableAgentProcesses()

	// B) Periodically break (and block) the connection between agent and principlal, via toxiproxy
	// - Requires toxiproxy to be running via podman/docker, see README.md
	// breakNetworkConnectionsViaToxiproxy()

}

// enableDisableAgentProcesses periodically starts/stops the principal/agent processes via goreman.
func enableDisableAgentProcesses() {

	// ACTION-REQUIRED: Comment/uncomment the process to periodically restart/
	processName := "principal"
	// processName := "agent-managed"
	// processName := "agent-autonomous"

	for {

		// Generate a random duration between X and Y seconds for the proxy to stay enabled
		// ACTION-REQUIRED: Configurable interval between stop
		enabledDuration := time.Duration(rand.Intn(20)+20) * time.Second
		printf("Process enabled, waiting %v before disabling...\n", enabledDuration)
		time.Sleep(enabledDuration)

		if err := stopProcess(processName); err != nil {
			fmt.Println("Error: Unable to stop process")
			os.Exit(1)
		}

		// Disable the proxy to simulate a network failure

		// Generate a random duration between X and Y seconds for the proxy to stay disabled
		// ACTION-REQUIRED: Configurable interval for how long to keep the process disabled.
		disabledDuration := time.Duration(rand.Intn(5)+5) * time.Second
		printf("Process disabled, waiting %v before re-enabling...\n", disabledDuration)
		time.Sleep(disabledDuration)

		// Re-enable the proxy to restore connectivity
		println("Process enabled")
		if err := startProcess(processName); err != nil {
			fmt.Println("Error: Unable to start process")
			os.Exit(1)
		}

	}

}

// breakNetworkConnectionsViaToxiproxy periodically breaks (and block) the connection between agent and principlal, via toxiproxy.
// - Requires toxiproxy to be running, see README.md
func breakNetworkConnectionsViaToxiproxy() {

	toxiproxyServerAddress := "127.0.0.1:8474"
	proxyListenAddress := "127.0.0.1:8475"
	upstreamAddress := "127.0.0.1:8443"

	println("Waiting for toxiproxy server")

	// Wait for the Toxiproxy server to be ready
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		conn, err := net.Dial("tcp", "localhost:8474")
		if err == nil {
			conn.Close()
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		printf("Toxiproxy server not ready: %v\n", err)
		return
	}

	println("Creating proxy")
	client := toxiproxyClient.NewClient(toxiproxyServerAddress)
	proxy, err := client.CreateProxy("test", proxyListenAddress, upstreamAddress)
	if err != nil {
		println("Error:", err)
		return
	}

	println("Waiting")

	randomEnableDisable := true

	for {

		if randomEnableDisable {
			// Generate a random duration between X and Y seconds for the proxy to stay enabled
			// ACTION-REQUIRED: configurable time for how long connection should be allowed
			enabledDuration := time.Duration(rand.Intn(10)+5) * time.Second
			printf("Proxy enabled, waiting %v before disabling...\n", enabledDuration)
			time.Sleep(enabledDuration)

			// Disable the proxy to simulate a network failure
			if err := proxy.Disable(); err != nil {
				printf("Error disabling proxy: %v\n", err)
				return
			}
			println("Proxy disabled")

			// Generate a random duration between X and Y seconds for the proxy to stay disabled
			// ACTION-REQUIRED: configurable time for how long connection should be disabled
			disabledDuration := time.Duration(rand.Intn(5)+5) * time.Second
			printf("Proxy disabled, waiting %v before re-enabling...\n", disabledDuration)
			time.Sleep(disabledDuration)

			// Re-enable the proxy to restore connectivity
			if err := proxy.Enable(); err != nil {
				printf("Error enabling proxy: %v\n", err)
				return
			}
			println("Proxy enabled")
		} else {
			time.Sleep(1 * time.Second)
		}
	}
}

// stopProcess stops the named process managed by goreman
func stopProcess(processName string) error {
	cmd := exec.Command("goreman", "run", "stop", processName)
	return cmd.Run()
}

// startProcess starts the named process managed by goreman
func startProcess(processName string) error {
	cmd := exec.Command("goreman", "run", "start", processName)
	return cmd.Run()
}

func timestamp() string {
	return time.Now().Format(time.ANSIC) + "> "
}

func println(str ...any) {
	fmt.Printf("%s", timestamp())
	fmt.Println(str...)
}

func printf(format string, str ...any) {
	fmt.Printf("%s", timestamp())
	fmt.Printf(format, str...)
}
