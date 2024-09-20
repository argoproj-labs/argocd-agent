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

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	wqmock "github.com/argoproj-labs/argocd-agent/test/mocks/k8s-workqueue"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_InvalidEvents(t *testing.T) {
	t.Run("Invalid event data type in queue", func(t *testing.T) {
		wq := wqmock.NewRateLimitingInterface(t)
		item := "foo"
		wq.On("Get").Return(&item, false)
		wq.On("Done", &item)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "invalid data in queue")

	})

	t.Run("Unknown event schema", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("unknown")
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "unable to process event with unknown target")
	})

	t.Run("Unknown event type", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType("application")
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "unable to process event of type application")
	})

	t.Run("Invalid data in event", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, "something")
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorContains(t, err, "failed to unmarshal")
	})
}

func Test_CreateEvents(t *testing.T) {
	t.Run("Create application in managed mode", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClient(), "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.ErrorIs(t, err, event.ErrEventDiscarded)
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
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil))
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		err = s.processRecvQueue(context.Background(), "foo", wq)
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
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.True(t, errors.IsAlreadyExists(err))
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
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)
		err = s.processRecvQueue(context.Background(), "foo", wq)
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
		wq := wqmock.NewRateLimitingInterface(t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeManaged)
		err = s.processRecvQueue(context.Background(), "foo", wq)
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
