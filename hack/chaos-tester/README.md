# Chaos Engineering Test Utility

This is a utility that can be used to perform chaos engineering on Argo CD agent.

There are two chaos engineering scenarios supported:
- A) Use toxiproxy to intermittently break the connection between principal and agent, while E2E tests are running
- B) Intermittently restart the agent/principal via goreman, while E2E tests are running 

When running the chaos utility, note the ACTION-REQUIRED comments which should be used to configure the tool. In most cases, the defaults are fine.


## A) Use toxiproxy to intermittently break the connection between principal and agent, while E2E tests are running

```
# Initial setup
make setup-e2e

# Run the following in different windows/tabs, for convenience...

# Window A: start toxiproxy
podman run --rm --net=host -it ghcr.io/shopify/toxiproxy
# You should see 'Starting Toxiproxy' message

# Window B: start chaos utility, which will break connection between principal/agent via toxiproxy
cd hack/chaos-tester
# In 'hack/chaos-tester/main.go', ensure the toxiproxy connection code is uncommented.
# - Note the other ACTION-REQUIRED configurables within the code.
go run .
# You should see 'Waiting for toxiproxy server'/'Creating proxy'


# Window C: start agent itself via goreman
ARGOCD_AGENT_REMOTE_PORT=8475 make start-e2e
# Verify you see messages from agent/managed indicating they are occasionally losing connection to principal
# - For example, "Auth failure: rpc error: code = Unavailable desc = connection error: desc = \"transport: Error while dialing: dial tcp 127.0.0.1:8475: connect: connection refused\" (retrying in 1s)"

# Window D:
# This will run the E2E tests over and over until they fail. 
hack/chaos-tester/until-fail.sh make test-e2e

# For any tests that fail, you can further investigate them by running them over and over until fail.
# e.g.
"hack/chaos-tester/until-fail.sh" go test -count=1 -v -v -timeout 60m -run  TestSyncTestSuite/Test_SyncManaged github.com/argoproj-labs/argocd-agent/test/e2e
```

#### Notes:
- Not all Argo CD functionality supports retriable messages:
    - For example, for resource proxy/redis proxy/resource actions, it is acceptable to fail after a certain period of time if the peer is not available.


## B) Intermittently restart the agent/principal via goreman, while E2E tests are running
   

```
# Initial setup
make setup-e2e

# Run the following in different windows/tabs, for convenience...

# Window A: start chaos utility, which will start/stop processes via goreman
cd hack/chaos-tester
# In 'hack/chaos-tester/main.go', ensure the process restart code is uncommented.
# - Note the other ACTION-REQUIRED configurables within the code.
go run .

# Window B: start agent itself via goreman
make start-e2e
# Verify that you see principal/agent processes occasionally being terminated via chaos-tester utility
# For example: 
# 02:07:45        principal | signal: interrupt
# 02:07:45        principal | Terminating principal
# (...)
# 02:07:53        principal | Starting principal on port 5000

# Window C:
# This will run the E2E tests over and over until they fail. 
hack/chaos-tester/until-fail.sh make test-e2e


# For any tests that fail, you can further investigate them by running them over and over until fail.
# e.g.
 "hack/chaos-tester/until-fail.sh" go test -count=1 -v -v -timeout 60m -run  TestSyncTestSuite/Test_SyncManaged github.com/argoproj-labs/argocd-agent/test/e2e
```

#### Note: 
- Some existing E2E tests in the codebase will themselves restart the principal/agent-managed/agent-autonomous process via goreman. These tests are not compatible with testing via scenario B, and can be skipped.
    - You can identify these tests by searching for E2E tests that call `fixture.StartProcess()` et al




