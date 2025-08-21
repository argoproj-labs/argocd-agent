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
	"strings"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/v3/util/glob"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newAppCallback is executed when a new application event was emitted from
// the informer and needs to be sent out to an agent. If the receiving agent
// is in autonomous mode, this event will be discarded.
func (s *Server) newAppCallback(outbound *v1alpha1.Application) {
	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"queue":            outbound.Namespace,
		"event":            "application_new",
		"application_name": outbound.Name,
	})

	s.resources.Add(outbound.Namespace, resources.NewResourceKeyFromApp(outbound))

	if !s.queues.HasQueuePair(outbound.Namespace) {
		if err := s.queues.Create(outbound.Namespace); err != nil {
			logCtx.WithError(err).Error("failed to create a queue pair for an existing agent namespace")
			return
		}
		logCtx.Trace("Created a new queue pair for the existing namespace")
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", outbound.Namespace)
		return
	}
	ev := s.events.ApplicationEvent(event.Create, outbound)
	q.Add(ev)
	logCtx.Tracef("Added app %s to send queue, total length now %d", outbound.QualifiedName(), q.Len())

	if s.metrics != nil {
		s.metrics.ApplicationCreated.Inc()
	}
}

func (s *Server) updateAppCallback(old *v1alpha1.Application, new *v1alpha1.Application) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"queue":            old.Namespace,
		"event":            "application_update",
		"application_name": old.Name,
	})
	if s.appManager.IsChangeIgnored(new.QualifiedName(), new.ResourceVersion) {
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
	q := s.queues.SendQ(old.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.ApplicationEvent(event.SpecUpdate, new)
	q.Add(ev)
	logCtx.Tracef("Added app to send queue, total length now %d", q.Len())

	if s.metrics != nil {
		s.metrics.ApplicationUpdated.Inc()
	}
}

func (s *Server) deleteAppCallback(outbound *v1alpha1.Application) {
	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"queue":            outbound.Namespace,
		"event":            "application_delete",
		"application_name": outbound.Name,
	})

	s.resources.Remove(outbound.Namespace, resources.NewResourceKeyFromApp(outbound))

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
	ev := s.events.ApplicationEvent(event.Delete, outbound)
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
	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           outbound.Namespace,
		"event":           "appproject_new",
		"appproject_name": outbound.Name,
	})

	s.resources.Add(outbound.Namespace, resources.NewResourceKeyFromAppProject(outbound))

	// Check if this AppProject was created by an autonomous agent
	if isResourceFromAutonomousAgent(outbound) {
		logCtx.Debugf("Discarding event, because the appProject is managed by an autonomous agent")
		return
	}

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

		agentAppProject := AgentSpecificAppProject(*outbound, agent)
		ev := s.events.AppProjectEvent(event.Create, &agentAppProject)
		q.Add(ev)
		logCtx.Tracef("Added appProject %s to send queue, total length now %d", outbound.Name, q.Len())
	}

	// Reconcile repositories that reference this project.
	s.syncRepositoriesForProject(outbound.Name, outbound.Namespace, logCtx)
}

func (s *Server) updateAppProjectCallback(old *v1alpha1.AppProject, new *v1alpha1.AppProject) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           old.Namespace,
		"event":           "appproject_update",
		"appproject_name": old.Name,
	})

	// Check if this AppProject was created by an autonomous agent
	if isResourceFromAutonomousAgent(new) {
		logCtx.Debugf("Discarding event, because the appProject is managed by an autonomous agent")
		return
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

	s.syncAppProjectUpdatesToAgents(old, new, logCtx)

	// The project rules could have been updated. Reconcile repositories that reference this project.
	s.syncRepositoriesForProject(new.Name, new.Namespace, logCtx)

	if s.metrics != nil {
		s.metrics.AppProjectUpdated.Inc()
	}
}

func (s *Server) deleteAppProjectCallback(outbound *v1alpha1.AppProject) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           outbound.Namespace,
		"event":           "appproject_delete",
		"appproject_name": outbound.Name,
	})

	s.resources.Remove(outbound.Namespace, resources.NewResourceKeyFromAppProject(outbound))

	// Check if this AppProject was created by an autonomous agent by examining its name prefix
	if isResourceFromAutonomousAgent(outbound) {
		logCtx.Debugf("Discarding event, because the appProject is managed by an autonomous agent")
		return
	}

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

		agentAppProject := AgentSpecificAppProject(*outbound, agent)
		ev := s.events.AppProjectEvent(event.Delete, &agentAppProject)
		q.Add(ev)
		logCtx.WithField("sendq_len", q.Len()+1).Tracef("Added appProject delete event to send queue")
	}

	if s.metrics != nil {
		s.metrics.AppProjectDeleted.Inc()
	}
}

func (s *Server) newRepositoryCallback(outbound *corev1.Secret) {
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
		q.Add(ev)

		s.repoToAgents.Add(outbound.Name, agent)
		logCtx.Tracef("Added repository %s to send queue, total length now %d", outbound.Name, q.Len())
	}
}

func (s *Server) updateRepositoryCallback(old, new *corev1.Secret) {
	logCtx := log().WithFields(logrus.Fields{
		"component": "EventCallback",
		"event":     "repository_update",
		"repo_name": old.Name,
	})

	logCtx.Info("Update repository event")

	s.syncRepositoryUpdatesToAgents(old, new, logCtx)
}

func (s *Server) deleteRepositoryCallback(outbound *corev1.Secret) {
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
		q.Add(ev)

		s.repoToAgents.Delete(outbound.Name, agent)
		logCtx.WithField("sendq_len", q.Len()+1).Tracef("Added repository delete event to send queue")
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

		if doesAgentMatchWithProject(agentName, appProject) {
			agents[agentName] = true
		}
	}

	return agents
}

// doesAgentMatchWithProject checks if the agent name matches the given AppProject.
// We match by name OR server if either is present. If both are present, we match by name.
// Deny patterns take precedence over allow patterns.
func doesAgentMatchWithProject(agentName string, appProject v1alpha1.AppProject) bool {
	destinationMatched := false

	for _, dst := range appProject.Spec.Destinations {
		// If the destination name is not empty, we need to match it with the agent name
		if dst.Name != "" {
			// Check if the user has denied this agent
			if isDenyPattern(dst.Name) {
				// Check if this deny pattern matches the agent name
				if glob.Match(dst.Name[1:], agentName) {
					return false
				}
			} else {
				// Check if the agent name matches the destination name. Continue matching...
				if glob.Match(dst.Name, agentName) {
					destinationMatched = true
				}
			}
		} else if dst.Server == "*" {
			// The server must be a wildcard if name is empty. Continue matching...
			destinationMatched = true
		}
	}

	// Must match both destination and source namespace requirements
	return destinationMatched &&
		glob.MatchStringInList(appProject.Spec.SourceNamespaces, agentName, glob.REGEXP)
}

func isDenyPattern(pattern string) bool {
	return strings.HasPrefix(pattern, "!")
}

// syncAppProjectUpdatesToAgents sends the AppProject update events to the relevant clusters.
// It sends delete events to the clusters that no longer match the given AppProject.
func (s *Server) syncAppProjectUpdatesToAgents(old, new *v1alpha1.AppProject, logCtx *logrus.Entry) {
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

		agentAppProject := AgentSpecificAppProject(*new, agent)
		ev := s.events.AppProjectEvent(event.Delete, &agentAppProject)
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

		agentAppProject := AgentSpecificAppProject(*new, agent)
		ev := s.events.AppProjectEvent(event.SpecUpdate, &agentAppProject)
		q.Add(ev)
		logCtx.Tracef("Added appProject %s update event to send queue", new.Name)
	}
}

// syncRepositoryUpdatesToAgents sends the repository update events to the relevant agents.
// It sends delete events to the agents that no longer match the given project.
func (s *Server) syncRepositoryUpdatesToAgents(old, new *corev1.Secret, logCtx *logrus.Entry) {
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
		q.Add(ev)

		s.repoToAgents.Add(new.Name, agent)

		logCtx.Tracef("Added repository %s update event to send queue", new.Name)
	}
}

func (s *Server) syncRepositoriesForProject(projectName, ns string, logCtx *logrus.Entry) {
	repositories := s.projectToRepos.Get(projectName)
	for repoName := range repositories {
		logCtx.Tracef("Syncing repository %s for project %s", repoName, projectName)

		repo, err := s.kubeClient.Clientset.CoreV1().Secrets(ns).Get(s.ctx, repoName, metav1.GetOptions{})
		if err != nil {
			logCtx.WithError(err).Error("failed to get the repository secret")
			continue
		}

		s.syncRepositoryUpdatesToAgents(repo, repo, logCtx)
	}
}

// AgentSpecificAppProject returns an agent specific version of the given AppProject
// We don't have to check for deny patterns because we only construct the agent specific AppProject
// if the agent name matches the AppProject's destinations.
func AgentSpecificAppProject(appProject v1alpha1.AppProject, agent string) v1alpha1.AppProject {
	// Only keep the destinations that are relevant to the given agent
	filteredDst := []v1alpha1.ApplicationDestination{}
	for _, dst := range appProject.Spec.Destinations {
		nameMatched := dst.Name != "" && glob.Match(dst.Name, agent)
		serverMatched := dst.Name == "" && dst.Server == "*"
		if nameMatched || serverMatched {
			dst.Name = "in-cluster"
			dst.Server = "https://kubernetes.default.svc"

			filteredDst = append(filteredDst, dst)
		}
	}
	appProject.Spec.Destinations = filteredDst

	// Only allow Applications to be managed from the agent namespace
	appProject.Spec.SourceNamespaces = []string{agent}

	// Remove the roles since they are not relevant on the workload cluster
	appProject.Spec.Roles = []v1alpha1.ProjectRole{}

	return appProject
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
