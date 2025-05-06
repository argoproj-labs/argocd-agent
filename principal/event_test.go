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
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	wqmock "github.com/argoproj-labs/argocd-agent/test/mocks/k8s-workqueue"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func Test_InvalidEvents(t *testing.T) {
	t.Run("Unknown event schema", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("unknown")
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "unknown target")
		assert.Equal(t, ev, *got)
	})

	t.Run("Unknown event type", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType("application")
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "unable to process event of type application")
		assert.Equal(t, ev, *got)
	})

	t.Run("Invalid data in event", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, "something")
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "failed to unmarshal")
		assert.Equal(t, ev, *got)
	})
}

func Test_CreateEvents(t *testing.T) {
	t.Run("Create application in managed mode", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorIs(t, err, event.ErrEventDiscarded)
		assert.Equal(t, ev, *got)
	})

	t.Run("Update the application if it already exists", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "HEAD",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		fac := kube.NewKubernetesFakeClient(app)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		// Update the application before sending the event
		app.Spec.Source.TargetRevision = "test"
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil))
		require.NoError(t, err)
		s.setAgentMode("argocd", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(context.Background(), "argocd", wq)
		assert.Equal(t, ev, *got)
		assert.NoError(t, err)
		// Check if the application was updated
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("argocd").Get(context.TODO(), "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		require.Equal(t, app.Spec.Source.TargetRevision, napp.Spec.Source.TargetRevision)
	})

	t.Run("Create application in autonomous mode", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "HEAD",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		fac := kube.NewKubernetesFakeClient(app)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil))
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.Equal(t, ev, *got)
		assert.NoError(t, err)
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("foo").Get(context.TODO(), "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		assert.Equal(t, "HEAD", napp.Spec.Source.TargetRevision)
		assert.Nil(t, napp.Operation)
		assert.Equal(t, v1alpha1.SyncStatusCodeSynced, napp.Status.Sync.Status)
		ns, err := fac.Clientset.CoreV1().Namespaces().Get(context.TODO(), "foo", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, ns)
	})
	t.Run("Create pre-existing application in autonomous mode", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "HEAD",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		exapp := app.DeepCopy()
		exapp.Namespace = "foo"
		fac := kube.NewKubernetesFakeClient(exapp)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.Nil(t, err)
		require.Equal(t, ev, *got)
	})

}

func Test_UpdateEvents(t *testing.T) {
	t.Run("Spec update for autonomous mode succeeds", func(t *testing.T) {
		upApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "HEAD",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		exApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "foo",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "main",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		fac := kube.NewKubernetesFakeClient(exApp)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.SpecUpdate.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.NoError(t, err)
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("foo").Get(context.TODO(), "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		assert.Equal(t, "HEAD", napp.Spec.Source.TargetRevision)
		assert.Nil(t, napp.Operation)
		assert.Equal(t, v1alpha1.SyncStatusCodeSynced, napp.Status.Sync.Status)
	})

	t.Run("Spec update for managed mode fails", func(t *testing.T) {
		upApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "foo",
					Path:           ".",
					TargetRevision: "HEAD",
				},
			},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "abc",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{Status: v1alpha1.SyncStatusCodeSynced},
			},
		}
		fac := kube.NewKubernetesFakeClient()
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.SpecUpdate.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeManaged)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.ErrorContains(t, err, "event type not allowed")
	})

}

func Test_createNamespaceIfNotExists(t *testing.T) {
	t.Run("Namespace creation is not enabled", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClient()
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.False(t, created)
		assert.NoError(t, err)
	})
	t.Run("Pattern matches and namespace is created", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClient()
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "^[a-z]+$", nil))
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.True(t, created)
		assert.NoError(t, err)
	})

	t.Run("Pattern does not match and namespace is not created", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClient()
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "^[a]+$", nil))
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.False(t, created)
		assert.NoError(t, err)
	})

	t.Run("Namespace is created only once", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClient()
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil))
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.True(t, created)
		assert.NoError(t, err)
		created, err = s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.False(t, created)
		assert.NoError(t, err)
	})
}
func Test_processAppProjectEvent(t *testing.T) {
	t.Run("Create appproject in managed mode", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("appproject")
		ev.SetType(event.Create.String())
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.ErrorIs(t, err, event.ErrEventDiscarded)
	})

	t.Run("Delete AppProject", func(t *testing.T) {
		upApp := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"foo"},
			},
		}
		fac := kube.NewKubernetesFakeClient()
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("appproject")
		ev.SetType(event.Delete.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeManaged)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.ErrorContains(t, err, "event type not allowed")
	})
}

func Test_processResourceEventResponse(t *testing.T) {
	t.Run("Successful processing of event", func(t *testing.T) {
		rp, err := resourceproxy.New("127.0.0.1:0")
		require.NoError(t, err)
		s := &Server{resourceProxy: rp}
		rch, err := s.resourceProxy.Track("123", "agent")
		require.NoError(t, err)
		require.NotNil(t, rch)
		go func() {
			<-rch
		}()
		evs := event.NewEventSource("agent")
		sentEv := evs.NewResourceResponseEvent("123", 200, "OK")
		err = s.processResourceEventResponse(context.TODO(), "agent", sentEv)
		assert.NoError(t, err)
	})

	t.Run("Timeout waiting for event", func(t *testing.T) {
		rp, err := resourceproxy.New("127.0.0.1:0")
		require.NoError(t, err)
		s := &Server{resourceProxy: rp}
		rch, err := s.resourceProxy.Track("123", "agent")
		require.NoError(t, err)
		require.NotNil(t, rch)
		evs := event.NewEventSource("agent")
		sentEv := evs.NewResourceResponseEvent("123", 200, "OK")
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		err = s.processResourceEventResponse(ctx, "agent", sentEv)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("Response for untracked resource", func(t *testing.T) {
		rp, err := resourceproxy.New("127.0.0.1:0")
		require.NoError(t, err)
		s := &Server{resourceProxy: rp}
		rch, err := s.resourceProxy.Track("123", "agent")
		require.NoError(t, err)
		require.NotNil(t, rch)
		evs := event.NewEventSource("agent")
		sentEv := evs.NewResourceResponseEvent("124", 200, "OK")
		err = s.processResourceEventResponse(context.TODO(), "agent", sentEv)
		assert.ErrorContains(t, err, "response not tracked")
	})

	t.Run("Response for wrong agent", func(t *testing.T) {
		rp, err := resourceproxy.New("127.0.0.1:0")
		require.NoError(t, err)
		s := &Server{resourceProxy: rp}
		rch, err := s.resourceProxy.Track("123", "other")
		require.NoError(t, err)
		require.NotNil(t, rch)
		evs := event.NewEventSource("agent")
		sentEv := evs.NewResourceResponseEvent("123", 200, "OK")
		err = s.processResourceEventResponse(context.TODO(), "agent", sentEv)
		assert.ErrorContains(t, err, "agent mismap")
	})
}

func Test_processIncomingResourceResyncEvent(t *testing.T) {
	ctx := context.Background()
	agentName := "test"

	fakeClient := kube.NewKubernetesFakeClient()
	fakeClient.RestConfig = &rest.Config{}

	s, err := NewServer(ctx, fakeClient, agentName, WithGeneratedTokenSigningKey())
	assert.Nil(t, err)

	err = s.queues.Create(agentName)
	assert.Nil(t, err)
	s.events = event.NewEventSource("test")

	t.Run("discard SyncedResourceList in autonomous mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		ev, err := s.events.RequestSyncedResourceListEvent([]byte{})
		assert.Nil(t, err)

		expected := "principal can only handle SyncedResourceList request in the managed mode"
		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process SyncedResourceList in managed mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeManaged)

		ev, err := s.events.RequestSyncedResourceListEvent([]byte{})
		assert.Nil(t, err)

		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Nil(t, err)
	})

	t.Run("process SyncedResource in autonomous mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		res := resources.ResourceKey{
			Name: "sample",
			Kind: "Application",
		}
		ev, err := s.events.SyncedResourceEvent(res)
		assert.Nil(t, err)

		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.NotNil(t, err)
	})

	t.Run("discard SyncedResource in managed mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeManaged)

		ev, err := s.events.SyncedResourceEvent(resources.ResourceKey{})
		assert.Nil(t, err)

		expected := "principal can only handle SyncedResource request in autonomous mode"
		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process request update in managed mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeManaged)

		update := &event.RequestUpdate{
			Name: "test",
			Kind: "Application",
		}
		ev, err := s.events.RequestUpdateEvent(update)
		assert.Nil(t, err)

		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.NotNil(t, err)
	})

	t.Run("discard request update in autonomous mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		update := &event.RequestUpdate{}
		ev, err := s.events.RequestUpdateEvent(update)
		assert.Nil(t, err)

		expected := "principal can only handle request update in the managed mode"
		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process ResourceResync in autonomous mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		ev, err := s.events.RequestResourceResyncEvent()
		assert.Nil(t, err)

		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Nil(t, err)
	})

	t.Run("discard ResourceResync in managed mode", func(t *testing.T) {
		s.setAgentMode(agentName, types.AgentModeManaged)

		ev, err := s.events.RequestResourceResyncEvent()
		assert.Nil(t, err)

		expected := "principal can only handle ResourceResync request in autonomous mode"
		err = s.processIncomingResourceResyncEvent(ctx, agentName, ev)
		assert.Equal(t, expected, err.Error())
	})
}
