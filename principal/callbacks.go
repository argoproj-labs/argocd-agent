package principal

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
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
