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
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// addAppCreationToQueue processes a new application event originating from the
// AppInformer and puts it in the send queue.
func (a *Agent) addAppCreationToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField("event", "NewApp").WithField("application", app.QualifiedName())
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
	logCtx.WithField("sendq_len", q.Len()).WithField("sendq_name", defaultQueueName).Debugf("Added app create event to send queue")
}

// addAppUpdateToQueue processes an application update event originating from
// the AppInformer and puts it in the agent's send queue.
func (a *Agent) addAppUpdateToQueue(old *v1alpha1.Application, new *v1alpha1.Application) {
	logCtx := log().WithField("event", "UpdateApp").WithField("application", old.QualifiedName())
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
		WithField("sendq_len", q.Len()).
		WithField("sendq_name", defaultQueueName).
		Debugf("Added event of type %s to send queue", eventType)
}

// addAppDeletionToQueue processes an application delete event originating from
// the AppInformer and puts it in the send queue.
func (a *Agent) addAppDeletionToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField("event", "DeleteApp").WithField("application", app.QualifiedName())
	logCtx.Debugf("Delete app event")

	a.resources.Remove(resources.NewResourceKeyFromApp(app))

	if !a.appManager.IsManaged(app.QualifiedName()) {
		logCtx.Warn("App is not managed, proceeding anyways")
	} else {
		_ = a.appManager.Unmanage(app.QualifiedName())
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	// Only send the deletion event when we're in autonomous mode
	if !a.mode.IsAutonomous() {
		return
	}

	q.Add(a.emitter.ApplicationEvent(event.Delete, app))
	logCtx.WithField("sendq_len", q.Len()).Debugf("Added app delete event to send queue")
}

// deleteNamespaceCallback is called when the user deletes the agent namespace.
// Since there is no namespace we can remove the queue associated with this agent.
func (a *Agent) deleteNamespaceCallback(outbound *corev1.Namespace) {
	logCtx := log().WithField("event", "DeleteNamespace").WithField("agent", outbound.Name)

	if !a.queues.HasQueuePair(outbound.Name) {
		return
	}

	if err := a.queues.Delete(outbound.Name, true); err != nil {
		logCtx.WithError(err).Error("failed to remove the queue pair for a deleted agent namespace")
		return
	}

	logCtx.Tracef("Deleted the queue pair since the agent namespace is deleted")
}
