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
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

// addAppCreationToQueue processes a new application event originating from the
// AppInformer and puts it in the send queue.
func (a *Agent) addAppCreationToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField("event", "NewApp").WithField("application", app.QualifiedName())
	logCtx.Debugf("New app event")

	// If the agent is not connected, we ignore this event. It just makes no
	// sense to fill up the send queue when we can't send.
	if !a.IsConnected() {
		logCtx.Trace("Agent is not connected, ignoring this event")
		return
	}

	// Update events trigger a new event sometimes, too. If we've already seen
	// the app, we just ignore the request then.
	if a.appManager.IsManaged(app.QualifiedName()) {
		logCtx.Trace("App is already managed")
		return
	}

	if err := a.appManager.Manage(app.QualifiedName()); err != nil {
		logCtx.Tracef("Could not manage app: %v", err)
		return
	}

	q := a.queues.SendQ(a.remote.ClientID())
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}

	q.Add(a.emitter.ApplicationEvent(event.Create, app))
	logCtx.WithField("sendq_len", q.Len()).WithField("sendq_name", a.remote.ClientID()).Debugf("Added app create event to send queue")
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

	// If the agent is not connected, we ignore this event. It just makes no
	// sense to fill up the send queue when we can't send.
	if !a.IsConnected() {
		logCtx.Trace("Agent is not connected, ignoring this event")
		return
	}

	// If the app is not managed, we ignore this event.
	if !a.appManager.IsManaged(new.QualifiedName()) {
		logCtx.Tracef("App is not managed")
		return
	}

	q := a.queues.SendQ(a.remote.ClientID())
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
		WithField("sendq_name", a.remote.ClientID()).
		Debugf("Added event of type %s to send queue", eventType)
}

// addAppDeletionToQueue processes an application delete event originating from
// the AppInformer and puts it in the send queue.
func (a *Agent) addAppDeletionToQueue(app *v1alpha1.Application) {
	logCtx := log().WithField("event", "DeleteApp").WithField("application", app.QualifiedName())
	logCtx.Debugf("Delete app event")

	// If the agent is not connected, we ignore this event. It just makes no
	// sense to fill up the send queue when we can't send.
	if !a.IsConnected() {
		logCtx.Trace("Agent is not connected, ignoring this event")
		return
	}

	if !a.appManager.IsManaged(app.QualifiedName()) {
		logCtx.Tracef("App is not managed")
	}

	q := a.queues.SendQ(a.remote.ClientID())
	if q == nil {
		logCtx.Error("Default queue disappeared!")
		return
	}
	q.Add(a.emitter.ApplicationEvent(event.Delete, app))
	logCtx.WithField("sendq_len", q.Len()).Debugf("Added app delete event to send queue")
}
