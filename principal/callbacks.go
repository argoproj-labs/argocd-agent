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
	corev1 "k8s.io/api/core/v1"
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
	if len(new.Finalizers) > 0 && len(new.Finalizers) != len(old.Finalizers) {
		tmp, err := s.appManager.RemoveFinalizers(s.ctx, new)
		if err != nil {
			logCtx.WithError(err).Warnf("Could not remove finalizer")
		} else {
			logCtx.Debug("Removed finalizer")
			new = tmp
		}
	}
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
		"component":       "EventCallback",
		"queue":           old.Namespace,
		"event":           "appproject_update",
		"appproject_name": old.Name,
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
	ev := s.events.AppProjectEvent(event.SpecUpdate, new)
	q.Add(ev)
	logCtx.Tracef("Added app to send queue, total length now %d", q.Len())

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
	ev := s.events.AppProjectEvent(event.Delete, outbound)
	logCtx.WithField("event", "DeleteAppProject").WithField("sendq_len", q.Len()+1).Tracef("Added event to send queue")
	q.Add(ev)

	if s.metrics != nil {
		s.metrics.AppProjectDeleted.Inc()
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

	logCtx.Tracef("Deleted the queue pair since the agent namespace is deleted")
}
