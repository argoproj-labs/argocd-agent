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

package agent

import (
	"errors"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/logging/logfields"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// addAppCreationToQueue processes a new application event originating from the
// AppInformer and puts it in the send queue.
func (a *Agent) addAppCreationToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField(logfields.Event, "NewApp").WithField(logfields.Application, app.QualifiedName())
	logCtx.Debugf("New app event")

	a.resources.Add(resources.NewResourceKeyFromApp(app))

	// Update events trigger a new event sometimes, too. If we've already seen
	// the app, we just ignore the request then.
	if a.appManager.IsManaged(app.QualifiedName()) {
		logCtx.Error("Cannot manage app that is already managed")
		return
	}

	if err := a.appManager.Manage(app.QualifiedName()); err != nil {
		logCtx.Errorf("Could not manage app: %v", err)
		return
	}

	// Only send the creation event when we're in autonomous mode
	if !a.mode.IsAutonomous() {
		return
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.ApplicationEvent(event.Create, app))
	logCtx.WithField(logfields.SendQueueLen, q.Len()).WithField(logfields.SendQueueName, defaultQueueName).Debugf("Added app create event to send queue")
}

// addAppUpdateToQueue processes an application update event originating from
// the AppInformer and puts it in the agent's send queue.
func (a *Agent) addAppUpdateToQueue(old *v1alpha1.Application, new *v1alpha1.Application) {
	logCtx := log().WithField(logfields.Event, "UpdateApp").WithField(logfields.Application, old.QualifiedName())
	a.watchLock.Lock()
	defer a.watchLock.Unlock()
	if a.appManager.IsChangeIgnored(new.QualifiedName(), new.ResourceVersion) {
		logCtx.Debugf("Ignoring this change for resource version %s", new.ResourceVersion)
		return
	}

	// If the app is not managed, we ignore this event.
	if !a.appManager.IsManaged(new.QualifiedName()) {
		logCtx.Errorf("Received update event for unmanaged app")
		return
	}

	// Revert any direct modifications done in application on managed-cluster
	// because for managed-agent all changes should be done through principal
	if reverted := a.appManager.RevertManagedAppChanges(a.context, new, a.sourceCache.Application); reverted {
		logCtx.Debugf("Modifications done in application: %s are reverted", new.Name)
		return
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	// Depending on what mode the agent operates in, we sent a different type
	// of event.
	var eventType event.EventType
	switch a.mode {
	case types.AgentModeAutonomous:
		eventType = event.SpecUpdate
	case types.AgentModeManaged:
		eventType = event.StatusUpdate
	}

	q.Add(a.emitter.ApplicationEvent(eventType, new))
	// q.Add(ev)
	logCtx.
		WithField(logfields.SendQueueLen, q.Len()).
		WithField(logfields.SendQueueName, defaultQueueName).
		Debugf("Added event of type %s to send queue", eventType)
}

// addAppDeletionToQueue processes an application delete event originating from
// the AppInformer and puts it in the send queue.
func (a *Agent) addAppDeletionToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField(logfields.Event, "DeleteApp").WithField(logfields.Application, app.QualifiedName())
	logCtx.Debugf("Delete app event")

	a.resources.Remove(resources.NewResourceKeyFromApp(app))

	if !a.appManager.IsManaged(app.QualifiedName()) {
		logCtx.Warn("Dropping app deletion event because the app is not managed")
		return
	} else {
		_ = a.appManager.Unmanage(app.QualifiedName())
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.ApplicationEvent(event.Delete, app))
	logCtx.WithField(logfields.SendQueueLen, q.Len()).Debugf("Added app delete event to send queue")
}

// deleteNamespaceCallback is called when the user deletes the agent namespace.
// Since there is no namespace we can remove the queue associated with this agent.
func (a *Agent) deleteNamespaceCallback(outbound *corev1.Namespace) {
	logCtx := log().WithField(logfields.Event, "DeleteNamespace").WithField(logfields.Agent, outbound.Name)

	if !a.queues.HasQueuePair(outbound.Name) {
		return
	}

	if err := a.queues.Delete(outbound.Name, true); err != nil {
		logCtx.WithError(err).Error("failed to remove the queue pair for a deleted agent namespace")
		return
	}

	logCtx.Tracef("Deleted the queue pair since the agent namespace is deleted")
}

// addAppProjectCreationToQueue processes a new appProject event originating from the
// AppProject Informer and puts it in the send queue.
func (a *Agent) addAppProjectCreationToQueue(appProject *v1alpha1.AppProject) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "NewAppProject",
		"appProject": appProject.Name,
		"sendq_name": defaultQueueName,
	})

	logCtx.Debugf("New appProject event")

	a.resources.Add(resources.NewResourceKeyFromAppProject(appProject))

	// Update events trigger a new event sometimes, too. If we've already seen
	// the appProject, we just ignore the request then.
	if a.projectManager.IsManaged(appProject.Name) {
		logCtx.Error("Cannot manage appProject that is already managed")
		return
	}

	if err := a.projectManager.Manage(appProject.Name); err != nil {
		logCtx.Errorf("Could not manage appProject: %v", err)
		return
	}

	// Only send the creation event when we're in autonomous mode
	if !a.mode.IsAutonomous() {
		return
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.AppProjectEvent(event.Create, appProject))
	logCtx.WithField(logfields.SendQueueLen, q.Len()).Debugf("Added appProject create event to send queue")
}

// addAppProjectUpdateToQueue processes an appProject update event originating from the
// AppProject Informer and puts it in the send queue.
func (a *Agent) addAppProjectUpdateToQueue(old *v1alpha1.AppProject, new *v1alpha1.AppProject) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "UpdateAppProject",
		"appProject": new.Name,
		"sendq_name": defaultQueueName,
	})

	a.watchLock.Lock()
	defer a.watchLock.Unlock()

	if a.projectManager.IsChangeIgnored(new.Name, new.ResourceVersion) {
		logCtx.Debugf("Ignoring this change for resource version %s", new.ResourceVersion)
		return
	}

	// If the appProject is not managed, we ignore this event.
	if !a.projectManager.IsManaged(new.Name) {
		logCtx.Errorf("Received update event for unmanaged appProject")
		return
	}

	// Revert any direct modifications done to appProject on managed-cluster
	// because for managed-agent all changes should be done through principal
	if isResourceFromPrincipal(new) {
		reverted, err := a.projectManager.RevertAppProjectChanges(a.context, new, a.sourceCache.AppProject)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert modifications done to appProject")
			return
		}
		if reverted {
			logCtx.Debugf("Modifications done to appProject are reverted")
			return
		}
	}

	// Only send the update event when we're in autonomous mode
	if !a.mode.IsAutonomous() {
		return
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.AppProjectEvent(event.SpecUpdate, new))
	logCtx.WithField(logfields.SendQueueLen, q.Len()).Debugf("Added appProject spec update event to send queue")
}

// addAppProjectDeletionToQueue processes an appProject delete event originating from the
// AppProject Informer and puts it in the send queue.
func (a *Agent) addAppProjectDeletionToQueue(appProject *v1alpha1.AppProject) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "DeleteAppProject",
		"appProject": appProject.Name,
		"sendq_name": defaultQueueName,
	})

	logCtx.Debugf("Delete appProject event")

	if isResourceFromPrincipal(appProject) {
		reverted, err := manager.RevertUserInitiatedDeletion(a.context, appProject, a.deletions, a.projectManager, logCtx)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert invalid deletion of appProject")
			return
		}
		if reverted {
			logCtx.Trace("Deleted appProject is recreated")
			return
		}
	}

	a.resources.Remove(resources.NewResourceKeyFromAppProject(appProject))

	// Only send the deletion event when we're in autonomous mode
	if !a.mode.IsAutonomous() {
		return
	}

	if !a.projectManager.IsManaged(appProject.Name) {
		logCtx.Warn("Dropping appProject deletion event because the appProject is not managed")
		return
	} else {
		_ = a.projectManager.Unmanage(appProject.Name)
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.AppProjectEvent(event.Delete, appProject))
	logCtx.WithField(logfields.SendQueueLen, q.Len()).Debugf("Added appProject delete event to send queue")
}

// addClusterCacheInfoUpdateToQueue processes a cluster cache info update event
// and puts it in the send queue.
func (a *Agent) addClusterCacheInfoUpdateToQueue() {
	logCtx := log().WithFields(logrus.Fields{
		"event": "addClusterCacheInfoUpdateToQueue",
	})

	clusterServer := "https://kubernetes.default.svc"
	clusterInfo := &v1alpha1.ClusterInfo{}

	// Get the updated cluster info from agent's cache.
	if err := a.clusterCache.GetClusterInfo(clusterServer, clusterInfo); err != nil {
		if !errors.Is(err, cacheutil.ErrCacheMiss) {
			logCtx.WithError(err).Errorf("Failed to get cluster info from cache")
		}
		return
	}

	// Send the event to principal to update the cluster cache info.
	q := a.queues.SendQ(defaultQueueName)
	if q != nil {
		clusterInfoEvent := a.emitter.ClusterCacheInfoUpdateEvent(event.ClusterCacheInfoUpdate, &event.ClusterCacheInfo{
			ApplicationsCount: clusterInfo.ApplicationsCount,
			APIsCount:         clusterInfo.CacheInfo.APIsCount,
			ResourcesCount:    clusterInfo.CacheInfo.ResourcesCount,
		})

		q.Add(clusterInfoEvent)
		logCtx.WithFields(logrus.Fields{
			"sendq_len":         q.Len(),
			"sendq_name":        defaultQueueName,
			"applicationsCount": clusterInfo.ApplicationsCount,
			"apisCount":         clusterInfo.CacheInfo.APIsCount,
			"resourcesCount":    clusterInfo.CacheInfo.ResourcesCount,
		}).Infof("Added ClusterCacheInfoUpdate event to send queue")
	} else {
		logCtx.Error("Default queue not found, unable to send ClusterCacheInfoUpdate event")
	}
}

func (a *Agent) handleRepositoryCreation(repo *corev1.Secret) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "NewRepository",
		"repository": repo.Name,
	})

	logCtx.Debugf("New repository event")

	if a.mode.IsAutonomous() {
		logCtx.Debugf("Skipping repository event because the agent is not in managed mode")
		return
	}

	a.resources.Add(resources.NewResourceKeyFromRepository(repo))

	if a.repoManager.IsManaged(repo.Name) {
		logCtx.Debugf("Skipping repository event because the repository is already managed")
		return
	}

	if err := a.repoManager.Manage(repo.Name); err != nil {
		logCtx.Errorf("Could not manage repository: %v", err)
		return
	}
}

func (a *Agent) handleRepositoryUpdate(old, new *corev1.Secret) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "UpdateRepository",
		"repository": new.Name,
	})

	a.watchLock.Lock()
	defer a.watchLock.Unlock()

	if a.repoManager.RevertRepositoryChanges(a.context, new, a.sourceCache.Repository) {
		logCtx.Debugf("Modifications done to repository are reverted")
		return
	}

	if a.mode.IsAutonomous() {
		logCtx.Debugf("Skipping repository event because the agent is not in managed mode")
		return
	}

	if a.repoManager.IsChangeIgnored(new.Name, new.ResourceVersion) {
		logCtx.Debugf("Ignoring this change for resource version %s", new.ResourceVersion)
		return
	}

	if !a.repoManager.IsManaged(new.Name) {
		logCtx.Debugf("Skipping repository event because the repository is already managed")
		return
	}
}

func (a *Agent) handleRepositoryDeletion(repo *corev1.Secret) {
	logCtx := log().WithFields(logrus.Fields{
		"event":      "DeleteRepository",
		"repository": repo.Name,
	})

	logCtx.Debugf("Delete repository event")

	if isResourceFromPrincipal(repo) {
		reverted, err := manager.RevertUserInitiatedDeletion(a.context, repo, a.deletions, a.repoManager, logCtx)
		if err != nil {
			logCtx.WithError(err).Error("failed to revert invalid deletion of repository")
			return
		}
		if reverted {
			logCtx.Trace("Deleted repository is recreated")
			return
		}
	}

	if a.mode.IsAutonomous() {
		logCtx.Debugf("Skipping repository event because the agent is not in managed mode")
		return
	}

	a.resources.Remove(resources.NewResourceKeyFromRepository(repo))

	if !a.repoManager.IsManaged(repo.Name) {
		logCtx.Debugf("Skipping repository event because the repository is not managed")
		return
	}

	if err := a.repoManager.Unmanage(repo.Name); err != nil {
		logCtx.Errorf("Could not unmanage repository: %v", err)
		return
	}
}

// isResourceFromPrincipal checks if a Kubernetes resource was created by the principal
// by examining if it has the source UID annotation.
func isResourceFromPrincipal(resource metav1.Object) bool {
	annotations := resource.GetAnnotations()
	if annotations == nil {
		return false
	}
	_, ok := annotations[manager.SourceUIDAnnotation]
	return ok
}
