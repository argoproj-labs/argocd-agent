# Running the end-to-end tests locally

## Setup

The e2e test scripts require [vcluster](https://github.com/loft-sh/vcluster) to be installed on your system. They also require an administrative connection to a host cluster.

**Warning** Don't run these scripts against a cluster that you care about, there is no guarantee they won't break the cluster in some way.

The scripts use `vcluster` to create three virtual clusters on the host cluster:

* vcluster-control-plane - For hosting the control plane and principal
* vcluster-agent-managed - A cluster with agent in managed mode
* vcluster-agent-autonomous - A cluster with agent in autonomous mode

The scripts will install Argo CD to each of those vclusters, in varying degrees of completeness.

Both the vcluster and Argo CD installations require that LoadBalancer functionality is available on the host cluster.

## Running the tests

### Connection Modes

E2E tests support two connection modes for Redis access:

1. **LoadBalancer Mode (Default - for CI/Linux with MetalLB):**
   - Uses LoadBalancer IPs directly
   - No port-forwards needed
   - Default when `E2E_USE_PORT_FORWARD` is not set
   ```bash
   make start-e2e
   make test-e2e
   ```

2. **Port-Forward Mode (for local development on any OS):**
   - Uses `localhost` addresses via `kubectl port-forward`
   - Works on macOS, Linux, Windows
   - Enable by setting `E2E_USE_PORT_FORWARD=true`
   ```bash
   E2E_USE_PORT_FORWARD=true make start-e2e
   E2E_USE_PORT_FORWARD=true make test-e2e
   ```

**When to use each mode:**
- **CI/GitHub Actions**: Use default LoadBalancer mode
- **Local development without MetalLB**: Use port-forward mode
- **Local development with MetalLB**: Either mode works

### Step 1: Setup the test environment

From the repository root:

```shell
make setup-e2e
```

**Note:** Redis TLS is **required** and configured automatically. See the [Redis TLS](#redis-tls) section below for details.

### Step 1b: Reverse Tunnel Setup (Remote Clusters Only)

**Only required if your vclusters are on a remote cluster (e.g., AWS, GCP) that cannot directly reach your local machine.**

If you're using a local cluster (kind, minikube, Docker Desktop), **skip this step**.

For remote clusters, set up the reverse tunnel to allow Argo CD (running remotely) to connect to your local principal:

In **Terminal 1**:

```shell
./hack/dev-env/reverse-tunnel/setup.sh
```

This will:
- Deploy a rathole proxy in your remote vcluster
- Configure Argo CD to route traffic through the tunnel
- Start a local rathole client (leave it running)
- Wait for "Control channel established" message

**Keep Terminal 1 running with the rathole tunnel.**

See [hack/dev-env/reverse-tunnel/README.md](../../hack/dev-env/reverse-tunnel/README.md) for more details.

### Step 2: Start the principal and agents

In **Terminal 2** (or Terminal 1 if not using reverse tunnel), start the E2E environment (principal, agents, and port-forwards):

```shell
make start-e2e
```

**Important:** Keep this terminal running! The tests require:
- Principal and agent processes
- Redis connections (via port-forward OR LoadBalancer, depending on mode)

These are managed by `goreman` and must remain running for tests to work.

**Dual-Mode Setup:**

By default, the scripts use **LoadBalancer mode** (direct LoadBalancer IPs, for CI/Linux with MetalLB). To use **port-forward mode** instead (local development on any OS), set `E2E_USE_PORT_FORWARD=true`:

```shell
# Port-forward mode (local development, any OS)
E2E_USE_PORT_FORWARD=true make start-e2e

# LoadBalancer mode (default - CI/Linux with MetalLB)
make start-e2e
```

**Port-forward mode:**
- Uses `localhost:6380`, `6381`, `6382` for Redis
- Requires port-forwards (started automatically via `Procfile.e2e.local`)
- Works on any OS (macOS, Linux, Windows)

**LoadBalancer mode (default):**
- Uses LoadBalancer IPs directly for Redis
- Requires MetalLB or cloud LoadBalancer support
- No port-forwards needed (uses `Procfile.e2e`)

**Note:** If using the reverse tunnel (remote clusters), Argo CD connects to the principal via the tunnel, not port-forwards.

### Step 3: Run the tests

In **Terminal 3** (or Terminal 2 if not using reverse tunnel), run the E2E tests:

```shell
# Port-forward mode (if you started with E2E_USE_PORT_FORWARD=true)
E2E_USE_PORT_FORWARD=true make test-e2e

# LoadBalancer mode (default - if you started without E2E_USE_PORT_FORWARD)
make test-e2e
```

**Important:** Use the same `E2E_USE_PORT_FORWARD` setting for both `start-e2e` and `test-e2e`.

The tests will use the appropriate connection method based on the `E2E_USE_PORT_FORWARD` setting:
- **Port-forward mode** (`E2E_USE_PORT_FORWARD=true`): Connects via port-forwards to `localhost`
- **LoadBalancer mode** (default): Connects directly to LoadBalancer IPs

### Redis TLS

Redis TLS is **mandatory** for E2E tests and is automatically configured by `make setup-e2e`. This includes:
- Generating TLS certificates for all three vclusters
- Configuring Redis to use TLS-only mode (port 6379)
- Configuring Argo CD components to connect with TLS

If you need to manually reconfigure Redis TLS (e.g., after certificate expiration or corruption):

```shell
# Regenerate certificates
./hack/dev-env/gen-redis-tls-certs.sh

# Reconfigure Redis for each vcluster
./hack/dev-env/configure-redis-tls.sh vcluster-control-plane
./hack/dev-env/configure-redis-tls.sh vcluster-agent-managed
./hack/dev-env/configure-redis-tls.sh vcluster-agent-autonomous

# Reconfigure Argo CD components for each vcluster
./hack/dev-env/configure-argocd-redis-for-tls.sh vcluster-control-plane
./hack/dev-env/configure-argocd-redis-for-tls.sh vcluster-agent-managed
./hack/dev-env/configure-argocd-redis-for-tls.sh vcluster-agent-autonomous
```

# Writing new end-to-end tests

There is some helper code in the `fixture` subdirectory. The tests use the [stretchr/testify](https://github.com/stretchr/testify) test framework. New tests should be created as part of a test suite, either an existing one or, preferably, as part of a new one.

A new test suite should embed the `fixture.BaseSuite` struct, which will provide some automatic setup and teardown functionality for the suite.

```go
type MyTestSuite struct {
	fixture.BaseSuite
}
```

This will configure your suite with a `context.Context` as well as three `kubernetes clients`, one for the principal vcluster, one for the managed-agent vcluster, and one for the autonomous-agent vcluster. This is implemented in the `SetupSuite()` method which has been defined on the BaseSuite.  If your suite does not need it's own `SetupSuite()` method, the one from BaseSuite will be used automatically. If you do need to specify a `SetupSuite()` method for your own suite, be sure to call the BaseSuite's method as the first thing.

```go
func (suite *MyTestSuite) SetupSuite() {
	suite.BaseSuite.SetupSuite()
    ...
}
```

The BaseSuite also defines the `SetupTest()` and `TearDownTest()` methods to perform cleanup. If your suite does not need it's own version of these methods, the ones from BaseSuite will be used automatically. If you do need to specify one of these methods for your own suite, be sure to call the BaseSuite's method as the first thing.

```go
func (suite *MyTestSuite) TearDownTest() {
	suite.BaseSuite.TearDownTest()
    ...
}
```

The kubernetes client is a wrapper around `client-go/dynamic`. It is able to access the ArgoCD types as well as the types from `k8s.io/api/core/v1`, `k8s.io/api/apps/v1`, and `k8s.io/api/rbac/v1`. If you need support for additional types, you can add then to the scheme used in the `NewKubeClient` function in `fixture/kubeclient.go`
