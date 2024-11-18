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

To setup the test environment on the cluster, execute the following command from the repository root:

```shell
make setup-e2e2
```

To run the principal and agents, execute the following command from the repository root:

```shell
make start-e2e2
```

To run the tests, execute the following command from the repository root in a separate terminal instance:

```shell
make test-e2e2
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
