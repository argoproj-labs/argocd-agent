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
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
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
		{
			name: "no agent name but server is wildcard",
			appProject: v1alpha1.AppProject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-project",
					Namespace: "argocd",
				},
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Server:    "*",
							Namespace: "default",
						},
					},
					SourceNamespaces: []string{"*"},
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
					Destinations: []v1alpha1.ApplicationDestination{
						{
							Name:      "in-cluster",
							Server:    "https://kubernetes.default.svc",
							Namespace: "default",
						},
					},
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

func TestDoesAgentMatchWithProject(t *testing.T) {
	tests := []struct {
		name       string
		agentName  string
		appProject v1alpha1.AppProject
		want       bool
	}{
		{
			name:      "agent matches destination name",
			agentName: "agent-prod",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-prod", Namespace: "default"},
					},
					SourceNamespaces: []string{"agent-prod"},
				},
			},
			want: true,
		},
		{
			name:      "empty destination name with wildcard server",
			agentName: "any-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "*", Namespace: "default"},
					},
					SourceNamespaces: []string{"any-agent"},
				},
			},
			want: true,
		},
		{
			name:      "wildcard server with a different name",
			agentName: "any-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "different-name", Server: "*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true, // Should match wildcard server
		},
		{
			name:      "agent matches destination name with wildcard",
			agentName: "agent-staging",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"agent-*"},
				},
			},
			want: true,
		},
		{
			name:      "deny pattern matches agent - should reject",
			agentName: "prod-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false,
		},
		{
			name:      "deny pattern with positive pattern - deny takes precedence",
			agentName: "prod-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "*", Namespace: "default"},       // Allow all
						{Name: "!prod-*", Namespace: "default"}, // Deny prod
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false,
		},
		{
			name:      "deny pattern with positive pattern - allow others",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "*", Namespace: "default"},       // Allow all
						{Name: "!prod-*", Namespace: "default"}, // Deny prod
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true,
		},
		{
			name:      "order independence - deny first, then allow",
			agentName: "prod-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"}, // Deny prod first
						{Name: "*", Namespace: "default"},       // Allow all
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // Same result regardless of order
		},
		{
			name:      "agent matches destination but not source namespace",
			agentName: "agent-test",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "agent-test", Namespace: "default"},
					},
					SourceNamespaces: []string{"different-namespace"},
				},
			},
			want: false,
		},
		{
			name:      "agent matches neither destination name nor server",
			agentName: "agent-nomatch",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "different-name", Server: "different-server", Namespace: "default"},
					},
					SourceNamespaces: []string{"agent-nomatch"},
				},
			},
			want: false,
		},
		{
			name:      "empty destination name with non-wildcard server",
			agentName: "any-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "https://kubernetes.default.svc", Namespace: "default"},
					},
					SourceNamespaces: []string{"any-agent"},
				},
			},
			want: false,
		},
		{
			name:      "multiple deny patterns - first match wins",
			agentName: "prod-sensitive-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},        // This matches first
						{Name: "!*-sensitive-*", Namespace: "default"}, // This would also match
						{Name: "*", Namespace: "default"},              // Allow all others
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false,
		},
		{
			name:      "wildcard with deny pattern override",
			agentName: "admin-user",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "", Server: "*", Namespace: "default"}, // Wildcard allow
						{Name: "!admin-*", Namespace: "default"},      // Deny admin
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // Deny overrides wildcard
		},
		{
			name:      "only deny patterns - no positive match",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // No positive pattern to match
		},
		{
			name:      "deny pattern doesn't match - should continue to positive patterns",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"}, // Deny doesn't match
						{Name: "dev-*", Namespace: "default"},   // Positive matches
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true, // Should match positive pattern after deny doesn't match
		},
		{
			name:      "multiple deny patterns none match - positive pattern wins",
			agentName: "test-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-*", Namespace: "default"},    // Doesn't match
						{Name: "!staging-*", Namespace: "default"}, // Doesn't match
						{Name: "*", Namespace: "default"},          // Matches
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true,
		},
		{
			name:      "mixed destinations with deny pattern",
			agentName: "dev-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!admin-*", Namespace: "default"},      // Name deny (doesn't match)
						{Name: "", Server: "*", Namespace: "default"}, // Server wildcard
						{Name: "dev-*", Namespace: "default"},         // Name allow (matches)
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: true, // Should pass deny check and match positive patterns
		},

		{
			name:      "deny pattern in first destination, positive in second",
			agentName: "prod-cluster",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!prod-cluster", Namespace: "default"}, // Exact deny match
						{Name: "*", Namespace: "default"},             // Would allow all
					},
					SourceNamespaces: []string{"*"},
				},
			},
			want: false, // Deny pattern matches exactly, should reject immediately
		},
		{
			name:      "deny pattern with different namespace requirements",
			agentName: "restricted-agent",
			appProject: v1alpha1.AppProject{
				Spec: v1alpha1.AppProjectSpec{
					Destinations: []v1alpha1.ApplicationDestination{
						{Name: "!restricted-*", Namespace: "default"}, // Deny pattern matches
					},
					SourceNamespaces: []string{"restricted-agent"}, // Namespace would match
				},
			},
			want: false, // Should be denied even though namespace matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := doesAgentMatchWithProject(tt.agentName, tt.appProject)
			assert.Equal(t, tt.want, got)
		})
	}
}
