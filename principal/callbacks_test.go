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
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			s.syncAppProjectUpdatesToAgents(&tt.oldAppProject, &tt.newAppProject, logCtx)

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

func TestAgentSpecificAppProject(t *testing.T) {
	tests := []struct {
		name       string
		appProject v1alpha1.AppProject
		agent      string
		want       v1alpha1.AppProject
	}{
		{
			name: "filters destinations for matching agent",
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "cluster-prod",
							Namespace: "default",
							Server:    "https://prod-cluster.example.com",
						},
						{
							Name:      "cluster-dev",
							Namespace: "staging",
							Server:    "https://dev-cluster.example.com",
						},
					},
					SourceNamespaces: []string{"argocd", "apps"},
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "admin",
							Policies: []string{
								"p, proj:test-project:admin, applications, *, test-project/*, allow",
							},
						},
					},
				},
			},
			agent: "cluster-prod",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "default",
							Server:    "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: []string{"cluster-prod"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "matches multiple destinations with glob pattern",
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-dest-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "prod-cluster-1",
							Namespace: "app1",
							Server:    "https://prod1.example.com",
						},
						{
							Name:      "prod-cluster-1",
							Namespace: "app2",
							Server:    "https://prod2.example.com",
						},
						{
							Name:      "dev-cluster",
							Namespace: "app3",
							Server:    "https://dev.example.com",
						},
					},
					SourceNamespaces: []string{"*"},
					Roles: []v1alpha1.ProjectRole{
						{Name: "viewer"},
						{Name: "admin"},
					},
				},
			},
			agent: "prod-cluster-1",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-dest-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Namespace: "app1",
							Server:    "https://kubernetes.default.svc",
						},
						{
							Name:      "in-cluster",
							Namespace: "app2",
							Server:    "https://kubernetes.default.svc",
						},
					},
					SourceNamespaces: []string{"prod-cluster-1"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "no matching destinations",
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-match-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "staging-cluster",
							Namespace: "default",
							Server:    "https://staging.example.com",
						},
						{
							Name:      "test-cluster",
							Namespace: "testing",
							Server:    "https://test.example.com",
						},
					},
					SourceNamespaces: []string{"argocd", "staging"},
					Roles: []v1alpha1.ProjectRole{
						{
							Name: "operator",
							Policies: []string{
								"p, proj:no-match-project:operator, applications, get, *, allow",
							},
						},
					},
				},
			},
			agent: "prod-cluster",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-match-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: []string{"prod-cluster"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
		{
			name: "empty destinations and roles",
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: []string{"original-namespace"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
			agent: "test-agent",
			want: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations:     []v1alpha1.ApplicationDestination{},
					SourceNamespaces: []string{"test-agent"},
					Roles:            []v1alpha1.ProjectRole{},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentSpecificAppProject(tt.appProject, tt.agent)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsAppProjectFromAutonomousAgent(t *testing.T) {
	tests := []struct {
		name         string
		projectName  string
		namespaceMap map[string]types.AgentMode
		want         bool
	}{
		{
			name:        "project matches autonomous agent prefix",
			projectName: "agent1-myproject",
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeAutonomous,
				"agent2": types.AgentModeManaged,
				"agent3": types.AgentModeAutonomous,
			},
			want: true,
		},
		{
			name:        "project doesn't match any autonomous agent prefix",
			projectName: "someproject",
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeAutonomous,
				"agent2": types.AgentModeManaged,
			},
			want: false,
		},
		{
			name:        "project matches managed agent prefix (not autonomous)",
			projectName: "agent2-project",
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeAutonomous,
				"agent2": types.AgentModeManaged,
			},
			want: false,
		},
		{
			name:        "multiple dashes in project name",
			projectName: "agent1-my-complex-project-name",
			namespaceMap: map[string]types.AgentMode{
				"agent1": types.AgentModeAutonomous,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{namespaceMap: tt.namespaceMap}
			got := s.isAppProjectFromAutonomousAgent(tt.projectName)
			assert.Equal(t, tt.want, got)
		})
	}
}
