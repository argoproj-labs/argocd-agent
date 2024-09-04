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
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
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

	// Return early if no interested agent is connected
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Debug("No agent is connected to this queue, discarding event")
		return
	}

	// New app events are only relevant for managed agents
	mode := s.agentMode(outbound.Namespace)
	if mode != types.AgentModeManaged {
		logCtx.Tracef("Discarding event for unmanaged agent")
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", outbound.Namespace)
		return
	}
	ev := s.events.ApplicationEvent(event.Create, outbound)
	q.Add(ev)
	logCtx.Tracef("Added app %s to send queue, total length now %d", outbound.QualifiedName(), q.Len())
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
	if len(new.Finalizers) > 0 && len(new.Finalizers) != len(old.Finalizers) {
		var err error
		new, err = s.appManager.RemoveFinalizers(s.ctx, new)
		if err != nil {
			logCtx.WithError(err).Warnf("Could not remove finalizer")
		} else {
			logCtx.Debug("Removed finalizer")
		}
	}
	if s.appManager.IsChangeIgnored(new.QualifiedName(), new.ResourceVersion) {
		logCtx.WithField("resource_version", new.ResourceVersion).Debugf("Resource version has already been seen")
		return
	}
	if !s.queues.HasQueuePair(old.Namespace) {
		logCtx.Tracef("No agent is connected to this queue, discarding event")
		return
	}
	q := s.queues.SendQ(old.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.ApplicationEvent(event.SpecUpdate, new)
	q.Add(ev)
	logCtx.Tracef("Added app to send queue, total length now %d", q.Len())
}

func (s *Server) deleteAppCallback(outbound *v1alpha1.Application) {
	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"queue":            outbound.Namespace,
		"event":            "application_delete",
		"application_name": outbound.Name,
	})
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Tracef("No agent is connected to this queue, discarding event")
		return
	}
	mode := s.agentMode(outbound.Namespace)
	if !mode.IsManaged() {
		logCtx.Tracef("Discarding event for unmanaged agent")
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.ApplicationEvent(event.Delete, outbound)
	logCtx.WithField("event", "DeleteApp").WithField("sendq_len", q.Len()+1).Tracef("Added event to send queue")
	q.Add(ev)
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

	// Return early if no interested agent is connected
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Debug("No agent is connected to this queue, discarding event")
		return
	}

	// New appproject events are only relevant for managed agents
	mode := s.agentMode(outbound.Namespace)
	if mode != types.AgentModeManaged {
		logCtx.Tracef("Discarding event for unmanaged agent")
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", outbound.Namespace)
		return
	}
	ev := s.events.AppProjectEvent(event.Create, outbound)
	q.Add(ev)
	logCtx.Tracef("Added appProject %s to send queue, total length now %d", outbound.Name, q.Len())
}

func (s *Server) updateAppProjectCallback(old *v1alpha1.AppProject, new *v1alpha1.AppProject) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	logCtx := log().WithFields(logrus.Fields{
		"component":        "EventCallback",
		"queue":            old.Namespace,
		"event":            "application_update",
		"application_name": old.Name,
	})
	if len(new.Finalizers) > 0 && len(new.Finalizers) != len(old.Finalizers) {
		var err error
		new, err = s.projectManager.RemoveFinalizers(s.ctx, new)
		if err != nil {
			logCtx.WithError(err).Warnf("Could not remove finalizer")
		} else {
			logCtx.Debug("Removed finalizer")
		}
	}
	if s.appManager.IsChangeIgnored(new.Name, new.ResourceVersion) {
		logCtx.WithField("resource_version", new.ResourceVersion).Debugf("Resource version has already been seen")
		return
	}
	if !s.queues.HasQueuePair(old.Namespace) {
		logCtx.Tracef("No agent is connected to this queue, discarding event")
		return
	}
	q := s.queues.SendQ(old.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.AppProjectEvent(event.SpecUpdate, new)
	q.Add(ev)
	logCtx.Tracef("Added app to send queue, total length now %d", q.Len())
}

func (s *Server) deleteAppProjectCallback(outbound *v1alpha1.AppProject) {
	logCtx := log().WithFields(logrus.Fields{
		"component":       "EventCallback",
		"queue":           outbound.Namespace,
		"event":           "appproject_delete",
		"appproject_name": outbound.Name,
	})
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Tracef("No agent is connected to this queue, discarding event")
		return
	}
	mode := s.agentMode(outbound.Namespace)
	if !mode.IsManaged() {
		logCtx.Tracef("Discarding event for unmanaged agent")
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Error("Help! Queue pair has disappeared!")
		return
	}
	ev := s.events.AppProjectEvent(event.Delete, outbound)
	logCtx.WithField("event", "DeleteAppProject").WithField("sendq_len", q.Len()+1).Tracef("Added event to send queue")
	q.Add(ev)
}
