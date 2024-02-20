package principal

import (
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
)

// newAppCallback is executed when a new application event was emitted from
// the informer and needs to be sent out to an agent. If the receiving agent
// is in autonomous mode, this event will be discarded.
func (s *Server) newAppCallback(outbound *v1alpha1.Application) {
	logCtx := log().WithFields(logrus.Fields{
		"component": "NewAppCallback",
		"namespace": outbound.Namespace,
	})

	mode := s.agentMode(outbound.Namespace)
	if mode != types.AgentModeManaged {
		logCtx.Tracef("Discarding NewApp event for unmanaged agent")
		return
	}
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Tracef("no agent connected to namespace %s, discarding", outbound.Namespace)
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", outbound.Namespace)
		return
	}
	ev := s.events.NewApplicationEvent(event.ApplicationCreated, outbound)
	q.Add(ev)
	logCtx.Tracef("Added app %s to send queue, total length now %d", outbound.QualifiedName(), q.Len())
}

func (s *Server) updateAppCallback(old *v1alpha1.Application, new *v1alpha1.Application) {
	s.watchLock.Lock()
	defer s.watchLock.Unlock()
	logCtx := log().WithFields(logrus.Fields{
		"component":   "UpdateAppCallback",
		"application": old.Name,
		"queue":       old.Namespace,
	})
	if s.appManager.IsChangeIgnored(new.QualifiedName(), new.ResourceVersion) {
		logCtx.Debugf("I have seen this version %s already", new.ResourceVersion)
		return
	}
	if !s.queues.HasQueuePair(old.Namespace) {
		logCtx.Tracef("no agent connected to namespace %s, discarding", old.Namespace)
		return
	}
	q := s.queues.SendQ(old.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", old.Namespace)
		return
	}
	ev := s.events.NewApplicationEvent(event.ApplicationSpecUpdated, new)
	q.Add(ev)
	logCtx.Tracef("Added app to send queue, total length now %d", q.Len())
}

func (s *Server) deleteAppCallback(outbound *v1alpha1.Application) {
	logCtx := log().WithField("component", "DeleteAppCallback")
	if !s.queues.HasQueuePair(outbound.Namespace) {
		logCtx.Tracef("no agent connected to namespace %s, discarding", outbound.Namespace)
		return
	}
	mode := s.agentMode(outbound.Namespace)
	if mode != types.AgentModeManaged {
		logCtx.Tracef("Discarding DeleteApp event for unmanaged agent")
		return
	}
	q := s.queues.SendQ(outbound.Namespace)
	if q == nil {
		logCtx.Errorf("Help! queue pair for namespace %s disappeared!", outbound.Namespace)
		return
	}
	ev := s.events.NewApplicationEvent(event.ApplicationDeleted, outbound)
	logCtx.WithField("event", "DeleteApp").WithField("sendq_len", q.Len()+1).Tracef("Added event to send queue")
	q.Add(ev)
}
