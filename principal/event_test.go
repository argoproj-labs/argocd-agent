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
	"strings"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	wqmock "github.com/argoproj-labs/argocd-agent/test/mocks/k8s-workqueue"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
)

func Test_InvalidEvents(t *testing.T) {
	t.Run("Unknown event schema", func(t *testing.T) {
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("unknown")
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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
		fac := kube.NewKubernetesFakeClientWithApps("argocd", app)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		// Update the application before sending the event
		app.Spec.Source.TargetRevision = "test"
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil), WithRedisProxyDisabled())
		s.Start(context.Background(), make(chan error))
		s.clusterMgr.MapCluster("argocd", &v1alpha1.Cluster{Name: "argocd", Server: "https://argocd.com"})
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
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "argoproj.io/v1alpha1",
						Kind:       "ApplicationSet",
						Name:       "test",
						UID:        "test",
					},
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "test",
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
		fac := kube.NewKubernetesFakeClientWithApps("argocd", app)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.clusterMgr.MapCluster("foo", &v1alpha1.Cluster{Name: "foo", Server: "https://foo.com"})
		s.setAgentMode("foo", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		assert.Equal(t, ev, *got)
		assert.NoError(t, err)
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("foo").Get(context.TODO(), "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		assert.Equal(t, "HEAD", napp.Spec.Source.TargetRevision)
		// OwnerReferences should be dropped on the control-plane application
		assert.Empty(t, napp.OwnerReferences)
		// Check that the destination is set to the cluster mapping
		assert.Equal(t, "foo", napp.Spec.Destination.Name)
		assert.Equal(t, "", napp.Spec.Destination.Server)
		assert.Nil(t, napp.Operation)
		assert.Equal(t, v1alpha1.SyncStatusCodeSynced, napp.Status.Sync.Status)
		prefixedName, err := agentPrefixedProjectName(app.Spec.Project, "foo")
		assert.Nil(t, err)
		assert.Equal(t, prefixedName, napp.Spec.Project)
		ns, err := fac.Clientset.CoreV1().Namespaces().Get(context.TODO(), "foo", v1.GetOptions{})
		assert.NoError(t, err)
		assert.NotNil(t, ns)
	})
	t.Run("Create pre-existing application in autonomous mode", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "argoproj.io/v1alpha1",
						Kind:       "ApplicationSet",
						Name:       "test",
						UID:        "test",
					},
				},
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
		exapp.OwnerReferences = nil
		fac := kube.NewKubernetesFakeClientWithApps("argocd", exapp)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, app)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		s, err := NewServer(ctx, fac, "argocd",
			WithGeneratedTokenSigningKey(),
			WithInformerSyncTimeout(5*time.Second),
			WithRedisProxyDisabled(),
		)
		require.NoError(t, err)

		defer func() {
			_ = s.Shutdown()
		}()

		err = s.Start(ctx, make(chan error))
		require.NoError(t, err)

		s.clusterMgr.MapCluster("foo", &v1alpha1.Cluster{Name: "foo", Server: "https://foo.com"})
		s.setAgentMode("foo", types.AgentModeAutonomous)
		got, err := s.processRecvQueue(ctx, "foo", wq)
		assert.Nil(t, err)
		require.Equal(t, ev, *got)
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("foo").Get(ctx, "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		assert.Empty(t, napp.OwnerReferences)
		// Check that the destination is set to the cluster mapping
		assert.Equal(t, "foo", napp.Spec.Destination.Name)
		assert.Equal(t, "", napp.Spec.Destination.Server)
	})

}

func Test_UpdateEvents(t *testing.T) {
	t.Run("Spec update for autonomous mode succeeds", func(t *testing.T) {
		upApp := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "argoproj.io/v1alpha1",
						Kind:       "ApplicationSet",
						Name:       "test",
						UID:        "test",
					},
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
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
		fac := kube.NewKubernetesFakeClientWithApps("argocd", exApp)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.SpecUpdate.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		s, err := NewServer(ctx, fac, "argocd",
			WithGeneratedTokenSigningKey(),
			WithInformerSyncTimeout(2*time.Second),
			WithRedisProxyDisabled(),
		)
		require.NoError(t, err)

		defer func() {
			_ = s.Shutdown()
		}()

		err = s.Start(ctx, make(chan error))
		require.NoError(t, err)

		s.setAgentMode("foo", types.AgentModeAutonomous)
		s.clusterMgr.MapCluster("foo", &v1alpha1.Cluster{Name: "foo", Server: "https://foo.com"})
		got, err := s.processRecvQueue(ctx, "foo", wq)
		require.Equal(t, ev, *got)
		assert.NoError(t, err)
		napp, err := fac.ApplicationsClientset.ArgoprojV1alpha1().Applications("foo").Get(ctx, "test", v1.GetOptions{})
		assert.NoError(t, err)
		require.NotNil(t, napp)
		assert.Equal(t, "HEAD", napp.Spec.Source.TargetRevision)
		// OwnerReferences should be dropped on spec update in autonomous mode
		assert.Empty(t, napp.OwnerReferences)
		// Check that the destination is set to the cluster mapping
		assert.Equal(t, "foo", napp.Spec.Destination.Name)
		assert.Equal(t, "", napp.Spec.Destination.Server)
		assert.Nil(t, napp.Operation)
		assert.Equal(t, "foo-default", napp.Spec.Project)
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
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("application")
		ev.SetType(event.SpecUpdate.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeManaged)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.ErrorContains(t, err, "event type not allowed")
	})

}

func Test_DeleteEvents_ManagedMode(t *testing.T) {

	type test struct {
		name                            string
		deletionTimestampSetOnPrincipal bool
	}

	tests := []test{
		{
			name:                            "Delete event from managed mode agent should allow deletion if principal is already being deleted",
			deletionTimestampSetOnPrincipal: true,
		},
		{
			name:                            "Delete event from managed mode agent should NOT allow deletion if principal is NOT already being deleted",
			deletionTimestampSetOnPrincipal: false,
		},
	}

	for _, test := range tests {

		t.Run(test.name, func(t *testing.T) {
			delApp := &v1alpha1.Application{
				ObjectMeta: v1.ObjectMeta{
					Name:       "test",
					Namespace:  "foo",
					Finalizers: []string{},
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

			if test.deletionTimestampSetOnPrincipal {
				delApp.DeletionTimestamp = ptr.To(v1.Time{Time: time.Now()})
			}

			// Create fake client with the application already in it
			fac := kube.NewKubernetesFakeClientWithApps("argocd", delApp)

			ev := cloudevents.NewEvent()
			ev.SetDataSchema("application")
			ev.SetType(event.Delete.String())
			ev.SetData(cloudevents.ApplicationJSON, delApp)
			wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
			wq.On("Get").Return(&ev, false)
			wq.On("Done", &ev)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			s, err := NewServer(ctx, fac, "argocd",
				WithGeneratedTokenSigningKey(),
				WithInformerSyncTimeout(2*time.Second),
			)
			require.NoError(t, err)

			defer func() {
				_ = s.Shutdown()
			}()

			err = s.Start(ctx, make(chan error))
			require.NoError(t, err)

			s.setAgentMode("foo", types.AgentModeManaged)

			var cachedApp *v1alpha1.Application
			for i := 0; i < 20; i++ {
				cachedApp, err = s.appManager.Get(ctx, delApp.Name, delApp.Namespace)
				if err == nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			require.NoError(t, err)

			if test.deletionTimestampSetOnPrincipal {
				require.NotNil(t, cachedApp.DeletionTimestamp)
			}

			got, err := s.processRecvQueue(ctx, "foo", wq)
			require.NoError(t, err)

			if test.deletionTimestampSetOnPrincipal {
				require.NoError(t, err)
				require.Equal(t, ev, *got)
				_, err = fac.ApplicationsClientset.ArgoprojV1alpha1().Applications(delApp.Namespace).Get(ctx, delApp.Name, v1.GetOptions{})
				require.True(t, apierrors.IsNotFound(err))
			} else {
				require.NoError(t, err)
				_, err = fac.ApplicationsClientset.ArgoprojV1alpha1().Applications(delApp.Namespace).Get(ctx, delApp.Name, v1.GetOptions{})
				require.NoError(t, err)
			}
		})
	}

}

func Test_createNamespaceIfNotExists(t *testing.T) {
	t.Run("Namespace creation is not enabled", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.False(t, created)
		assert.NoError(t, err)
	})
	t.Run("Pattern matches and namespace is created", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "^[a-z]+$", nil), WithRedisProxyDisabled())
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.True(t, created)
		assert.NoError(t, err)
	})

	t.Run("Pattern does not match and namespace is not created", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "^[a]+$", nil), WithRedisProxyDisabled())
		require.NoError(t, err)
		created, err := s.createNamespaceIfNotExist(context.TODO(), "test")
		assert.False(t, created)
		assert.NoError(t, err)
	})

	t.Run("Namespace is created only once", func(t *testing.T) {
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithAutoNamespaceCreate(true, "", nil), WithRedisProxyDisabled())
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
		s, err := NewServer(context.Background(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		got, err := s.processRecvQueue(context.Background(), "foo", wq)
		require.Equal(t, ev, *got)
		assert.ErrorIs(t, err, event.ErrEventDiscarded)
	})

	t.Run("Create appProject in autonomous mode", func(t *testing.T) {
		project := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
				OwnerReferences: []v1.OwnerReference{
					{
						APIVersion: "argoproj.io/v1alpha1",
						Kind:       "ApplicationSet",
						Name:       "owner",
						UID:        "uid-1",
					},
				},
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"foo"},
				Destinations: []v1alpha1.ApplicationDestination{
					{
						Name:   "original-cluster",
						Server: "https://original.server.com",
					},
					{
						Name:   "another-cluster",
						Server: "https://another.server.com",
					},
				},
			},
		}

		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("appproject")
		ev.SetType(event.Create.String())
		ev.SetData(cloudevents.ApplicationJSON, project)

		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)

		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)

		_, err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.NoError(t, err)

		projName, err := agentPrefixedProjectName(project.Name, "foo")
		assert.Nil(t, err)

		// Check that the AppProject was created with the prefixed name
		createdProject, err := fac.ApplicationsClientset.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), projName, v1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, projName, createdProject.Name)
		// OwnerReferences should be dropped on the control-plane appProject
		assert.Empty(t, createdProject.OwnerReferences)

		// Check that SourceNamespaces is set to the agent name
		assert.Equal(t, []string{"foo"}, createdProject.Spec.SourceNamespaces)

		// Check that all destinations are updated to point to the agent cluster
		assert.Len(t, createdProject.Spec.Destinations, 2)
		for _, dest := range createdProject.Spec.Destinations {
			assert.Equal(t, "foo", dest.Name)
			assert.Equal(t, "*", dest.Server)
		}
	})

	t.Run("Update appProject in autonomous mode", func(t *testing.T) {
		project := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo-test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"foo"},
				Destinations: []v1alpha1.ApplicationDestination{
					{
						Name:   "original-cluster",
						Server: "https://original.server.com",
					},
				},
			},
		}

		fac := kube.NewKubernetesFakeClientWithApps("argocd", project)
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("appproject")
		ev.SetType(event.SpecUpdate.String())

		updatedProject := project.DeepCopy()
		// include owner refs in update payload and ensure they are dropped
		updatedProject.OwnerReferences = []v1.OwnerReference{{APIVersion: "argoproj.io/v1alpha1", Kind: "ApplicationSet", Name: "owner2", UID: "uid-2"}}
		updatedProject.Spec.Description = "updated"
		updatedProject.Name = "test" // Use original name (will be prefixed)
		ev.SetData(cloudevents.ApplicationJSON, updatedProject)

		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)

		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.setAgentMode("foo", types.AgentModeAutonomous)

		_, err = s.processRecvQueue(context.Background(), "foo", wq)
		assert.NoError(t, err)

		projName, err := agentPrefixedProjectName(updatedProject.Name, "foo")
		assert.Nil(t, err)

		// Check that the AppProject was updated with the correct changes
		got, err := fac.ApplicationsClientset.ArgoprojV1alpha1().AppProjects("argocd").Get(context.TODO(), projName, v1.GetOptions{})
		assert.NoError(t, err)
		assert.Equal(t, updatedProject.Spec.Description, got.Spec.Description)
		// OwnerReferences should be dropped on update as well
		assert.Empty(t, got.OwnerReferences)

		// Check that SourceNamespaces is set to the agent name
		assert.Equal(t, []string{"foo"}, got.Spec.SourceNamespaces)

		// Check that all destinations are updated to point to the agent cluster
		assert.Len(t, got.Spec.Destinations, 1)
		assert.Equal(t, "foo", got.Spec.Destinations[0].Name)
		assert.Equal(t, "*", got.Spec.Destinations[0].Server)
	})

	t.Run("Delete AppProject in managed mode", func(t *testing.T) {
		upApp := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "argocd",
			},
			Spec: v1alpha1.AppProjectSpec{
				SourceRepos: []string{"foo"},
			},
		}
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		ev := cloudevents.NewEvent()
		ev.SetDataSchema("appproject")
		ev.SetType(event.Delete.String())
		ev.SetData(cloudevents.ApplicationJSON, upApp)
		wq := wqmock.NewTypedRateLimitingInterface[*cloudevents.Event](t)
		wq.On("Get").Return(&ev, false)
		wq.On("Done", &ev)
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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

	fakeClient := kube.NewKubernetesFakeClientWithApps(agentName)
	fakeClient.RestConfig = &rest.Config{}

	s, err := NewServer(ctx, fakeClient, agentName, WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
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

func Test_agentPrefixedProjectName(t *testing.T) {
	t.Run("Normal project name", func(t *testing.T) {
		result, err := agentPrefixedProjectName("myproject", "myagent")
		assert.Nil(t, err)
		assert.Equal(t, "myagent-myproject", result)
	})

	t.Run("Return an error if the name exceeds DNS1123 limit", func(t *testing.T) {
		// Create a project name that when combined with agent name exceeds 253 characters
		longProjectName := strings.Repeat("test", 245)
		agentName := "my-agent"

		result, err := agentPrefixedProjectName(longProjectName, agentName)
		assert.ErrorContains(t, err, "agent prefixed project name cannot be longer than 253 characters")
		assert.Empty(t, result)
	})
}

func Test_processClusterCacheInfoUpdateEvent(t *testing.T) {
	t.Run("Returns error when event data is invalid", func(t *testing.T) {
		agentName := "test-agent"

		// Create a event with invalid data
		ev := cloudevents.NewEvent()
		ev.SetDataSchema(event.TargetClusterCacheInfoUpdate.String())
		ev.SetType(event.ClusterCacheInfoUpdate.String())
		err := ev.SetData(cloudevents.ApplicationJSON, "invalid-json-data")
		require.NoError(t, err)

		// Create a server with fake client
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		err = s.processClusterCacheInfoUpdateEvent(agentName, &ev)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "failed to unmarshal")
	})

	t.Run("Processes event with correct data", func(t *testing.T) {
		agentName := "unmapped-agent"
		clusterInfo := &event.ClusterCacheInfo{
			ApplicationsCount: 3,
			APIsCount:         6,
			ResourcesCount:    50,
		}

		// Create a event
		ev := cloudevents.NewEvent()
		ev.SetDataSchema(event.TargetClusterCacheInfoUpdate.String())
		ev.SetType(event.ClusterCacheInfoUpdate.String())
		ev.SetExtension("eventid", "test-event-456")
		ev.SetExtension("resourceid", "test-resource-456")
		err := ev.SetData(cloudevents.ApplicationJSON, clusterInfo)
		require.NoError(t, err)

		// Create a server with fake client
		fac := kube.NewKubernetesFakeClientWithApps("argocd")
		s, err := NewServer(context.Background(), fac, "argocd", WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		require.NoError(t, err)
		s.setAgentMode(agentName, types.AgentModeAutonomous)

		err = s.processClusterCacheInfoUpdateEvent(agentName, &ev)

		// Should get error about agent not being mapped, the error is expected
		// as goal of this test is to verify only processClusterCacheInfoUpdateEvent logic
		assert.Error(t, err)
		assert.ErrorContains(t, err, "not mapped to any cluster")
	})
}
