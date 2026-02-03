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
	"fmt"
	"sync"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/internal/tracing"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	synccommon "github.com/argoproj/gitops-engine/pkg/sync/common"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Informer operation types
	operationcreate = "create"
	operationupdate = "update"
	operationdelete = "delete"
)

// newAppCallback is executed when a new application event was emitted from
// the informer and needs to be sent out to an agent. If the receiving agent
// is in autonomous mode, this event will be discarded.
func (s *Server) newAppCallback(outbound *v1alpha1.Application) {
	ctx, span := s.startSpan(operationcreate, "Application", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"event":            "application_new",
		"application_name": outbound.Name,
	})

	agentName := s.getAgentNameForApp(outbound)
	if agentName == "" {
		logCtx.Error("Failed to get agent name for application")
		return
	}

	logCtx = logCtx.WithField("queue", agentName)

	s.resources.Add(agentName, resources.NewResourceKeyFromApp(outbound))
	// Track the app-to-agent mapping for redis proxy routing
	s.trackAppToAgent(outbound, agentName)

	if !s.queues.HasQueuePair(agentName) {
		if err := s.queues.Create(agentName); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for agent")
			return
		}
		logCtx.Trace("Created a new queue pair for the agent")
	}
	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Errorf("Help! queue pair for agent %s disappeared!", agentName)
		return
	}
	ev := s.events.ApplicationEvent(event.Create, outbound)
	// Inject trace context into the event for propagation to agent
	tracing.InjectTraceContext(ctx, ev)
	q.Add(ev)
	logCtx.Tracef("Added app %s to send queue, total length now %d", outbound.QualifiedName(), q.Len())

	if s.metrics != nil {
		s.metrics.ApplicationCreated.Inc()
	}
}

func (s *Server) updateAppCallback(old *v1alpha1.Application, new *v1alpha1.Application) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()

	ctx, span := s.startSpan(operationupdate, "Application", old)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"event":            "application_update",
		"application_name": old.Name,
	})

	agentName := s.getAgentNameForApp(new)
	if agentName == "" {
		logCtx.Error("Failed to get agent name for application")
		return
	}

	logCtx = logCtx.WithField("queue", agentName)

	if s.destinationBasedMapping {
		s.handleAppAgentChange(ctx, old, new, logCtx)
	}

	if isResourceFromAutonomousAgent(new) {
		// Remove finalizers from autonomous agent applications if it is being deleted
		if new.DeletionTimestamp != nil && len(new.Finalizers) > 0 {
			updated, err := s.appManager.RemoveFinalizers(s.ctx, new)
			if err != nil {
				logCtx.WithError(err).Error("Failed to remove finalizers from autonomous application")
			} else {
				logCtx.Debug("Removed finalizers from autonomous application to allow deletion")
				new = updated
			}
		}

		// Revert modifications on autonomous agent applications
		if s.appManager.RevertAutonomousAppChanges(s.ctx, new, s.sourceCache.Application) {
			logCtx.Trace("Modifications to the application are reverted")
			return
		}
	}

	if s.appManager.IsChangeIgnored(new.QualifiedName(), new.ResourceVersion) {
		logCtx.WithField("resource_version", new.ResourceVersion).Debugf("Resource version has already been seen")
		return
	}
	if !s.queues.HasQueuePair(agentName) {
		if err := s.queues.Create(agentName); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for agent")
			return
		}
		logCtx.Trace("Created a new queue pair for the agent")
	}
	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}

	var ev *cloudevents.Event

	// Check if this update is a terminate-operation or a regular spec update
	if isTerminateOperation(old, new) {
		ev = s.events.ApplicationEvent(event.TerminateOperation, new)
	} else {
		// Prevent sending operation back on regular spec updates.
		// Allow only nil->non-nil transitions to carry operation (i.e. principal-initiated sync).

		// DeepCopy to avoid mutating the informer object
		out := new.DeepCopy()
		if old.Operation == nil && new.Operation != nil {
			out.Operation = new.Operation.DeepCopy()
		} else {
			out.Operation = nil
		}

		ev = s.events.ApplicationEvent(event.SpecUpdate, out)
	}

	// Inject trace context into the event for propagation to agent
	tracing.InjectTraceContext(ctx, ev)
	q.Add(ev)
	logCtx.WithField("event_type", ev.Type()).Tracef("Added app to send queue, total length now %d", q.Len())

	if s.metrics != nil {
		s.metrics.ApplicationUpdated.Inc()
	}
}

func (s *Server) deleteAppCallback(outbound *v1alpha1.Application) {
	ctx, span := s.startSpan(operationdelete, "Application", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"event":            "application_delete",
		"application_name": outbound.Name,
	})

	agentName := s.getAgentNameForApp(outbound)
	if agentName == "" {
		logCtx.Error("Failed to get agent name for application")
		return
	}

	logCtx = logCtx.WithField("queue", agentName)

	// Revert user-initiated deletion on autonomous agent applications
	if isResourceFromAutonomousAgent(outbound) {
		reverted, err := manager.RevertUserInitiatedDeletion(s.ctx, outbound, s.deletions, s.appManager, logCtx)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert invalid deletion of application")
			return
		}
		if reverted {
			logCtx.Trace("Deleted application is recreated")
			return
		}
	}

	s.resources.Remove(agentName, resources.NewResourceKeyFromApp(outbound))
	// Remove the app-to-agent mapping
	s.untrackAppToAgent(outbound)

	if !s.queues.HasQueuePair(agentName) {
		if err := s.queues.Create(agentName); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for agent")
			return
		}
		logCtx.Trace("Created a new queue pair for the agent")
	}
	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.ApplicationEvent(event.Delete, outbound)
	// Inject trace context into the event for propagation to agent
	tracing.InjectTraceContext(ctx, ev)
	logCtx.WithField("event", "DeleteApp").WithField("sendq_len", q.Len()+1).Tracef("Added event to send queue")
	q.Add(ev)

	if s.metrics != nil {
		s.metrics.ApplicationDeleted.Inc()
	}
}

// newAppProjectCallback is executed when a new AppProject event was emitted from
// the informer and needs to be sent out to an agent. If the receiving agent
// is in autonomous mode, this event will be discarded.
func (s *Server) newAppProjectCallback(outbound *v1alpha1.AppProject) {
	ctx, span := s.startSpan(operationcreate, "AppProject", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           outbound.Namespace,
		"event":           "appproject_new",
		"appproject_name": outbound.Name,
	})

	// Check if this AppProject was created by an autonomous agent
	if isResourceFromAutonomousAgent(outbound) {
		// For autonomous agents, the agent name may be different from the namespace name.
		// SourceNamespaces[0] contains the exact agent name.
		if len(outbound.Spec.SourceNamespaces) > 0 {
			agentName := outbound.Spec.SourceNamespaces[0]
			s.resources.Add(agentName, resources.NewResourceKeyFromAppProject(outbound))
		}
		logCtx.Debugf("Discarding event, because the appProject is managed by an autonomous agent")
		return
	}

	s.resources.Add(outbound.Namespace, resources.NewResourceKeyFromAppProject(outbound))

	// Return early if no interested agent is connected
	if !s.queues.HasQueuePair(outbound.Namespace) {
		if err := s.queues.Create(outbound.Namespace); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for an existing agent namespace")
			return
		}
		logCtx.Trace("Created a new queue pair for the existing namespace")
	}

	if s.metrics != nil {
		s.metrics.AppProjectCreated.Inc()
	}

	agents := s.mapAppProjectToAgents(*outbound)
	for agent := range agents {
		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		agentAppProject := appproject.AgentSpecificAppProject(*outbound, agent, s.destinationBasedMapping)
		ev := s.events.AppProjectEvent(event.Create, &agentAppProject)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)
		logCtx.Tracef("Added appProject %s to send queue, total length now %d", outbound.Name, q.Len())
	}

	// Reconcile repositories that reference this project.
	s.syncRepositoriesForProject(ctx, outbound.Name, outbound.Namespace, logCtx)
}

func (s *Server) updateAppProjectCallback(old *v1alpha1.AppProject, new *v1alpha1.AppProject) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()

	ctx, span := s.startSpan(operationupdate, "AppProject", old)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           old.Namespace,
		"event":           "appproject_update",
		"appproject_name": old.Name,
	})

	// Check if this AppProject was created by an autonomous agent
	if isResourceFromAutonomousAgent(new) {
		// Revert modifications on autonomous agent appProjects
		reverted, err := s.projectManager.RevertAppProjectChanges(s.ctx, new, s.sourceCache.AppProject)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert modifications done to appProject")
			return
		}
		if reverted {
			logCtx.Debugf("Modifications done to appProject are reverted")
			return
		}
	}

	if len(new.Finalizers) > 0 && len(new.Finalizers) != len(old.Finalizers) {
		var err error
		tmp, err := s.projectManager.RemoveFinalizers(s.ctx, new)
		if err != nil {
			logCtx.WithError(err).Warnf("Could not remove finalizer")
		} else {
			logCtx.Debug("Removed finalizer")
			new = tmp
		}
	}
	if s.projectManager.IsChangeIgnored(new.Name, new.ResourceVersion) {
		logCtx.WithField("resource_version", new.ResourceVersion).Debugf("Resource version has already been seen")
		return
	}
	if !s.queues.HasQueuePair(old.Namespace) {
		if err := s.queues.Create(old.Namespace); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for an existing agent namespace")
			return
		}
		logCtx.Trace("Created a new queue pair for the existing agent namespace")
	}

	s.syncAppProjectUpdatesToAgents(ctx, old, new, logCtx)

	// The project rules could have been updated. Reconcile repositories that reference this project.
	s.syncRepositoriesForProject(ctx, new.Name, new.Namespace, logCtx)

	if s.metrics != nil {
		s.metrics.AppProjectUpdated.Inc()
	}
}

func (s *Server) deleteAppProjectCallback(outbound *v1alpha1.AppProject) {
	ctx, span := s.startSpan(operationdelete, "AppProject", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           outbound.Namespace,
		"event":           "appproject_delete",
		"appproject_name": outbound.Name,
	})

	// Revert user-initiated deletion on autonomous agent applications
	if isResourceFromAutonomousAgent(outbound) {
		reverted, err := manager.RevertUserInitiatedDeletion(s.ctx, outbound, s.deletions, s.projectManager, logCtx)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert invalid deletion of appProject")
			return
		}
		if reverted {
			logCtx.Trace("Deleted appProject is recreated")
			return
		}

		// For autonomous agents, the agent name may be different from the namespace name.
		// SourceNamespaces[0] contains the exact agent name.
		if len(outbound.Spec.SourceNamespaces) > 0 {
			agentName := outbound.Spec.SourceNamespaces[0]
			s.resources.Remove(agentName, resources.NewResourceKeyFromAppProject(outbound))
		}
		logCtx.Debugf("Discarding event, because the appProject is managed by an autonomous agent")
		return
	}

	s.resources.Remove(outbound.Namespace, resources.NewResourceKeyFromAppProject(outbound))

	if !s.queues.HasQueuePair(outbound.Namespace) {
		if err := s.queues.Create(outbound.Namespace); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for an existing agent namespace")
			return
		}
		logCtx.Trace("Created a new queue pair for the existing agent namespace")
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}

	agents := s.mapAppProjectToAgents(*outbound)
	for agent := range agents {
		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		agentAppProject := appproject.AgentSpecificAppProject(*outbound, agent, s.destinationBasedMapping)
		ev := s.events.AppProjectEvent(event.Delete, &agentAppProject)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)
		logCtx.WithField("sendq_len", q.Len()+1).Tracef("Added appProject delete event to send queue")
	}

	if s.metrics != nil {
		s.metrics.AppProjectDeleted.Inc()
	}
}

func (s *Server) newRepositoryCallback(outbound *corev1.Secret) {
	ctx, span := s.startSpan(operationcreate, "Repository", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component": "EventCallback",
		"event":     "repository_create",
		"repo_name": outbound.Name,
	})

	logCtx.Info("New repository event")

	projectName, ok := outbound.Data["project"]
	if !ok {
		logCtx.Error("Repository secret is not project scoped")
		return
	}

	s.projectToRepos.Add(string(projectName), outbound.Name)

	project, err := s.projectManager.Get(s.ctx, string(projectName), outbound.Namespace)
	if err != nil {
		logCtx.WithError(err).Error("failed to get the project referenced by the repository secret")
		return
	}

	agents := s.mapAppProjectToAgents(*project)
	if len(agents) == 0 {
		logCtx.Tracef("No matching agents found for project %s", projectName)
		return
	}

	for agent := range agents {
		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}
		s.resources.Add(agent, resources.NewResourceKeyFromRepository(outbound))

		ev := s.events.RepositoryEvent(event.Create, outbound)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)

		s.repoToAgents.Add(outbound.Name, agent)
		logCtx.Tracef("Added repository %s to send queue, total length now %d", outbound.Name, q.Len())
	}

	if s.metrics != nil {
		s.metrics.RepositoryCreated.Inc()
	}
}

func (s *Server) updateRepositoryCallback(old, new *corev1.Secret) {
	ctx, span := s.startSpan(operationupdate, "Repository", old)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component": "EventCallback",
		"event":     "repository_update",
		"repo_name": old.Name,
	})

	logCtx.Info("Update repository event")

	s.syncRepositoryUpdatesToAgents(ctx, old, new, logCtx)

	if s.metrics != nil {
		s.metrics.RepositoryUpdated.Inc()
	}
}

func (s *Server) deleteRepositoryCallback(outbound *corev1.Secret) {
	ctx, span := s.startSpan(operationdelete, "Repository", outbound)
	defer span.End()

	logCtx := log().WithFields(logrus.Fields{
		"component": "EventCallback",
		"event":     "repository_delete",
		"repo_name": outbound.Name,
	})

	logCtx.Info("Delete repository event")

	projectName, ok := outbound.Data["project"]
	if !ok {
		logCtx.Error("Repository secret is not project scoped")
		return
	}

	var agents map[string]bool

	project, err := s.projectManager.Get(s.ctx, string(projectName), outbound.Namespace)
	if err != nil {
		if !errors.IsNotFound(err) {
			logCtx.WithError(err).Error("failed to get the project referenced by the repository secret")
			return
		}

		logCtx.Tracef("Project %s is deleted. Using cached repo-agents mapping to delete repository", projectName)

		// Project is deleted. Use the cached repo-agents map to avoid orphaned repositories on the agent clusters.
		agents = s.repoToAgents.Get(outbound.Name)
	} else {
		agents = s.mapAppProjectToAgents(*project)
	}

	s.projectToRepos.Delete(string(projectName), outbound.Name)
	if len(agents) == 0 {
		logCtx.Tracef("No matching agents found for project %s", projectName)
		return
	}

	for agent := range agents {
		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		s.resources.Remove(agent, resources.NewResourceKeyFromRepository(outbound))

		ev := s.events.RepositoryEvent(event.Delete, outbound)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)

		s.repoToAgents.Delete(outbound.Name, agent)
		logCtx.WithField("sendq_len", q.Len()+1).Tracef("Added repository delete event to send queue")
	}

	if s.metrics != nil {
		s.metrics.RepositoryDeleted.Inc()
	}
}

// deleteNamespaceCallback is called when the user deletes the agent namespace.
// Since there is no namespace we can remove the queue associated with this agent.
func (s *Server) deleteNamespaceCallback(outbound *corev1.Namespace) {
	logCtx := log().WithFields(logrus.Fields{
		"component":      "EventCallback",
		"queue":          outbound.Name,
		"event":          "namespace_delete",
		"namespace_name": outbound.Name,
	})

	if !s.queues.HasQueuePair(outbound.Name) {
		return
	}

	if err := s.queues.Delete(outbound.Name, true); err != nil {
		logCtx.WithError(err).Error("failed to remove the queue pair for a deleted agent namespace")
		return
	}

	// Remove eventwriter associated with this agent
	s.eventWriters.Remove(outbound.Name)

	logCtx.Tracef("Deleted the queue pair since the agent namespace is deleted")
}

// mapAppProjectToAgents maps an AppProject to the list of managed agents that should receive it.
// We sync an AppProject from the principal to an agent if:
// 1. It is a managed agent (not autonomous)
// 2. The agent name matches one of the AppProject's destinations and source namespaces
func (s *Server) mapAppProjectToAgents(appProject v1alpha1.AppProject) map[string]bool {
	agents := map[string]bool{}
	s.clientLock.RLock()
	defer s.clientLock.RUnlock()
	for agentName, mode := range s.namespaceMap {
		if mode != types.AgentModeManaged {
			continue
		}

		if appproject.DoesAgentMatchWithProject(agentName, appProject) {
			agents[agentName] = true
		}
	}

	return agents
}

// syncAppProjectUpdatesToAgents sends the AppProject update events to the relevant clusters.
// It sends delete events to the clusters that no longer match the given AppProject.
func (s *Server) syncAppProjectUpdatesToAgents(ctx context.Context, old, new *v1alpha1.AppProject, logCtx *logrus.Entry) {
	oldAgents := s.mapAppProjectToAgents(*old)
	newAgents := s.mapAppProjectToAgents(*new)

	deletedAgents := map[string]bool{}

	// Delete the appProject from clusters that no longer match the new appProject
	for agent := range oldAgents {
		if _, ok := newAgents[agent]; ok {
			continue
		}

		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		agentAppProject := appproject.AgentSpecificAppProject(*new, agent, s.destinationBasedMapping)
		ev := s.events.AppProjectEvent(event.Delete, &agentAppProject)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)
		logCtx.Tracef("Sent a delete event for an AppProject for a removed cluster")
		deletedAgents[agent] = true
	}

	// Update the appProjects in the existing clusters
	for agent := range newAgents {
		if _, ok := deletedAgents[agent]; ok {
			continue
		}

		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		agentAppProject := appproject.AgentSpecificAppProject(*new, agent, s.destinationBasedMapping)
		ev := s.events.AppProjectEvent(event.SpecUpdate, &agentAppProject)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)
		logCtx.Tracef("Added appProject %s update event to send queue", new.Name)
	}
}

// syncRepositoryUpdatesToAgents sends the repository update events to the relevant agents.
// It sends delete events to the agents that no longer match the given project.
func (s *Server) syncRepositoryUpdatesToAgents(ctx context.Context, old, new *corev1.Secret, logCtx *logrus.Entry) {
	oldAgents := map[string]bool{}
	newAgents := map[string]bool{}

	oldProjectName, ok := old.Data["project"]
	if !ok {
		logCtx.Error("Repository secret is not project scoped")
		return
	}

	oldproject, err := s.projectManager.Get(s.ctx, string(oldProjectName), old.Namespace)
	if err != nil {
		if !errors.IsNotFound(err) {
			logCtx.WithError(err).Error("failed to get the project that was previously referenced by the repository secret")
			return
		}
	} else {
		oldAgents = s.mapAppProjectToAgents(*oldproject)
	}

	// Add the agents that were previously synced for this repository
	for agent := range s.repoToAgents.Get(old.Name) {
		oldAgents[agent] = true
	}

	newProjectName, ok := new.Data["project"]
	if !ok {
		logCtx.Error("Repository secret is not project scoped")
		return
	}

	newProject, err := s.projectManager.Get(s.ctx, string(newProjectName), new.Namespace)
	if err != nil {
		logCtx.WithError(err).Error("failed to get the project that is currently referenced by the repository secret")
	} else {
		newAgents = s.mapAppProjectToAgents(*newProject)
	}

	if oldproject != nil && newProject != nil && oldproject.Name != newProject.Name {
		// The project name has changed. Delete the repository from the old project.
		s.projectToRepos.Delete(oldproject.Name, new.Name)
		s.projectToRepos.Add(newProject.Name, new.Name)
	}

	// Delete the repository from agents that no longer match the new project
	for agent := range oldAgents {
		// If the agent is still in the newAgents map, it means the repository is still valid for the agent
		if _, ok := newAgents[agent]; ok {
			continue
		}

		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		ev := s.events.RepositoryEvent(event.Delete, new)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)

		s.repoToAgents.Delete(new.Name, agent)

		logCtx.Tracef("Sent a repository delete event for an agent that no longer matches the project")
	}

	// Update the repositories in the existing agents
	for agent := range newAgents {

		q := s.queues.SendQ(agent)
		if q == nil {
			logCtx.Errorf("Queue pair not found for agent %s", agent)
			continue
		}

		ev := s.events.RepositoryEvent(event.SpecUpdate, new)
		// Inject trace context into the event for propagation to agent
		tracing.InjectTraceContext(ctx, ev)
		q.Add(ev)

		s.repoToAgents.Add(new.Name, agent)

		logCtx.Tracef("Added repository %s update event to send queue", new.Name)
	}
}

func (s *Server) syncRepositoriesForProject(ctx context.Context, projectName, ns string, logCtx *logrus.Entry) {
	repositories := s.projectToRepos.Get(projectName)
	for repoName := range repositories {
		logCtx.Tracef("Syncing repository %s for project %s", repoName, projectName)

		repo, err := s.kubeClient.Clientset.CoreV1().Secrets(ns).Get(s.ctx, repoName, metav1.GetOptions{})
		if err != nil {
			logCtx.WithError(err).Error("failed to get the repository secret")
			continue
		}

		s.syncRepositoryUpdatesToAgents(ctx, repo, repo, logCtx)
	}
}

// isResourceFromAutonomousAgent checks if a Kubernetes resource was created by an autonomous agent
// by examining if it has the source UID annotation.
func isResourceFromAutonomousAgent(resource metav1.Object) bool {
	annotations := resource.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, ok := annotations[manager.SourceUIDAnnotation]
	return ok
}

func isTerminateOperation(old, new *v1alpha1.Application) bool {
	return old.Status.OperationState != nil &&
		old.Status.OperationState.Phase != synccommon.OperationTerminating &&
		new.Status.OperationState != nil &&
		new.Status.OperationState.Phase == synccommon.OperationTerminating
}

// getAgentNameForApp returns the agent name that should handle the given application.
// If destination-based mapping is enabled, the agent name is determined from
// spec.destination.name. Otherwise, the agent name is the application's namespace.
func (s *Server) getAgentNameForApp(app *v1alpha1.Application) string {
	if isResourceFromAutonomousAgent(app) {
		return app.Namespace
	}

	if s.destinationBasedMapping {
		// Use destination.name as the agent identifier
		return app.Spec.Destination.Name
	}
	return app.Namespace
}

// trackAppToAgent stores the mapping from application qualified name to agent name.
// This is used by the redis proxy to route requests to the correct agent when
// destination-based mapping is enabled.
func (s *Server) trackAppToAgent(app *v1alpha1.Application, agentName string) {
	if s.destinationBasedMapping && s.appToAgent != nil {
		s.appToAgent.Set(app.QualifiedName(), agentName)
	}
}

// untrackAppToAgent removes the mapping from application qualified name to agent name.
func (s *Server) untrackAppToAgent(app *v1alpha1.Application) {
	if s.destinationBasedMapping && s.appToAgent != nil {
		s.appToAgent.Delete(app.QualifiedName())
	}
}

// GetAgentForApp returns the agent name for the given application based on
// the app's namespace and name. This is used by the redis proxy to route
// requests to the correct agent when destination-based mapping is enabled.
// Returns empty string if the app is not tracked or destination-based mapping is disabled.
func (s *Server) GetAgentForApp(namespace, name string) string {
	if !s.destinationBasedMapping {
		// In namespace-based mapping, the namespace is the agent name
		return namespace
	}
	if s.appToAgent == nil {
		return ""
	}
	qualifiedName := fmt.Sprintf("%s/%s", namespace, name)
	return s.appToAgent.Get(qualifiedName)
}

// handleAppAgentChange handles the case when an application's destination.name changes,
// meaning it needs to be moved from one agent to another.
func (s *Server) handleAppAgentChange(ctx context.Context, old, new *v1alpha1.Application, logCtx *logrus.Entry) {
	if isResourceFromAutonomousAgent(old) || isResourceFromAutonomousAgent(new) {
		return
	}

	oldAgentName := old.Spec.Destination.Name
	newAgentName := new.Spec.Destination.Name

	if !s.destinationBasedMapping || (oldAgentName == newAgentName) {
		return
	}

	logCtx.Info("Application destination changed, moving app from old agent to new agent")

	if oldAgentName != "" {
		// Remove mapping and delete the app from the old agent
		s.untrackAppToAgent(old)
		s.resources.Remove(oldAgentName, resources.NewResourceKeyFromApp(old))

		if !s.queues.HasQueuePair(oldAgentName) {
			if err := s.queues.Create(oldAgentName); err != nil {
				logCtx.WithError(err).Error("failed to create queue pair for old agent")
			}
		}
		if oldQ := s.queues.SendQ(oldAgentName); oldQ != nil {
			deleteEv := s.events.ApplicationEvent(event.Delete, old)
			tracing.InjectTraceContext(ctx, deleteEv)
			oldQ.Add(deleteEv)
			logCtx.Debug("Sent delete event to old agent")
		}

	}

	if newAgentName != "" {
		// Add mapping for the new agent
		s.trackAppToAgent(new, newAgentName)
		s.resources.Add(newAgentName, resources.NewResourceKeyFromApp(new))
	}
}

// startSpan creates a trace span for a principal callback with common attributes.
// Returns the context with the span and the span itself for defer cleanup.
func (s *Server) startSpan(operation, resourceKind string, obj metav1.Object) (context.Context, trace.Span) {
	if !tracing.IsEnabled() {
		return s.ctx, trace.SpanFromContext(s.ctx)
	}

	agentMode := types.AgentModeManaged
	if isResourceFromAutonomousAgent(obj) {
		agentMode = types.AgentModeAutonomous
	}

	spanName := fmt.Sprintf("%s.%s", resourceKind, operation)
	return tracing.Tracer().Start(s.ctx, spanName,
		trace.WithAttributes(
			tracing.AttrComponentType.String("principal"),
			tracing.AttrOperationType.String(operation),
			tracing.AttrAgentName.String(obj.GetNamespace()),
			tracing.AttrResourceName.String(obj.GetName()),
			tracing.AttrResourceKind.String(resourceKind),
			tracing.AttrAgentMode.String(agentMode.String()),
			tracing.AttrResourceUID.String(string(obj.GetUID())),
		),
	)
}

type concurrentMap[K comparable, V any] struct {
	mu sync.RWMutex
	m  map[K]V
}

func newConcurrentStringMap() *concurrentMap[string, string] {
	return &concurrentMap[string, string]{m: make(map[string]string)}
}

func (c *concurrentMap[K, V]) Get(key K) V {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.m[key]
}

func (c *concurrentMap[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = value
}

func (c *concurrentMap[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}
