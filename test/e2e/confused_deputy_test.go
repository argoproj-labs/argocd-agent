// Copyright 2026 The argocd-agent Authors
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

package e2e

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/gitops-engine/pkg/health"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
)

// principalGRPCAddress and principalGRPCPort are where the live e2e principal
// listens for agent connections (see hack/dev-env/start-principal.sh /
// ARGOCD_PRINCIPAL_LISTEN_PORT, and test/e2e/fixture/toxyproxy.go which
// targets the same address).
const (
	principalGRPCAddress = "127.0.0.1"
	principalGRPCPort    = 8443
)

// unmappedAgentName is a destination that no connected agent answers for. It
// is used as the owner of victim Applications in tests where a *real* agent
// reacting to the Application would otherwise race the assertion. The
// principal still stores and routes on the destination name, so the ownership
// check behaves exactly as it does for a live agent, but nothing else touches
// the object.
const unmappedAgentName = "agent-not-connected"

// These tests cover the confused-deputy class of issue in the principal's
// inbound event path: a managed agent addressing an Application by
// (namespace, name) that belongs to a *different* agent.
//
// TestForgedAgentCanUpdateItsOwnApplication is the positive control for the
// whole file. It proves the forged-agent harness genuinely works end to end -
// connection, authentication and event delivery - and that the ownership
// check is not simply denying everything from an unprovisioned agent.
//
// Without this, the Never-style assertions in the tests below could pass
// vacuously (e.g. if the attacker never managed to connect at all).
func (suite *DestinationMappingTestSuite) TestForgedAgentCanUpdateItsOwnApplication() {
	requires := suite.Require()
	t := suite.T()

	agentName := "confused-deputy-self"
	appName := "confused-deputy-self-owned"
	appKey := ktypes.NamespacedName{Namespace: destMapAgentNamespace, Name: appName}

	t.Log("Create an application whose destination maps it to the forged agent itself")
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, false)
	app.Spec.Destination.Name = agentName
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	defer func() {
		requires.NoError(fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app))
	}()

	attacker, err := dialForgedAgent(suite.Ctx, suite.PrincipalClient, agentName)
	requires.NoError(err)
	defer attacker.Disconnect()

	marker := fmt.Sprintf("self-owned-%d", time.Now().UnixNano())
	update := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: destMapAgentNamespace},
		Status: v1alpha1.ApplicationStatus{
			Health: v1alpha1.AppHealthStatus{Status: health.HealthStatusDegraded},
			Conditions: []v1alpha1.ApplicationCondition{
				{Type: "SelfOwnedProbe", Message: marker},
			},
		},
	}

	t.Log("An agent updating the status of an application that IS mapped to it must still be accepted")
	stop := startForgedEventSender(suite.Ctx, t, attacker, 1*time.Second, func() *cloudevents.Event {
		return event.NewEventSource(agentName).ApplicationEvent(event.StatusUpdate, update)
	})
	defer stop()

	requires.Eventually(func() bool {
		found, err := appHasCondition(suite, appKey, "SelfOwnedProbe", marker)
		if err != nil {
			t.Logf("could not read the application, skipping this assertion: %v", err)
			return false
		}
		return found
	}, 30*time.Second, 500*time.Millisecond,
		"the ownership check must not reject an agent updating its own application")
}

// TestStatusUpdateConfusedDeputy verifies that the principal rejects a
// status-update event naming an Application that belongs to a different
// agent.
//
// Background (the issue this guards against): with
// --destination-based-mapping enabled, UpdateStatus() skips the
// `incoming.SetNamespace(namespace)` pin that otherwise confines an agent's
// writes to its own namespace, and processApplicationEvent()'s status-update
// case used to pass the agent-supplied (Namespace, Name) straight through. A
// single connected managed agent could therefore overwrite the status of any
// other agent's Application on the principal - flipping a broken app to
// Healthy, or a healthy app to Degraded, in the hub's UI and API.
//
// This test simulates a compromised managed agent: an attacker who can mint a
// client certificate signed by the CA the principal already trusts (e.g. by
// compromising an agent host) and speaks the wire protocol directly rather
// than running the honest agent binary. Being able to *connect* that way is
// by design and is not an issue; being able to write to someone
// else's Application is.
func (suite *DestinationMappingTestSuite) TestStatusUpdateConfusedDeputy() {
	requires := suite.Require()
	t := suite.T()

	appName := "confused-deputy-victim"
	victimKey := ktypes.NamespacedName{Namespace: destMapAgentNamespace, Name: appName}

	t.Log("Create the victim application, legitimately owned by the real, honest agent-managed agent")
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, true)
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	defer func() {
		requires.NoError(fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app))
	}()

	t.Log("Wait for the real agent to pick up and start managing the victim application")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, victimKey, agentApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "victim application should be created on the real agent")

	t.Log("Simulate a compromised SECOND managed agent by minting a client identity signed by the same CA the principal already trusts")
	attackerAgentName := "confused-deputy-attacker"
	attacker, err := dialForgedAgent(suite.Ctx, suite.PrincipalClient, attackerAgentName)
	requires.NoError(err, "a forged-but-CA-signed agent identity should still be able to authenticate to the principal - "+
		"that part is by design (any agent can connect), and is not itself the issue being tested")
	defer attacker.Disconnect()

	marker := fmt.Sprintf("forged-by-%s-%d", attackerAgentName, time.Now().UnixNano())
	t.Log("Attacker forges a status-update for the VICTIM's application, which it does not own")
	forged := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: destMapAgentNamespace,
		},
		Status: v1alpha1.ApplicationStatus{
			Health: v1alpha1.AppHealthStatus{
				Status: health.HealthStatusDegraded,
			},
			Conditions: []v1alpha1.ApplicationCondition{
				{
					Type:    "ConfusedDeputyPoC",
					Message: marker,
				},
			},
		},
	}

	t.Log("the principal must never apply the attacker's forged status to the victim's application")
	// The forged event is re-sent continuously rather than once, so the
	// assertion covers a sustained attack and does not depend on catching a
	// narrow window. The principal accepts the event onto its receive queue
	// (so Push itself succeeds) and rejects it during processing, logging an
	// EventNotAllowed - which is why we assert on the stored object rather
	// than on the Push result.
	stop := startForgedEventSender(suite.Ctx, t, attacker, 1*time.Second, func() *cloudevents.Event {
		return event.NewEventSource(attackerAgentName).ApplicationEvent(event.StatusUpdate, forged)
	})
	defer stop()

	requires.Never(func() bool {
		found, err := appHasCondition(suite, victimKey, "ConfusedDeputyPoC", marker)
		if err != nil {
			t.Logf("could not read the victim application: %v", err)
			return true
		}
		return found
	}, 10*time.Second, 500*time.Millisecond)
}

// TestOperationPropagationConfusedDeputy covers the second half of the same
// missing check: UpdateStatus() merges `existing.Operation =
// incoming.Operation`, so a forged status-update could also plant an
// Operation (a sync trigger) on another agent's Application.
//
// Two distinct properties are asserted here:
//
//  1. The forged Operation must no longer land on the principal's copy at all
//     - this is what the ownership check fixes.
//
//  2. It must never reach the victim agent's own cluster. This held even
//     before the fix, because UpdateStatus() marks its own write via
//     IgnoreChange() and principal/callbacks.go's updateAppCallback() checks
//     that ignore-list before reaching the logic that detects a new Operation
//     and forwards it as a dedicated SetOperation event. A legitimate
//     hub-triggered sync works because the Argo CD API server writes the
//     Application CR directly - a write that was never marked ignorable.
//
// Assertion 2 is kept as a safety-boundary regression guard: if it ever starts
// failing, the propagation logic in principal/callbacks.go has changed in a
// way that needs re-assessment, independently of the ownership check.
func (suite *DestinationMappingTestSuite) TestOperationPropagationConfusedDeputy() {
	requires := suite.Require()
	t := suite.T()

	appName := "confused-deputy-operation-victim"
	victimKey := ktypes.NamespacedName{Namespace: destMapAgentNamespace, Name: appName}

	t.Log("Create the victim application (no auto-sync), legitimately owned by the real, honest agent-managed agent")
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, false)
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	defer func() {
		requires.NoError(fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app))
	}()

	t.Log("Wait for the real agent to pick up and start managing the victim application")
	requires.Eventually(func() bool {
		agentApp := &v1alpha1.Application{}
		err := suite.ManagedAgentClient.Get(suite.Ctx, victimKey, agentApp, metav1.GetOptions{})
		return err == nil
	}, 60*time.Second, 1*time.Second, "victim application should be created on the real agent")

	t.Log("Simulate a compromised SECOND managed agent by minting a client identity signed by the same CA the principal already trusts")
	attackerAgentName := "confused-deputy-operation-attacker"
	attacker, err := dialForgedAgent(suite.Ctx, suite.PrincipalClient, attackerAgentName)
	requires.NoError(err, "a forged-but-CA-signed agent identity should still be able to authenticate to the principal - "+
		"that part is by design (any agent can connect), and is not itself the issue being tested")
	defer attacker.Disconnect()

	marker := fmt.Sprintf("forged-sync-by-%s-%d", attackerAgentName, time.Now().UnixNano())
	t.Log("Attacker forges a sync Operation for the VICTIM's application, which it does not own")
	forged := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: destMapAgentNamespace,
		},
		Operation: &v1alpha1.Operation{
			Sync: &v1alpha1.SyncOperation{},
			InitiatedBy: v1alpha1.OperationInitiator{
				Username: marker,
			},
		},
	}

	// The sender keeps running for both assertions below, so the propagation
	// boundary is also checked while the attack is ongoing rather than only
	// after it has stopped.
	stop := startForgedEventSender(suite.Ctx, t, attacker, 1*time.Second, func() *cloudevents.Event {
		return event.NewEventSource(attackerAgentName).ApplicationEvent(event.StatusUpdate, forged)
	})
	defer stop()

	t.Log("the forged operation must never land on the principal's copy of the victim's application")
	requires.Never(func() bool {
		principalApp := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, victimKey, principalApp, metav1.GetOptions{}); err != nil {
			t.Logf("could not read the victim application on the principal: %v", err)
			return true
		}
		return principalApp.Operation != nil && principalApp.Operation.InitiatedBy.Username == marker
	}, 20*time.Second, 500*time.Millisecond,
		"either the principal accepted an Operation on this Application from an agent that does not own it, "+
			"or the application could not be read so nothing was proven; the log above says which")

	t.Log("CONFIRMED BOUNDARY: the forged operation must never reach the real agent's own cluster or be executed there")
	requires.Never(func() bool {
		agentApp := &v1alpha1.Application{}
		if err := suite.ManagedAgentClient.Get(suite.Ctx, victimKey, agentApp, metav1.GetOptions{}); err != nil {
			t.Logf("could not read the victim application on the agent: %v", err)
			return true
		}
		if agentApp.Operation != nil && agentApp.Operation.InitiatedBy.Username == marker {
			t.Logf("UNEXPECTED: forged operation reached the real agent's local Operation field")
			return true
		}
		if agentApp.Status.OperationState != nil && agentApp.Status.OperationState.Operation.InitiatedBy.Username == marker {
			t.Logf("UNEXPECTED: forged operation was actually executed by the real agent's local controller")
			return true
		}
		return false
	}, 15*time.Second, 1*time.Second)
}

// TestDeleteConfusedDeputy covers the delete branch of the same missing
// ownership check.
func (suite *DestinationMappingTestSuite) TestDeleteConfusedDeputy() {
	requires := suite.Require()
	t := suite.T()

	appName := "confused-deputy-delete-victim"
	victimKey := ktypes.NamespacedName{Namespace: destMapAgentNamespace, Name: appName}
	const finalizer = "resources-finalizer.argocd.argoproj.io"

	t.Log("Create a victim application owned by an agent that is not connected, carrying a finalizer")
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, false)
	app.Spec.Destination.Name = unmappedAgentName
	app.Finalizers = []string{finalizer}
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)

	// The finalizer blocks real removal, so the object must be force-cleaned
	// at the end regardless of how the test exits.
	defer func() {
		stored := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, victimKey, stored, metav1.GetOptions{}); err == nil {
			_ = fixture.EnsureUpdate(suite.Ctx, suite.PrincipalClient, stored, func(obj fixture.KubeObject) {
				obj.(*v1alpha1.Application).Finalizers = nil
			})
		}
		requires.NoError(fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app))
	}()

	t.Log("Put the victim application into a deleting state; the finalizer keeps it alive")
	err = suite.PrincipalClient.Delete(suite.Ctx, app, metav1.DeleteOptions{})
	requires.NoError(err)

	requires.Eventually(func() bool {
		stored := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, victimKey, stored, metav1.GetOptions{}); err != nil {
			return false
		}
		return stored.DeletionTimestamp != nil && len(stored.Finalizers) > 0
	}, 30*time.Second, 500*time.Millisecond,
		"the victim application should be stuck in deletion with its finalizer intact")

	attackerAgentName := "confused-deputy-delete-attacker"
	attacker, err := dialForgedAgent(suite.Ctx, suite.PrincipalClient, attackerAgentName)
	requires.NoError(err)
	defer attacker.Disconnect()

	forged := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: destMapAgentNamespace},
	}

	t.Log("the attacker must not be able to strip the finalizer or complete the deletion")
	stop := startForgedEventSender(suite.Ctx, t, attacker, 1*time.Second, func() *cloudevents.Event {
		return event.NewEventSource(attackerAgentName).ApplicationEvent(event.Delete, forged)
	})
	defer stop()

	requires.Never(func() bool {
		stored := &v1alpha1.Application{}
		if err := suite.PrincipalClient.Get(suite.Ctx, victimKey, stored, metav1.GetOptions{}); err != nil {
			if errors.IsNotFound(err) {
				t.Logf("UNEXPECTED: the victim application is gone")
			} else {
				t.Logf("could not read the victim application: %v", err)
			}
			return true
		}
		if len(stored.Finalizers) == 0 {
			t.Logf("UNEXPECTED: the victim application's finalizers were stripped")
			return true
		}
		return false
	}, 20*time.Second, 500*time.Millisecond)
}

// TestResyncRequestUpdateConfusedDeputy covers the resync path, which leaks in
// the opposite direction to the other tests in this file.
//
// internal/resync's ProcessRequestUpdateEvent looks an Application up by the
// agent-supplied namespace under destination-based mapping and, on a checksum
// mismatch, pushes the full Application spec back to the *requesting* agent.
// Without an ownership check any managed agent could read another agent's
// Application spec that way - repo URLs, paths, Helm values, destinations.
//
// AppProject and Repository resync already had an equivalent check
// (isAppProjectRelevant); Applications did not. The fix adds
// isApplicationRelevant, guarded on the principal role so that autonomous
// agents - which legitimately receive request-updates from the principal over
// the same shared code path - are unaffected.
func (suite *DestinationMappingTestSuite) TestResyncRequestUpdateConfusedDeputy() {
	requires := suite.Require()
	t := suite.T()

	appName := "confused-deputy-resync-victim"

	t.Log("Create a victim application owned by an agent that is not connected")
	app := createAppForDestMappingTests(appName, destMapAgentNamespace, false)
	app.Spec.Destination.Name = unmappedAgentName
	err := suite.PrincipalClient.Create(suite.Ctx, app, metav1.CreateOptions{})
	requires.NoError(err)
	defer func() {
		requires.NoError(fixture.EnsureDeletion(suite.Ctx, suite.PrincipalClient, app))
	}()

	attackerAgentName := "confused-deputy-resync-attacker"
	attacker, err := dialForgedAgent(suite.Ctx, suite.PrincipalClient, attackerAgentName)
	requires.NoError(err)
	defer attacker.Disconnect()

	t.Log("Attacker subscribes so that the principal has a send queue for it, then asks for an update on the victim's application")
	subCtx, cancelSub := context.WithCancel(suite.Ctx)

	esClient := eventstreamapi.NewEventStreamClient(attacker.Conn())
	stream, err := esClient.Subscribe(subCtx)
	requires.NoError(err, "the forged agent should be able to subscribe; that is not the issue being tested")

	// Collect everything the principal sends us for the duration of the test.
	received := make(chan *cloudevents.Event, 64)
	recvDone := make(chan struct{})

	// Stop the receiver and wait for it to exit before the deferred
	// Disconnect above tears the connection down. Registered after that defer
	// so LIFO ordering runs this first. Without the wait, the goroutine can
	// outlive the test function and panic on t.Logf.
	defer func() {
		cancelSub()
		<-recvDone
	}()

	go func() {
		defer close(recvDone)
		defer close(received)
		for {
			ev, err := stream.Recv()
			if err != nil {
				// A cancelled subscription is our own teardown, not a fault.
				if subCtx.Err() == nil && err != io.EOF {
					t.Logf("subscribe stream ended: %v", err)
				}
				return
			}
			cev, err := format.FromProto(ev.Event)
			if err != nil {
				t.Logf("decoding received event: %v", err)
				continue
			}
			select {
			case received <- cev:
			default:
			}
		}
	}()

	// A deliberately wrong checksum: if the principal did not check
	// ownership, this mismatch is exactly what makes it reply with the
	// victim's full Application spec.
	reqUpdate := event.NewRequestUpdate(appName, destMapAgentNamespace, "Application", "", []byte("deliberately-wrong-checksum"))
	reqEv, err := event.NewEventSource(attackerAgentName).RequestUpdateEvent(reqUpdate)
	requires.NoError(err)

	requires.NoError(sendForgedEvent(suite.Ctx, attacker, reqEv))

	t.Log("the principal must answer with a spec-free delete, never with the victim's application spec")
	deadline := time.After(10 * time.Second)
	resend := time.NewTicker(2 * time.Second)
	defer resend.Stop()

	sawDelete := false
	for {
		select {
		case <-deadline:
			requires.True(sawDelete,
				"the principal never answered the forged request-update; the test cannot tell whether the "+
					"ownership check ran or the request simply went nowhere")
			t.Log("No spec for the victim application was leaked to the attacker")
			return
		case <-resend.C:
			if err := sendForgedEvent(suite.Ctx, attacker, reqEv); err != nil {
				t.Logf("re-sending forged request-update: %v", err)
			}
		case cev, ok := <-received:
			if !ok {
				requires.Fail("the attacker's subscribe stream closed before the assertion window elapsed; " +
					"the test cannot prove anything - investigate rather than ignoring this")
			}
			got := &v1alpha1.Application{}
			if err := cev.DataAs(got); err != nil {
				continue
			}
			if got.Name != appName {
				continue
			}

			requires.Equal(event.Delete.String(), cev.Type(),
				"the principal sent the attacker a %q event for an Application owned by another agent; only a "+
					"spec-free delete is allowed here", cev.Type())

			// Even a delete must not carry the owner's spec.
			requires.Nil(got.Spec.Source, "the delete event must not carry the owner's spec")
			requires.Empty(got.Spec.Destination.Name, "the delete event must not carry the owner's spec")
			sawDelete = true
		}
	}
}

// startForgedEventSender repeatedly pushes the event built by build onto the
// principal's EventStream until the returned stop function is called. stop
// blocks until the sender has actually exited.
//
// The sending deliberately does NOT happen inside the Eventually/Never
// condition. testify runs each condition evaluation in its own goroutine and
// returns as soon as its timer fires, so the final evaluation can still be
// running after the assertion returns - and `defer remote.Disconnect()` then
// nils out the gRPC connection underneath it, panicking the whole test binary
// on a nil *grpc.ClientConn. Note that Never returns via that timer on the
// *success* path, so the crash would happen precisely when the test passes.
//
// Keeping all transport work here, and stopping it before teardown, removes
// that race: the conditions are then pure reads against the Kubernetes API.
//
// Callers must register `defer stop()` *after* `defer remote.Disconnect()` so
// that LIFO ordering stops the sender before the connection goes away.
func startForgedEventSender(ctx context.Context, t *testing.T, remote *client.Remote, interval time.Duration, build func() *cloudevents.Event) (stop func()) {
	sendCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := sendForgedEvent(sendCtx, remote, build()); err != nil {
				// A cancelled context means we are shutting down, not failing.
				if sendCtx.Err() != nil {
					return
				}
				t.Logf("sending forged event: %v", err)
			}
			select {
			case <-sendCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// appHasCondition reports whether the principal's copy of the application at
// key carries a condition of the given type with the given message.
func appHasCondition(suite *DestinationMappingTestSuite, key ktypes.NamespacedName, condType, message string) (bool, error) {
	principalApp := &v1alpha1.Application{}
	if err := suite.PrincipalClient.Get(suite.Ctx, key, principalApp, metav1.GetOptions{}); err != nil {
		return false, err
	}
	for _, c := range principalApp.Status.Conditions {
		if string(c.Type) == condType && c.Message == message {
			return true, nil
		}
	}
	return false, nil
}

// dialForgedAgent opens a raw EventStream connection to the live principal,
// authenticated as a brand-new agent identity that no operator ever
// provisioned. It stands in for an attacker who has compromised a managed
// agent (or otherwise obtained a certificate signed by the trusted CA) and
// talks the wire protocol directly instead of running the honest agent
// binary.
//
// The certificate is signed by the same CA the live principal already trusts
// (read from the "argocd-agent-ca" secret in the control-plane cluster), so
// from the principal's perspective the resulting connection is
// indistinguishable from a legitimately provisioned agent authenticating for
// the first time.
func dialForgedAgent(ctx context.Context, principalClient fixture.KubeClient, agentName string) (*client.Remote, error) {
	kubeClient, err := kubernetes.NewForConfig(principalClient.Config)
	if err != nil {
		return nil, fmt.Errorf("building kube client for principal cluster: %w", err)
	}

	ca, err := tlsutil.TLSCertFromSecret(ctx, kubeClient, fixture.PrincipalNamespace, config.SecretNamePrincipalCA)
	if err != nil {
		return nil, fmt.Errorf("reading principal CA secret: %w", err)
	}

	certPEM, keyPEM, err := tlsutil.GenerateClientCertificate(
		agentName, ca.Leaf, ca.PrivateKey, tlsutil.DefaultLeafCertValidityDays,
		tlsutil.KeyGenOptions{Algorithm: "ecdsa-p256"},
	)
	if err != nil {
		return nil, fmt.Errorf("minting forged client certificate for %q: %w", agentName, err)
	}

	remote, err := client.NewRemote(principalGRPCAddress, principalGRPCPort,
		client.WithTLSClientCertFromBytes([]byte(certPEM), []byte(keyPEM)),
		client.WithRootAuthoritiesFromSecret(kubeClient, fixture.PrincipalNamespace, config.SecretNamePrincipalCA, "tls.crt"),
		client.WithAuth(auth.MethodMTLS, auth.Credentials{}),
		client.WithClientMode(types.AgentModeManaged),
	)
	if err != nil {
		return nil, fmt.Errorf("configuring forged agent client: %w", err)
	}

	// Bound the connection attempt: Remote.Connect() retries indefinitely on
	// retriable errors (which includes TLS/transport failures), so a
	// misconfiguration here would otherwise hang the test forever instead of
	// failing fast.
	connectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := remote.Connect(connectCtx, false); err != nil {
		return nil, fmt.Errorf("authenticating to principal as forged agent %q: %w", agentName, err)
	}

	return remote, nil
}

// sendForgedEvent pushes an already-built cloudevent onto the principal's
// EventStream over a short-lived Push stream, using the exact same
// client-streaming Push RPC a real agent uses. It deliberately does not - and
// should not need to - prove any ownership relationship between the sender
// and the resource it names; establishing that relationship is the
// principal's job.
func sendForgedEvent(ctx context.Context, remote *client.Remote, cev *cloudevents.Event) error {
	// Remote.Disconnect() nils the connection out, and grpc's NewStream
	// dereferences its receiver, so a send racing teardown would panic the
	// whole test binary rather than failing a single test. Fail softly.
	conn := remote.Conn()
	if conn == nil {
		return fmt.Errorf("forged agent connection is closed")
	}

	esClient := eventstreamapi.NewEventStreamClient(conn)

	pushCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stream, err := esClient.Push(pushCtx)
	if err != nil {
		return fmt.Errorf("opening push stream: %w", err)
	}

	protoEv, err := format.ToProto(cev)
	if err != nil {
		return fmt.Errorf("encoding forged event: %w", err)
	}

	if err := stream.Send(&eventstreamapi.Event{Event: protoEv}); err != nil {
		return fmt.Errorf("sending forged event: %w", err)
	}

	_, err = stream.CloseAndRecv()
	return err
}
