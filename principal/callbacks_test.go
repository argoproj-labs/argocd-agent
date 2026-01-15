// Copyright 2025 The argocd-agent Authors
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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	synccommon "github.com/argoproj/gitops-engine/pkg/sync/common"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func TestMapAppProjectToAgents(t *testing.T) {
	tests := []struct {
		name       string
		appProject v1alpha1.AppProject
		agents     map[string]types.AgentMode
		want       map[string]bool
	}{
		{
			name: "matches single agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"cluster-1"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeAutonomous,
			},
			want: map[string]bool{
				"cluster-1": true,
			},
		},
		{
			name: "matches multiple agents",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
			},
			want: map[string]bool{
				"cluster-1": true,
				"cluster-2": true,
			},
		},
		{
			name: "doesn't match the destination",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "test-*"},
					},
					SourceNamespaces: []string{"cluster-1"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeAutonomous,
			},
			want: map[string]bool{},
		},
		{
			name: "doesn't match the sourceNamespace",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"random"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
			},
			want: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{namespaceMap: tt.agents}
			got := s.mapAppProjectToAgents(tt.appProject)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSyncAppProjectUpdatesToAgents(t *testing.T) {
	tests := []struct {
		name                 string
		oldAppProject        v1alpha1.AppProject
		newAppProject        v1alpha1.AppProject
		agents               map[string]types.AgentMode
		expectedAgentsDelete []string
		expectedAgentsUpdate []string
	}{
		{
			name: "agent added - sends create event",
			oldAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-1"},
					},
					SourceNamespaces: []string{"cluster-1"},
				},
			},
			newAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-1"},
						{Name: "cluster-2"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
			},
			expectedAgentsDelete: []string{},
			expectedAgentsUpdate: []string{"cluster-1", "cluster-2"},
		},
		{
			name: "agent removed - sends delete event",
			oldAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			newAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-1"},
					},
					SourceNamespaces: []string{"cluster-1"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
			},
			expectedAgentsDelete: []string{"cluster-2"},
			expectedAgentsUpdate: []string{"cluster-1"},
		},
		{
			name: "mixed changes - delete from one, update another, add new",
			oldAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-1"},
						{Name: "cluster-2"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			newAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-2"},
						{Name: "cluster-3"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
				"cluster-3": types.AgentModeManaged,
			},
			expectedAgentsDelete: []string{"cluster-1"},
			expectedAgentsUpdate: []string{"cluster-2", "cluster-3"},
		},
		{
			name: "no changes - only updates existing agents",
			oldAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			newAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeManaged,
				"cluster-2": types.AgentModeManaged,
			},
			expectedAgentsDelete: []string{},
			expectedAgentsUpdate: []string{"cluster-1", "cluster-2"},
		},
		{
			name: "autonomous agents ignored",
			oldAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			newAppProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "cluster-*"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			agents: map[string]types.AgentMode{
				"cluster-1": types.AgentModeAutonomous,
				"cluster-2": types.AgentModeManaged,
			},
			expectedAgentsDelete: []string{},
			expectedAgentsUpdate: []string{"cluster-2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			sentEvents := make(map[string][]*cloudevents.Event)

			// Create server with mock dependencies
			s := &Server{
				queues:       queue.NewSendRecvQueues(),
				events:       event.NewEventSource("test"),
				namespaceMap: tt.agents,
			}

			// Create mock queues for each agent
			for agentName := range tt.agents {
				err := s.queues.Create(agentName)
				require.NoError(t, err)
			}

			// Create a logger for testing
			logCtx := logrus.WithField("test", "syncAppProjectUpdatesToAgents")

			// Execute the function under test
			s.syncAppProjectUpdatesToAgents(context.Background(), &tt.oldAppProject, &tt.newAppProject, logCtx)

			// Collect events from all queues
			var deleteEventCount, updateEventCount int
			var agentsWithDeletes, agentsWithUpdates []string

			for agentName := range tt.agents {
				q := s.queues.SendQ(agentName)
				require.NotNil(t, q)

				for q.Len() > 0 {
					ev, shutdown := q.Get()
					require.False(t, shutdown)
					require.NotNil(t, ev)

					if sentEvents[agentName] == nil {
						sentEvents[agentName] = []*cloudevents.Event{}
					}
					sentEvents[agentName] = append(sentEvents[agentName], ev)

					// Count event types
					switch ev.Type() {
					case event.Delete.String():
						deleteEventCount++
						agentsWithDeletes = append(agentsWithDeletes, agentName)
					case event.SpecUpdate.String():
						updateEventCount++
						agentsWithUpdates = append(agentsWithUpdates, agentName)
					}

					q.Done(ev)
				}
			}

			// Verify event counts
			assert.Equal(t, len(tt.expectedAgentsDelete), deleteEventCount, "Delete event count mismatch")
			assert.Equal(t, len(tt.expectedAgentsUpdate), updateEventCount, "Update event count mismatch")

			// Verify which agents received delete events
			assert.ElementsMatch(t, tt.expectedAgentsDelete, agentsWithDeletes, "Agents with delete events mismatch")

			// Verify which agents received update events
			assert.ElementsMatch(t, tt.expectedAgentsUpdate, agentsWithUpdates, "Agents with update events mismatch")
		})
	}
}

func TestIsResourceFromAutonomousAgent(t *testing.T) {
	tests := []struct {
		name    string
		project v1alpha1.AppProject
		want    bool
	}{
		{
			name: "project with SourceUID annotation is autonomous",
			project: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
					Annotations: map[string]string{
						manager.SourceUIDAnnotation: "some-uid",
					},
				},
			},
			want: true,
		},
		{
			name: "project without annotations is not autonomous",
			project: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
				},
			},
			want: false,
		},
		{
			name: "project with empty annotations is not autonomous",
			project: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-project",
					Annotations: map[string]string{},
				},
			},
			want: false,
		},
		{
			name: "project with other annotations but no SourceUID is not autonomous",
			project: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-project",
					Annotations: map[string]string{
						"other-annotation": "value",
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isResourceFromAutonomousAgent(&tt.project)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestServer_newRepositoryCallback(t *testing.T) {
	tests := []struct {
		name             string
		repositorySecret *corev1.Secret
		namespaceMap     map[string]types.AgentMode
		projectSetup     func(*mocks.AppProject)
		expectEvents     bool
		expectedAgents   []string
		shouldError      bool
	}{
		{
			name: "successful repository creation with single agent",
			repositorySecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					UID:       "repo-uid-123",
				},
				Data: map[string][]byte{
					"project": []byte("default"),
					"url":     []byte("https://github.com/example/repo.git"),
				},
			},
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeManaged,
				"agent2": types.AgentModeAutonomous,
			},
			projectSetup: func(mockProjectBackend *mocks.AppProject) {
				project := &v1alpha1.AppProject{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default",
						Namespace: "argocd",
					},
					Spec: v1alpha1.AppProjectSpec{
						Destinations: []v1alpha1.ApplicationDestination{
							{Name: "agent1"},
						},
						SourceNamespaces: []string{"agent1"},
					},
				}
				mockProjectBackend.On("Get", mock.Anything, "default", "argocd").Return(project, nil)
			},
			expectEvents:   true,
			expectedAgents: []string{"agent1"},
			shouldError:    false,
		},
		{
			name: "repository without project data should skip",
			repositorySecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-repo",
					Namespace: "argocd",
					UID:       "repo-uid-123",
				},
				Data: map[string][]byte{
					"url": []byte("https://github.com/example/repo.git"),
					// no "project" key
				},
			},
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeManaged,
			},
			projectSetup:   func(mockProjectBackend *mocks.AppProject) {},
			expectEvents:   false,
			expectedAgents: []string{},
			shouldError:    false,
		},
		{
			name: "repository with multiple matching agents",
			repositorySecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-agent-repo",
					Namespace: "argocd",
					UID:       "repo-uid-456",
				},
				Data: map[string][]byte{
					"project": []byte("multi-agent-project"),
					"url":     []byte("https://github.com/example/multi-repo.git"),
				},
			},
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeManaged,
				"agent2": types.AgentModeManaged,
				"agent3": types.AgentModeAutonomous, // should be ignored
			},
			projectSetup: func(mockProjectBackend *mocks.AppProject) {
				project := &v1alpha1.AppProject{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "multi-agent-project",
						Namespace: "argocd",
					},
					Spec: v1alpha1.AppProjectSpec{
						Destinations: []v1alpha1.ApplicationDestination{
							{Name: "agent*"},
						},
						SourceNamespaces: []string{"agent1", "agent2"},
					},
				}
				mockProjectBackend.On("Get", mock.Anything, "multi-agent-project", "argocd").Return(project, nil)
			},
			expectEvents:   true,
			expectedAgents: []string{"agent1", "agent2"},
			shouldError:    false,
		},
		{
			name: "project not found should return early",
			repositorySecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "orphaned-repo",
					Namespace: "argocd",
					UID:       "repo-uid-789",
				},
				Data: map[string][]byte{
					"project": []byte("nonexistent-project"),
					"url":     []byte("https://github.com/example/orphaned.git"),
				},
			},
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeManaged,
			},
			projectSetup: func(mockProjectBackend *mocks.AppProject) {
				mockProjectBackend.On("Get", mock.Anything, "nonexistent-project", "argocd").Return(nil, assert.AnError)
			},
			expectEvents:   false,
			expectedAgents: []string{},
			shouldError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock project backend
			mockProjectBackend := &mocks.AppProject{}
			tt.projectSetup(mockProjectBackend)

			// Create project manager with mock backend
			projectManager, err := appproject.NewAppProjectManager(mockProjectBackend, "argocd")
			require.NoError(t, err)

			// Create server with dependencies
			s := &Server{
				ctx:            context.Background(),
				queues:         queue.NewSendRecvQueues(),
				events:         event.NewEventSource("test"),
				namespaceMap:   tt.namespaceMap,
				projectManager: projectManager,
				resources:      resources.NewAgentResources(),
				repoToAgents:   NewMapToSet(),
				projectToRepos: NewMapToSet(),
			}

			// Create queues for agents
			for agentName := range tt.namespaceMap {
				err := s.queues.Create(agentName)
				require.NoError(t, err)
			}

			// Execute the function under test
			s.newRepositoryCallback(tt.repositorySecret)

			if tt.expectEvents {
				// Verify events were sent to expected agents
				var eventsReceived []string
				for _, agentName := range tt.expectedAgents {
					q := s.queues.SendQ(agentName)
					require.NotNil(t, q)

					if q.Len() > 0 {
						ev, shutdown := q.Get()
						require.False(t, shutdown)
						require.NotNil(t, ev)

						// Verify event type is repository create
						assert.Equal(t, event.Create.String(), ev.Type())

						eventsReceived = append(eventsReceived, agentName)
						q.Done(ev)
					}
				}

				assert.ElementsMatch(t, tt.expectedAgents, eventsReceived, "Events should be sent to expected agents")

				// Verify projectToRepos mapping was updated
				repoSet := s.projectToRepos.Get(string(tt.repositorySecret.Data["project"]))
				assert.True(t, repoSet[tt.repositorySecret.Name], "Repository should be added to project mapping")

				// Verify repoToAgents mapping was updated for each agent
				for _, agentName := range tt.expectedAgents {
					agentSet := s.repoToAgents.Get(tt.repositorySecret.Name)
					assert.True(t, agentSet[agentName], "Agent should be added to repository mapping")
				}
			} else {
				// Verify no events were sent
				for agentName := range tt.namespaceMap {
					q := s.queues.SendQ(agentName)
					require.NotNil(t, q)
					assert.Equal(t, 0, q.Len(), "No events should be sent when not expected")
				}
			}

			// Verify mock expectations
			mockProjectBackend.AssertExpectations(t)
		})
	}
}

func TestServer_syncRepositoryUpdatesToAgents(t *testing.T) {
	// Helper to create a secret with project
	createSecret := func(project string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "argocd"},
			Data:       map[string][]byte{"project": []byte(project), "url": []byte("https://example.com/repo.git")},
		}
	}

	// Helper to create a project with agent destinations
	createProject := func(name string, agents ...string) *v1alpha1.AppProject {
		var destinations []v1alpha1.ApplicationDestination
		for _, agent := range agents {
			destinations = append(destinations, v1alpha1.ApplicationDestination{Name: agent})
		}
		return &v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "argocd"},
			Spec:       v1alpha1.AppProjectSpec{Destinations: destinations, SourceNamespaces: agents},
		}
	}

	// Helper to setup server and execute function
	executeTest := func(t *testing.T, mockBackend *mocks.AppProject, oldSecret, newSecret *corev1.Secret, existingMapping map[string]bool) (deleteEvents, updateEvents []string, finalMapping map[string]bool) {
		projectManager, _ := appproject.NewAppProjectManager(mockBackend, "argocd")
		s := &Server{
			ctx:            context.Background(),
			queues:         queue.NewSendRecvQueues(),
			events:         event.NewEventSource("test"),
			projectManager: projectManager,
			repoToAgents:   NewMapToSet(),
			projectToRepos: NewMapToSet(),
			namespaceMap:   map[string]types.AgentMode{"agent1": types.AgentModeManaged, "agent2": types.AgentModeManaged},
		}

		// Setup existing mapping and queues
		for agent := range existingMapping {
			s.repoToAgents.Add("test-repo", agent)
		}
		s.queues.Create("agent1")
		s.queues.Create("agent2")

		// Execute function
		logCtx := logrus.WithField("test", "syncRepositoryUpdatesToAgents")
		s.syncRepositoryUpdatesToAgents(context.Background(), oldSecret, newSecret, logCtx)

		// Collect events
		for _, agent := range []string{"agent1", "agent2"} {
			if q := s.queues.SendQ(agent); q != nil {
				for q.Len() > 0 {
					ev, _ := q.Get()
					switch ev.Type() {
					case event.Delete.String():
						deleteEvents = append(deleteEvents, agent)
					case event.SpecUpdate.String():
						updateEvents = append(updateEvents, agent)
					}
					q.Done(ev)
				}
			}
		}

		return deleteEvents, updateEvents, s.repoToAgents.Get("test-repo")
	}

	t.Run("repository changes project - agents migrate", func(t *testing.T) {
		// Repository moves from "old-project" (agent1) to "new-project" (agent2)
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "old-project", "argocd").Return(createProject("old-project", "agent1"), nil)
		mockBackend.On("Get", mock.Anything, "new-project", "argocd").Return(createProject("new-project", "agent2"), nil)

		oldSecret := createSecret("old-project")
		newSecret := createSecret("new-project")
		existingMapping := map[string]bool{"agent1": true}

		deleteEvents, updateEvents, finalMapping := executeTest(t, mockBackend, oldSecret, newSecret, existingMapping)

		// agent1 should get delete event, agent2 should get update event
		assert.ElementsMatch(t, []string{"agent1"}, deleteEvents)
		assert.ElementsMatch(t, []string{"agent2"}, updateEvents)
		assert.Equal(t, map[string]bool{"agent2": true}, finalMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("repository updated within same project", func(t *testing.T) {
		// Repository URL changes but stays in same project - both agents get updates
		mockBackend := &mocks.AppProject{}
		project := createProject("same-project", "agent1", "agent2")
		mockBackend.On("Get", mock.Anything, "same-project", "argocd").Return(project, nil).Twice()

		oldSecret := createSecret("same-project")
		newSecret := createSecret("same-project")
		newSecret.Data["url"] = []byte("https://different-url.com/repo.git") // Change URL
		existingMapping := map[string]bool{"agent1": true, "agent2": true}

		deleteEvents, updateEvents, finalMapping := executeTest(t, mockBackend, oldSecret, newSecret, existingMapping)

		// No deletes, both agents get updates
		assert.Empty(t, deleteEvents)
		assert.ElementsMatch(t, []string{"agent1", "agent2"}, updateEvents)
		assert.Equal(t, map[string]bool{"agent1": true, "agent2": true}, finalMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("old project deleted - uses cached mapping", func(t *testing.T) {
		// Old project no longer exists, function uses cached repo-to-agents mapping
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "deleted-project", "argocd").Return(nil, errors.NewNotFound(schema.GroupResource{}, "deleted-project"))
		mockBackend.On("Get", mock.Anything, "new-project", "argocd").Return(createProject("new-project", "agent2"), nil)

		oldSecret := createSecret("deleted-project")
		newSecret := createSecret("new-project")
		existingMapping := map[string]bool{"agent1": true} // Cached from when project existed

		deleteEvents, updateEvents, finalMapping := executeTest(t, mockBackend, oldSecret, newSecret, existingMapping)

		// Cached agent1 gets delete, new agent2 gets update
		assert.ElementsMatch(t, []string{"agent1"}, deleteEvents)
		assert.ElementsMatch(t, []string{"agent2"}, updateEvents)
		assert.Equal(t, map[string]bool{"agent2": true}, finalMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("new project lookup fails", func(t *testing.T) {
		// Can't fetch new project - only cleanup happens, no new agents get the repo
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "old-project", "argocd").Return(createProject("old-project", "agent1"), nil)
		mockBackend.On("Get", mock.Anything, "bad-project", "argocd").Return(nil, assert.AnError)

		oldSecret := createSecret("old-project")
		newSecret := createSecret("bad-project")
		existingMapping := map[string]bool{"agent1": true}

		deleteEvents, updateEvents, finalMapping := executeTest(t, mockBackend, oldSecret, newSecret, existingMapping)

		// Only cleanup happens
		assert.ElementsMatch(t, []string{"agent1"}, deleteEvents)
		assert.Empty(t, updateEvents)
		assert.Empty(t, finalMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("old secret missing project field", func(t *testing.T) {
		// Malformed old secret - function should return early
		s := &Server{events: event.NewEventSource("test"), queues: queue.NewSendRecvQueues()}
		s.queues.Create("agent1")

		oldSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "argocd"},
			Data:       map[string][]byte{"url": []byte("https://example.com/repo.git")}, // No project field
		}
		newSecret := createSecret("new-project")
		logCtx := logrus.WithField("test", "missing-project")

		s.syncRepositoryUpdatesToAgents(context.Background(), oldSecret, newSecret, logCtx)

		// No events should be sent
		q := s.queues.SendQ("agent1")
		assert.Equal(t, 0, q.Len())
	})

	t.Run("new secret missing project field", func(t *testing.T) {
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "old-project", "argocd").Return(createProject("old-project", "agent1"), nil)

		projectManager, _ := appproject.NewAppProjectManager(mockBackend, "argocd")
		s := &Server{
			events:         event.NewEventSource("test"),
			queues:         queue.NewSendRecvQueues(),
			projectManager: projectManager,
			repoToAgents:   NewMapToSet(),
			projectToRepos: NewMapToSet(),
		}
		s.queues.Create("agent1")

		oldSecret := createSecret("old-project")

		// Malformed new secret - function should return early
		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "argocd"},
			Data:       map[string][]byte{"url": []byte("https://example.com/repo.git")}, // No project field
		}
		logCtx := logrus.WithField("test", "missing-project")

		s.syncRepositoryUpdatesToAgents(context.Background(), oldSecret, newSecret, logCtx)

		// No events should be sent
		q := s.queues.SendQ("agent1")
		assert.Equal(t, 0, q.Len())
	})
}

func TestServer_deleteRepositoryCallback(t *testing.T) {
	// Helper to create a secret with project
	createSecret := func(name, project string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "argocd"},
			Data:       map[string][]byte{"project": []byte(project), "url": []byte("https://example.com/repo.git")},
		}
	}

	// Helper to create a project with agent destinations
	createProject := func(name string, agents ...string) *v1alpha1.AppProject {
		var destinations []v1alpha1.ApplicationDestination
		for _, agent := range agents {
			destinations = append(destinations, v1alpha1.ApplicationDestination{Name: agent})
		}
		return &v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "argocd"},
			Spec:       v1alpha1.AppProjectSpec{Destinations: destinations, SourceNamespaces: agents},
		}
	}

	// Helper to setup server and execute function
	executeTest := func(t *testing.T, mockBackend *mocks.AppProject, secret *corev1.Secret, existingRepoMapping, existingProjectMapping map[string]bool) (deleteEvents []string, finalRepoMapping map[string]bool, finalProjectMapping map[string]bool) {
		projectManager, _ := appproject.NewAppProjectManager(mockBackend, "argocd")
		s := &Server{
			ctx:            context.Background(),
			queues:         queue.NewSendRecvQueues(),
			events:         event.NewEventSource("test"),
			projectManager: projectManager,
			repoToAgents:   NewMapToSet(),
			projectToRepos: NewMapToSet(),
			resources:      resources.NewAgentResources(),
			namespaceMap:   map[string]types.AgentMode{"agent1": types.AgentModeManaged, "agent2": types.AgentModeManaged},
		}

		// Setup existing mappings
		for agent := range existingRepoMapping {
			s.repoToAgents.Add(secret.Name, agent)
		}
		for repo := range existingProjectMapping {
			s.projectToRepos.Add(string(secret.Data["project"]), repo)
		}

		// Setup queues
		s.queues.Create("agent1")
		s.queues.Create("agent2")

		// Execute function
		s.deleteRepositoryCallback(secret)

		// Collect events
		for _, agent := range []string{"agent1", "agent2"} {
			if q := s.queues.SendQ(agent); q != nil {
				for q.Len() > 0 {
					ev, _ := q.Get()
					if ev.Type() == event.Delete.String() {
						deleteEvents = append(deleteEvents, agent)
					}
					q.Done(ev)
				}
			}
		}

		return deleteEvents, s.repoToAgents.Get(secret.Name), s.projectToRepos.Get(string(secret.Data["project"]))
	}

	t.Run("deletes repository from active project", func(t *testing.T) {
		// Repository exists in active project - should send delete events to project's agents
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "active-project", "argocd").Return(createProject("active-project", "agent1", "agent2"), nil)

		secret := createSecret("test-repo", "active-project")
		existingRepoMapping := map[string]bool{"agent1": true, "agent2": true}
		existingProjectMapping := map[string]bool{"test-repo": true}

		deleteEvents, finalRepoMapping, finalProjectMapping := executeTest(t, mockBackend, secret, existingRepoMapping, existingProjectMapping)

		// Both agents should get delete events
		assert.ElementsMatch(t, []string{"agent1", "agent2"}, deleteEvents)
		// Repository should be removed from all mappings
		assert.Empty(t, finalRepoMapping)
		assert.Empty(t, finalProjectMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("deletes repository using cached mapping when project deleted", func(t *testing.T) {
		// Project no longer exists - should use cached repo-to-agents mapping
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "deleted-project", "argocd").Return(nil, errors.NewNotFound(schema.GroupResource{}, "deleted-project"))

		secret := createSecret("test-repo", "deleted-project")
		existingRepoMapping := map[string]bool{"agent1": true} // Cached from when project existed
		existingProjectMapping := map[string]bool{"test-repo": true}

		deleteEvents, finalRepoMapping, finalProjectMapping := executeTest(t, mockBackend, secret, existingRepoMapping, existingProjectMapping)

		// Cached agent should get delete event
		assert.ElementsMatch(t, []string{"agent1"}, deleteEvents)
		// Repository should be removed from all mappings
		assert.Empty(t, finalRepoMapping)
		assert.Empty(t, finalProjectMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("handles project lookup failure", func(t *testing.T) {
		// Project lookup fails with non-NotFound error - should return early
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "bad-project", "argocd").Return(nil, assert.AnError)

		secret := createSecret("test-repo", "bad-project")
		existingRepoMapping := map[string]bool{"agent1": true}
		existingProjectMapping := map[string]bool{"test-repo": true}

		deleteEvents, finalRepoMapping, finalProjectMapping := executeTest(t, mockBackend, secret, existingRepoMapping, existingProjectMapping)

		// No events should be sent due to error
		assert.Empty(t, deleteEvents)
		// Mappings should remain unchanged
		assert.Equal(t, map[string]bool{"agent1": true}, finalRepoMapping)
		assert.Equal(t, map[string]bool{"test-repo": true}, finalProjectMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("handles no matching agents", func(t *testing.T) {
		// Project exists but has no matching agents
		mockBackend := &mocks.AppProject{}
		mockBackend.On("Get", mock.Anything, "empty-project", "argocd").Return(createProject("empty-project"), nil) // No agents

		secret := createSecret("test-repo", "empty-project")
		existingRepoMapping := map[string]bool{} // No existing mapping
		existingProjectMapping := map[string]bool{"test-repo": true}

		deleteEvents, _, finalProjectMapping := executeTest(t, mockBackend, secret, existingRepoMapping, existingProjectMapping)

		// No events should be sent
		assert.Empty(t, deleteEvents)
		// Project mapping should still be cleaned up
		assert.Empty(t, finalProjectMapping)
		mockBackend.AssertExpectations(t)
	})

	t.Run("handles missing project field", func(t *testing.T) {
		// Repository secret missing project field - should return early
		s := &Server{
			events:         event.NewEventSource("test"),
			queues:         queue.NewSendRecvQueues(),
			repoToAgents:   NewMapToSet(),
			projectToRepos: NewMapToSet(),
		}
		s.queues.Create("agent1")

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-repo", Namespace: "argocd"},
			Data:       map[string][]byte{"url": []byte("https://example.com/repo.git")}, // No project field
		}

		// Setup some existing state that should remain unchanged
		s.repoToAgents.Add("test-repo", "agent1")
		s.projectToRepos.Add("some-project", "test-repo")

		s.deleteRepositoryCallback(secret)

		// No events should be sent
		q := s.queues.SendQ("agent1")
		assert.Equal(t, 0, q.Len())
		// State should remain unchanged
		assert.Equal(t, map[string]bool{"agent1": true}, s.repoToAgents.Get("test-repo"))
		assert.Equal(t, map[string]bool{"test-repo": true}, s.projectToRepos.Get("some-project"))
	})
}

func TestServer_deleteAppCallback_AutonomousAgent(t *testing.T) {
	tests := []struct {
		name           string
		app            *v1alpha1.Application
		shouldRecreate bool // Whether app should be recreated (unauthorized deletion)
		shouldError    bool // Whether recreation should fail
		setupMocks     func(*mocks.Application)
	}{
		{
			name: "legitimate deletion from autonomous agent - should allow",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "autonomous-agent",
					UID:       "app-uid-123",
					Annotations: map[string]string{
						manager.SourceUIDAnnotation: "source-uid-456",
					},
				},
				Spec: v1alpha1.ApplicationSpec{
					Project: "default",
				},
			},
			shouldRecreate: false,
			shouldError:    false,
			setupMocks: func(mockBackend *mocks.Application) {
				// No mocks needed - deletion should proceed normally
			},
		},
		{
			name: "unauthorized deletion by user - should recreate",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "autonomous-agent",
					UID:       "app-uid-123",
					Annotations: map[string]string{
						manager.SourceUIDAnnotation: "source-uid-456",
					},
				},
				Spec: v1alpha1.ApplicationSpec{
					Project: "default",
				},
			},
			shouldRecreate: true,
			shouldError:    false,
			setupMocks: func(mockBackend *mocks.Application) {
				// Mock successful recreation
				mockBackend.On("Create", mock.Anything, mock.MatchedBy(func(app *v1alpha1.Application) bool {
					return app.Name == "test-app" &&
						app.Namespace == "autonomous-agent" &&
						app.ResourceVersion == "" &&
						app.DeletionTimestamp == nil
				})).Return(&v1alpha1.Application{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-app",
						Namespace: "autonomous-agent",
						UID:       "new-uid-789",
					},
				}, nil)
			},
		},
		{
			name: "unauthorized deletion with recreation failure",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "autonomous-agent",
					UID:       "app-uid-123",
					Annotations: map[string]string{
						manager.SourceUIDAnnotation: "source-uid-456",
					},
				},
				Spec: v1alpha1.ApplicationSpec{
					Project: "default",
				},
			},
			shouldRecreate: true,
			shouldError:    true,
			setupMocks: func(mockBackend *mocks.Application) {
				// Mock recreation failure
				mockBackend.On("Create", mock.Anything, mock.Anything).Return(nil, errors.NewInternalError(fmt.Errorf("creation failed")))
			},
		},
		{
			name: "non-autonomous agent app - should not affect normal flow",
			app: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "managed-agent",
					UID:       "app-uid-123",
					// No SourceUIDAnnotation - not from autonomous agent
				},
				Spec: v1alpha1.ApplicationSpec{
					Project: "default",
				},
			},
			shouldRecreate: false,
			shouldError:    false,
			setupMocks: func(mockBackend *mocks.Application) {
				// No mocks needed - should follow normal deletion flow
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			mockAppBackend := &mocks.Application{}
			tt.setupMocks(mockAppBackend)

			// Create application manager with mock backend
			appManager, err := application.NewApplicationManager(mockAppBackend, "argocd")
			require.NoError(t, err)

			// Create server with dependencies
			s := &Server{
				ctx:         context.Background(),
				queues:      queue.NewSendRecvQueues(),
				events:      event.NewEventSource("test"),
				resources:   resources.NewAgentResources(),
				appManager:  appManager,
				sourceCache: cache.NewSourceCache(),
				deletions:   manager.NewDeletionTracker(),
			}

			// Create send queue for the agent
			err = s.queues.Create(tt.app.Namespace)
			require.NoError(t, err)

			sourceUID := tt.app.Annotations[manager.SourceUIDAnnotation]
			if tt.shouldRecreate {
				s.deletions.Unmark(k8stypes.UID(sourceUID))
			} else {
				s.deletions.MarkExpected(k8stypes.UID(sourceUID))
			}

			// Execute the callback
			s.deleteAppCallback(tt.app)

			// Verify behavior
			if tt.shouldRecreate {
				// If recreation was expected, verify the Create call was made
				mockAppBackend.AssertExpectations(t)
			}

			// Verify queue state
			sendQ := s.queues.SendQ(tt.app.Namespace)
			if tt.shouldRecreate {
				// If we recreated, callback should have returned early - no events in queue
				assert.Equal(t, 0, sendQ.Len(), "Queue should be empty when recreation happens")
			} else if !isResourceFromAutonomousAgent(tt.app) {
				// If not autonomous agent app, normal deletion flow should add event to queue
				assert.Equal(t, 1, sendQ.Len(), "Queue should contain delete event for normal apps")
			}
		})
	}
}

func TestServer_newAppCallback(t *testing.T) {
	t.Run("sends create event to queue using namespace-based mapping", func(t *testing.T) {
		s := &Server{
			ctx:        context.Background(),
			queues:     queue.NewSendRecvQueues(),
			events:     event.NewEventSource("test"),
			resources:  resources.NewAgentResources(),
			appToAgent: NewMapToSet(),
		}

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-app",
				Namespace: "managed-agent",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
			},
		}

		s.newAppCallback(app)

		// Verify event was added to queue
		sendQ := s.queues.SendQ("managed-agent")
		require.NotNil(t, sendQ)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		require.False(t, shutdown)
		assert.Equal(t, event.Create.String(), ev.Type())

		// Verify event contains the app
		receivedApp := &v1alpha1.Application{}
		err := json.Unmarshal(ev.Data(), receivedApp)
		require.NoError(t, err)
		assert.Equal(t, app.Name, receivedApp.Name)
		assert.Equal(t, app.Namespace, receivedApp.Namespace)
		sendQ.Done(ev)

		// Verify resource tracking
		agentResources := s.resources.GetAllResources("managed-agent")
		assert.Len(t, agentResources, 1)
		assert.Equal(t, app.Name, agentResources[0].Name)

		assert.Empty(t, s.appToAgent.Get("managed-agent/test-app"))
	})

	t.Run("sends create event using destination-based mapping", func(t *testing.T) {
		s := &Server{
			ctx:                     context.Background(),
			queues:                  queue.NewSendRecvQueues(),
			events:                  event.NewEventSource("test"),
			resources:               resources.NewAgentResources(),
			appToAgent:              NewMapToSet(),
			destinationBasedMapping: true,
		}

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-app",
				Namespace: "argocd", // Different from agent name
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
				Destination: v1alpha1.ApplicationDestination{
					Name: "target-cluster", // This should be used as agent name
				},
			},
		}

		s.newAppCallback(app)

		// Verify event was added to the correct queue (target-cluster, not argocd)
		sendQ := s.queues.SendQ("target-cluster")
		require.NotNil(t, sendQ)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		require.False(t, shutdown)
		assert.Equal(t, event.Create.String(), ev.Type())
		sendQ.Done(ev)

		// Verify app-to-agent mapping was tracked
		agents := s.appToAgent.Get("argocd/test-app")
		assert.True(t, agents["target-cluster"])

		// Verify resource tracking uses destination name
		agentResources := s.resources.GetAllResources("target-cluster")
		assert.Len(t, agentResources, 1)
		assert.Equal(t, app.Name, agentResources[0].Name)
	})

	t.Run("destination.name is empty, should not process the app", func(t *testing.T) {
		s := &Server{
			ctx:                     context.Background(),
			queues:                  queue.NewSendRecvQueues(),
			events:                  event.NewEventSource("test"),
			resources:               resources.NewAgentResources(),
			appToAgent:              NewMapToSet(),
			destinationBasedMapping: true,
		}

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-app",
				Namespace: "fallback-agent",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
				Destination: v1alpha1.ApplicationDestination{
					Name: "", // Empty - should fall back to namespace
				},
			},
		}

		s.newAppCallback(app)

		// Should not process the app
		assert.Zero(t, s.queues.Len())
	})
}

func TestServer_updateAppCallback(t *testing.T) {
	t.Run("managed agent update sends event to queue", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"managed-agent": types.AgentModeManaged},
			appManager:   appManager,
		}

		err = s.queues.Create("managed-agent")
		require.NoError(t, err)

		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "1"},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "2"},
		}

		s.updateAppCallback(oldApp, newApp)

		sendQ := s.queues.SendQ("managed-agent")
		assert.Equal(t, 1, sendQ.Len())

		if sendQ.Len() > 0 {
			ev, _ := sendQ.Get()
			assert.Equal(t, event.SpecUpdate.String(), ev.Type())
			app := &v1alpha1.Application{}
			b := ev.Data()
			err := json.Unmarshal(b, app)
			require.NoError(t, err)
			assert.Equal(t, newApp, app)
			sendQ.Done(ev)
		}
	})

	t.Run("resource version already marked as ignored", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"managed-agent": types.AgentModeManaged},
			appManager:   appManager,
		}

		err = s.queues.Create("managed-agent")
		require.NoError(t, err)

		app := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "1"},
		}

		err = s.appManager.IgnoreChange("managed-agent/test-app", "1")
		require.NoError(t, err)

		s.updateAppCallback(app, app)
		sendQ := s.queues.SendQ("managed-agent")
		assert.Equal(t, 0, sendQ.Len(), "Call with ignored version should not queue events")
	})

	t.Run("autonomous agent without cache entry processes normally", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"autonomous-agent": types.AgentModeAutonomous},
			appManager:   appManager,
			sourceCache:  cache.NewSourceCache(),
		}

		err = s.queues.Create("autonomous-agent")
		require.NoError(t, err)

		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "1",
				Annotations: map[string]string{manager.SourceUIDAnnotation: "uid-123"},
			},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "2",
				Annotations: map[string]string{manager.SourceUIDAnnotation: "uid-123"},
			},
		}

		s.updateAppCallback(oldApp, newApp)

		sendQ := s.queues.SendQ("autonomous-agent")
		assert.Equal(t, 1, sendQ.Len())
	})

	t.Run("updates to autonomous apps are reverted", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd", application.WithRole(manager.ManagerRolePrincipal))
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"autonomous-agent": types.AgentModeAutonomous},
			appManager:   appManager,
			sourceCache:  cache.NewSourceCache(),
		}

		err = s.queues.Create("autonomous-agent")
		require.NoError(t, err)

		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "1",
				Annotations: map[string]string{manager.SourceUIDAnnotation: "uid-123"},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "default",
			},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "2",
				Annotations: map[string]string{manager.SourceUIDAnnotation: "uid-123"},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "random-project",
			},
		}

		sourceUID := k8stypes.UID(oldApp.Annotations[manager.SourceUIDAnnotation])
		s.sourceCache.Application.Set(sourceUID, oldApp.Spec)
		defer s.sourceCache.Application.Delete(sourceUID)

		mockBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldApp, nil)
		mockBackend.On("SupportsPatch").Return(true)
		mockBackend.On("Patch", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(oldApp, nil)

		s.updateAppCallback(oldApp, newApp)

		sendQ := s.queues.SendQ("autonomous-agent")
		assert.Equal(t, 0, sendQ.Len())
	})

	t.Run("autonomous agent finalizer removal during deletion", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"autonomous-agent": types.AgentModeAutonomous},
			appManager:   appManager,
			sourceCache:  cache.NewSourceCache(),
		}

		err = s.queues.Create("autonomous-agent")
		require.NoError(t, err)

		deletionTime := metav1.Now()
		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "1",
				Annotations:       map[string]string{manager.SourceUIDAnnotation: "uid-123"},
				DeletionTimestamp: &deletionTime,
				Finalizers:        []string{"resources-finalizer.argocd.argoproj.io"},
			},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-app", Namespace: "autonomous-agent", ResourceVersion: "2",
				Annotations:       map[string]string{manager.SourceUIDAnnotation: "uid-123"},
				DeletionTimestamp: &deletionTime,
				Finalizers:        []string{"resources-finalizer.argocd.argoproj.io"},
			},
		}

		appWithoutFinalizers := newApp.DeepCopy()
		appWithoutFinalizers.Finalizers = nil

		got := &v1alpha1.Application{}
		mockBackend.On("Get", mock.Anything, "test-app", "autonomous-agent").Return(newApp, nil)
		mockBackend.On("SupportsPatch").Return(false)
		mockBackend.On("Update", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			got = args.Get(1).(*v1alpha1.Application)
		}).Return(appWithoutFinalizers, nil)

		s.updateAppCallback(oldApp, newApp)

		assert.Equal(t, appWithoutFinalizers, got)
		sendQ := s.queues.SendQ("autonomous-agent")
		assert.Equal(t, 1, sendQ.Len())
	})

	t.Run("include operation in event if it is initiated for the first time", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"managed-agent": types.AgentModeManaged},
			appManager:   appManager,
		}

		err = s.queues.Create("managed-agent")
		require.NoError(t, err)

		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "1"},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "2"},
			Operation: &v1alpha1.Operation{
				Sync: &v1alpha1.SyncOperation{
					Revision: "HEAD",
				},
			},
		}

		s.updateAppCallback(oldApp, newApp)

		sendQ := s.queues.SendQ("managed-agent")
		assert.Equal(t, 1, sendQ.Len())

		ev, _ := sendQ.Get()
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())
		app := &v1alpha1.Application{}
		b := ev.Data()
		err = json.Unmarshal(b, app)
		require.NoError(t, err)
		require.NotNil(t, app.Operation)
		assert.Equal(t, newApp.Operation, app.Operation)
		sendQ.Done(ev)

		// Operation should be set to nil for subsequent events
		oldApp = newApp.DeepCopy()
		s.updateAppCallback(oldApp, newApp)

		assert.Equal(t, 1, sendQ.Len())
		ev, _ = sendQ.Get()
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())
		app = &v1alpha1.Application{}
		b = ev.Data()
		err = json.Unmarshal(b, app)
		require.NoError(t, err)
		fmt.Println(app.Operation.String())
		require.Nil(t, app.Operation)
		sendQ.Done(ev)
	})

	t.Run("send terminate operation event if the operation is terminating", func(t *testing.T) {
		mockBackend := &mocks.Application{}

		appManager, err := application.NewApplicationManager(mockBackend, "argocd")
		require.NoError(t, err)

		s := &Server{
			ctx:          context.Background(),
			queues:       queue.NewSendRecvQueues(),
			events:       event.NewEventSource("test"),
			namespaceMap: map[string]types.AgentMode{"managed-agent": types.AgentModeManaged},
			appManager:   appManager,
		}

		err = s.queues.Create("managed-agent")
		require.NoError(t, err)

		oldApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "1"},
			Status: v1alpha1.ApplicationStatus{
				OperationState: &v1alpha1.OperationState{
					Phase: synccommon.OperationRunning,
				},
			},
		}
		newApp := &v1alpha1.Application{
			ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "managed-agent", ResourceVersion: "2"},
			Status: v1alpha1.ApplicationStatus{
				OperationState: &v1alpha1.OperationState{
					Phase: synccommon.OperationTerminating,
				},
			},
		}

		s.updateAppCallback(oldApp, newApp)

		sendQ := s.queues.SendQ("managed-agent")
		assert.Equal(t, 1, sendQ.Len())

		if sendQ.Len() > 0 {
			ev, _ := sendQ.Get()
			assert.Equal(t, event.TerminateOperation.String(), ev.Type())
			sendQ.Done(ev)
		}
	})
}
