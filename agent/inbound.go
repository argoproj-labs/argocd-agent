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
	"encoding/json"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/checkpoint"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/resync"
	"github.com/argoproj-labs/argocd-agent/internal/tracing"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

/*
This file contains a collection of callbacks to process inbound events.

Inbound events are those coming through our gRPC interface, e.g. those that
were received from a server.
*/

const defaultResourceRequestTimeout = 5 * time.Second

func (a *Agent) processIncomingEvent(ev *event.Event) error {
	// Extract trace context from the incoming event
	ctx := tracing.ExtractTraceContext(a.context, ev.CloudEvent())

	// Create trace span for incoming event processing, continuing the trace from principal
	spanName := fmt.Sprintf("%s.%s", ev.Target().String(), ev.Type().String())
	ctx, span := tracing.Tracer().Start(ctx, spanName,
		trace.WithAttributes(
			tracing.AttrEventType.String(string(ev.Type())),
			tracing.AttrEventTarget.String(ev.Target().String()),
			tracing.AttrEventID.String(ev.EventID()),
			tracing.AttrResourceUID.String(ev.ResourceID()),
			tracing.AttrAgentMode.String(a.mode.String()),
			tracing.AttrComponentType.String("agent"),
		),
	)
	defer span.End()

	// Start measuring time for event processing
	cp := checkpoint.NewCheckpoint("process_recv_queue")

	status := metrics.EventProcessingSuccess

	// Start checkpoint step
	cp.Start(ev.Target().String())

	var err error
	switch ev.Target() {
	case event.TargetApplication:
		err = a.processIncomingApplication(ev)
	case event.TargetAppProject:
		err = a.processIncomingAppProject(ev)
	case event.TargetRepository:
		err = a.processIncomingRepository(ev)
	case event.TargetResource:
		err = a.processIncomingResourceRequest(ev)
	case event.TargetResourceResync:
		err = a.processIncomingResourceResyncEvent(ev)
	case event.TargetRedis:
		go func() {
			// Process request in a separate go routine, to avoid blocking the event thread on redis I/O
			_, redisSpan := tracing.Tracer().Start(ctx, "redis.async_processing")
			defer redisSpan.End()
			err := a.processIncomingRedisRequest(ev)
			if err != nil {
				tracing.RecordError(redisSpan, err)
				a.logGrpcEvent().WithError(err).Errorf("Unable to process incoming redis event")
			} else {
				tracing.SetSpanOK(redisSpan)
			}
		}()
	case event.TargetContainerLog:
		err = a.processIncomingContainerLogRequest(ev)
	case event.TargetTerminal:
		// Process terminal request in a separate goroutine to avoid blocking the event thread
		go func() {
			if termErr := a.processIncomingTerminalRequest(ev); termErr != nil && !isShellNotFoundError(termErr) {
				a.logGrpcEvent().WithError(termErr).Errorf("Unable to process incoming terminal event")
			}
		}()
	default:
		err = fmt.Errorf("unknown event target - processIncomingEvent: %s", ev.Target())
	}

	cp.End()

	if err != nil {
		tracing.RecordError(span, err)
	} else {
		tracing.SetSpanOK(span)
	}

	if a.metrics != nil {
		if err != nil {
			// ignore EventDiscarded errors for metrics
			if event.IsEventDiscarded(err) {
				status = metrics.EventProcessingDiscarded
			} else {
				status = metrics.EventProcessingFail
				a.metrics.AgentErrors.WithLabelValues(ev.Target().String()).Inc()
			}
		}

		// store time taken by agent to process event in metrics
		a.metrics.EventProcessingTime.WithLabelValues(string(status), string(a.mode), ev.Target().String()).Observe(cp.Duration().Seconds())
	}

	return err
}

func (a *Agent) processIncomingApplication(ev *event.Event) error {
	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})
	incomingApp, err := ev.Application()
	if err != nil {
		return err
	}

	// Determine the target namespace for the application
	targetNamespace := a.getTargetNamespaceForApp(incomingApp)
	incomingApp.SetNamespace(targetNamespace)

	var exists, sourceUIDMatch bool

	if a.mode == types.AgentModeManaged {
		// Source UID annotation is not present for apps on the autonomous agent since it is the source of truth.
		exists, sourceUIDMatch, err = a.appManager.CompareSourceUID(a.context, incomingApp)
		if err != nil {
			return fmt.Errorf("failed to compare the source UID of app: %w", err)
		}
		// In managed mode, Drop ownerReferences from the incoming resource
		// This can lead to garbage-collection of the resource on the agent cluster, if referenced owner is missing. For example, AppSet
		incomingApp.OwnerReferences = nil
	}

	switch ev.Type() {
	case event.Create:
		if exists {
			if sourceUIDMatch {
				logCtx.Debug("Received a Create event for an existing app. Updating the existing app")
				_, err := a.updateApplication(incomingApp)
				if err != nil {
					return fmt.Errorf("could not update the existing app: %w", err)
				}
				return nil
			} else {
				logCtx.Debug("An app already exists with a different source UID. Deleting the existing app")
				if err := a.deleteApplication(incomingApp); err != nil {
					return fmt.Errorf("could not delete existing app prior to creation: %w", err)
				}
			}
		}

		_, err = a.createApplication(incomingApp)
		if err != nil {
			logCtx.Errorf("Error creating application: %v", err)
		}
	case event.SpecUpdate:
		// Principal may send update events to refresh/sync the apps on the autonomous agent.
		if a.mode == types.AgentModeAutonomous {
			_, err = a.updateApplication(incomingApp)
			if err != nil {
				logCtx.Errorf("Error updating application: %v", err)
			}
			return nil
		}

		if !exists {
			logCtx.Debug("Received an Update event for an app that doesn't exist. Creating the incoming app")
			if _, err := a.createApplication(incomingApp); err != nil {
				return fmt.Errorf("could not create incoming app: %w", err)
			}
			return nil
		}

		if !sourceUIDMatch {
			logCtx.Debug("Source UID mismatch between the incoming app and existing app. Deleting the existing app")
			if err := a.deleteApplication(incomingApp); err != nil {
				return fmt.Errorf("could not delete existing app prior to creation: %w", err)
			}

			logCtx.Debug("Creating the incoming app after deleting the existing app")
			if _, err := a.createApplication(incomingApp); err != nil {
				return fmt.Errorf("could not create incoming app after deleting existing app: %w", err)
			}
			return nil
		}

		_, err = a.updateApplication(incomingApp)
		if err != nil {
			logCtx.Errorf("Error updating application: %v", err)
		}
	case event.TerminateOperation:
		logCtx.Trace("Received a TerminateOperation event")
		// Terminate a running sync operation on the agent cluster
		_, err = a.appManager.TerminateOperation(a.context, incomingApp)
		if err != nil {
			logCtx.Errorf("Error terminating application operation: %v", err)
		}
	case event.Delete:
		err = a.deleteApplication(incomingApp)
		if err != nil {
			logCtx.Errorf("Error deleting application: %v", err)
		}
	default:
		logCtx.Warnf("Received an unknown event: %s. Protocol mismatch?", ev.Type())
	}

	return err
}

func (a *Agent) processIncomingAppProject(ev *event.Event) error {
	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})
	incomingAppProject, err := ev.AppProject()
	if err != nil {
		return err
	}

	// AppProjects must exist in the same namespace as the agent
	incomingAppProject.SetNamespace(a.namespace)

	exists, sourceUIDMatch, err := a.projectManager.CompareSourceUID(a.context, incomingAppProject)
	if err != nil {
		return fmt.Errorf("failed to validate source UID of appProject: %w", err)
	}

	if a.mode == types.AgentModeManaged {
		// In managed mode, Drop ownerReferences from the incoming resource
		// This can lead to garbage-collection of the resource on the agent cluster, if referenced owner is missing. For example, AppSet
		incomingAppProject.OwnerReferences = nil
	}

	switch ev.Type() {
	case event.Create:
		if exists {
			if sourceUIDMatch {
				logCtx.Debug("Received a Create event for an existing appProject. Updating the existing appProject")
				_, err := a.updateAppProject(incomingAppProject)
				if err != nil {
					return fmt.Errorf("could not update the existing appProject: %w", err)
				}
				return nil
			} else {
				logCtx.Debug("An appProject already exists with a different source UID. Deleting the existing appProject")
				if err := a.deleteAppProject(incomingAppProject); err != nil {
					return fmt.Errorf("could not delete existing appProject prior to creation: %w", err)
				}
			}
		}

		_, err = a.createAppProject(incomingAppProject)
		if err != nil {
			logCtx.Errorf("Error creating appproject: %v", err)
		}
	case event.SpecUpdate:
		if !exists {
			logCtx.Debug("Received an Update event for an appProject that doesn't exist. Creating the incoming appProject")
			if _, err := a.createAppProject(incomingAppProject); err != nil {
				return fmt.Errorf("could not create incoming appProject: %w", err)
			}
			return nil
		}

		if !sourceUIDMatch {
			logCtx.Debug("Source UID mismatch between the incoming and existing appProject. Deleting the existing appProject")
			if err := a.deleteAppProject(incomingAppProject); err != nil {
				return fmt.Errorf("could not delete existing appProject prior to creation: %w", err)
			}

			logCtx.Debug("Creating the incoming appProject after deleting the existing appProject")
			if _, err := a.createAppProject(incomingAppProject); err != nil {
				return fmt.Errorf("could not create incoming appProject after deleting existing appProject: %w", err)
			}
			return nil
		}

		_, err = a.updateAppProject(incomingAppProject)
		if err != nil {
			logCtx.Errorf("Error updating appproject: %v", err)
		}
	case event.Delete:
		err = a.deleteAppProject(incomingAppProject)
		if err != nil {
			logCtx.Errorf("Error deleting appproject: %v", err)
		}
	default:
		logCtx.Warnf("Received an unknown event: %s. Protocol mismatch?", ev.Type())
	}

	return err
}

func (a *Agent) processIncomingRepository(ev *event.Event) error {
	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})

	incomingRepo, err := ev.Repository()
	if err != nil {
		return err
	}

	// Repository secrets must exist in the same namespace as the agent
	incomingRepo.SetNamespace(a.namespace)

	var exists, sourceUIDMatch bool

	// Source UID annotation is not present for repos on the autonomous agent since it is the source of truth.
	if a.mode == types.AgentModeManaged {
		exists, sourceUIDMatch, err = a.repoManager.CompareSourceUID(a.context, incomingRepo)
		if err != nil {
			return fmt.Errorf("failed to compare the source UID of app: %w", err)
		}
	}

	switch ev.Type() {
	case event.Create:
		if exists {
			if sourceUIDMatch {
				logCtx.Debug("Received a Create event for an existing repository. Updating the existing repository")
				_, err := a.updateRepository(incomingRepo)
				if err != nil {
					return fmt.Errorf("could not update the existing repository: %w", err)
				}
				return nil
			} else {
				logCtx.Debug("Repository already exists with a different source UID. Deleting the existing repository")
				if err := a.deleteRepository(incomingRepo); err != nil {
					return fmt.Errorf("could not delete existing repository prior to creation: %w", err)
				}
			}
		}

		_, err = a.createRepository(incomingRepo)
		if err != nil {
			logCtx.Errorf("Error creating repository: %v", err)
		}

	case event.SpecUpdate:
		if !exists {
			logCtx.Debug("Received an Update event for a repository that doesn't exist. Creating the incoming repository")
			if _, err := a.createRepository(incomingRepo); err != nil {
				return fmt.Errorf("could not create incoming repository: %w", err)
			}
			return nil
		}

		if !sourceUIDMatch {
			logCtx.Debug("Source UID mismatch between the incoming repository and existing repository. Deleting the existing repository")
			if err := a.deleteRepository(incomingRepo); err != nil {
				return fmt.Errorf("could not delete existing repository prior to creation: %w", err)
			}

			logCtx.Debug("Creating the incoming repository after deleting the existing repository")
			if _, err := a.createRepository(incomingRepo); err != nil {
				return fmt.Errorf("could not create incoming repository after deleting existing repository: %w", err)
			}
			return nil
		}

		_, err = a.updateRepository(incomingRepo)
		if err != nil {
			logCtx.Errorf("Error updating repository: %v", err)
		}

	case event.Delete:
		err = a.deleteRepository(incomingRepo)
		if err != nil {
			logCtx.Errorf("Error deleting repository: %v", err)
		}
	default:
		logCtx.Warnf("Received an unknown event: %s. Protocol mismatch?", ev.Type())
	}

	return err
}

// processIncomingResourceResyncEvent handles all the resync events that are
// exchanged with the agent/principal restarts
func (a *Agent) processIncomingResourceResyncEvent(ev *event.Event) error {
	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":      "processIncomingEvents",
		"agent":       a.remote.ClientID(),
		"mode":        a.mode,
		"event":       ev.Type(),
		"resource_id": ev.ResourceID(),
	})

	dynClient, err := dynamic.NewForConfig(a.kubeClient.RestConfig)
	if err != nil {
		return err
	}

	sendQ := a.queues.SendQ(defaultQueueName)
	if sendQ == nil {
		return fmt.Errorf("send queue not found for the default queue pair")
	}

	resyncHandler := resync.NewRequestHandler(dynClient, sendQ, a.emitter, a.resources, logCtx, manager.ManagerRoleAgent, a.namespace)
	subject := &auth.AuthSubject{}
	err = json.Unmarshal([]byte(a.remote.ClientID()), subject)
	if err != nil {
		return fmt.Errorf("failed to extract agent name from client ID: %w", err)
	}

	agentName := subject.ClientID

	switch ev.Type() {
	case event.SyncedResourceList:
		if a.mode != types.AgentModeAutonomous {
			return fmt.Errorf("agent can only handle SyncedResourceList request in the autonomous mode")
		}

		req, err := ev.RequestSyncedResourceList()
		if err != nil {
			return err
		}

		return resyncHandler.ProcessSyncedResourceListRequest(agentName, req)
	case event.ResponseSyncedResource:
		if a.mode != types.AgentModeManaged {
			return fmt.Errorf("agent can only handle SyncedResource request in the managed mode")
		}

		req, err := ev.SyncedResource()
		if err != nil {
			return err
		}

		return resyncHandler.ProcessIncomingSyncedResource(a.context, req, agentName)
	case event.EventRequestUpdate:
		if a.mode != types.AgentModeAutonomous {
			return fmt.Errorf("agent can only handle RequestUpdate in the autonomous mode")
		}

		incoming, err := ev.RequestUpdate()
		if err != nil {
			return err
		}

		// For autonomous agents, the principal stores AppProjects with a prefixed name (agent-name + "-" + project-name).
		// When the principal sends a RequestUpdate, it uses the prefixed name. We need to strip the prefix
		// before looking up the resource locally.
		if incoming.Kind == "AppProject" {
			prefix := agentName + "-"
			if len(incoming.Name) > len(prefix) && incoming.Name[:len(prefix)] == prefix {
				incoming.Name = incoming.Name[len(prefix):]
			}
		}

		return resyncHandler.ProcessRequestUpdateEvent(a.context, agentName, incoming)
	case event.EventRequestResourceResync:
		if a.mode != types.AgentModeManaged {
			return fmt.Errorf("agent can only handle ResourceResync request in the managed mode")
		}

		return resyncHandler.ProcessIncomingResourceResyncRequest(a.context, agentName)
	default:
		return fmt.Errorf("invalid type of resource resync: %s", ev.Type())
	}
}

// createApplication creates an Application upon an event in the agent's work
// queue.
func (a *Agent) createApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	// Determine the target namespace for the application
	targetNamespace := a.getTargetNamespaceForApp(incoming)
	incoming.SetNamespace(targetNamespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "CreateApplication",
		"app":    incoming.QualifiedName(),
	})

	// In modes other than "managed", we don't process new application events
	// that are incoming.
	if a.mode != types.AgentModeManaged {
		logCtx.Info("Discarding this event, because agent is not in managed mode")
		return nil, event.NewEventDiscardedErr("cannot create application: agent is not in managed mode")
	}

	// If we receive a new app event for an app we already manage, it usually
	// means that we're out-of-sync from the control plane.
	//
	// TODO(jannfis): Handle this situation properly instead of throwing an error.
	if a.appManager.IsManaged(incoming.QualifiedName()) {
		logCtx.Info("Discarding this event, because application is already managed on this agent")
		return nil, event.NewEventDiscardedErr("application %s is already managed", incoming.QualifiedName())
	}

	// Create the namespace if it doesn't exist and the option is enabled
	if a.createNamespace && a.destinationBasedMapping {
		if err := a.ensureNamespaceExists(targetNamespace); err != nil {
			return nil, fmt.Errorf("failed to ensure namespace %s exists: %w", targetNamespace, err)
		}
	}

	logCtx.Infof("Creating a new application on behalf of an incoming event")

	// Get rid of some fields that we do not want to have on the application
	// as we start fresh.
	if incoming.Annotations != nil {
		delete(incoming.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	}

	// Set target cluster to a sensible value
	// TODO(jannfis): Make this actually configurable per agent
	incoming.Spec.Destination.Server = ""
	incoming.Spec.Destination.Name = "in-cluster"

	if a.mode == types.AgentModeManaged {
		// Store app spec in cache
		a.sourceCache.Application.Set(incoming.UID, incoming.Spec)
	}

	created, err := a.appManager.Create(a.context, incoming)
	if apierrors.IsAlreadyExists(err) {
		logCtx.Debug("application already exists")
		return created, nil
	}

	return created, err
}

func (a *Agent) updateApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	// Determine the target namespace for the application
	targetNamespace := a.getTargetNamespaceForApp(incoming)
	incoming.SetNamespace(targetNamespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":          "UpdateApplication",
		"app":             incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})

	if a.appManager.IsChangeIgnored(incoming.QualifiedName(), incoming.ResourceVersion) {
		logCtx.Tracef("Discarding this event, because agent has seen this version %s already", incoming.ResourceVersion)
		return nil, event.NewEventDiscardedErr("the version %s has already been seen by this agent", incoming.ResourceVersion)
	} else {
		logCtx.Tracef("New resource version: %s", incoming.ResourceVersion)
	}

	// Create the namespace if it doesn't exist and the option is enabled
	if a.createNamespace && a.destinationBasedMapping {
		if err := a.ensureNamespaceExists(targetNamespace); err != nil {
			logCtx.WithError(err).Errorf("Failed to ensure namespace %s exists", targetNamespace)
			return nil, fmt.Errorf("failed to ensure namespace %s exists: %w", targetNamespace, err)
		}
	}

	// Set target cluster to a sensible value
	// TODO(jannfis): Make this actually configurable per agent
	incoming.Spec.Destination.Server = ""
	incoming.Spec.Destination.Name = "in-cluster"

	logCtx.Infof("Updating application")

	var err error
	var napp *v1alpha1.Application
	switch a.mode {
	case types.AgentModeManaged:

		// Update app spec in cache
		logCtx.Tracef("Calling update spec for this event")
		a.sourceCache.Application.Set(incoming.UID, incoming.Spec)

		napp, err = a.appManager.UpdateManagedApp(a.context, incoming)
	case types.AgentModeAutonomous:
		logCtx.Tracef("Calling update operation for this event")
		napp, err = a.appManager.UpdateOperation(a.context, incoming)
	default:
		err = fmt.Errorf("unknown operation mode: %s", a.mode)
	}
	return napp, err
}

func (a *Agent) deleteApplication(app *v1alpha1.Application) error {
	// Determine the target namespace for the application
	targetNamespace := a.getTargetNamespaceForApp(app)
	app.SetNamespace(targetNamespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "DeleteApplication",
		"app":    app.QualifiedName(),
	})

	// If we receive an update app event for an app we don't know about yet it
	// means that we're out-of-sync from the control plane.
	//
	// TODO(jannfis): Handle this situation properly instead of throwing an error.
	if !a.appManager.IsManaged(app.QualifiedName()) {
		return fmt.Errorf("application %s is not managed", app.QualifiedName())
	}

	logCtx.Infof("Deleting application")

	// Fetch the source UID of the existing app to mark it as expected deletion.
	app, err := a.appManager.Get(a.context, app.Name, app.Namespace)
	if err != nil {
		return err
	}

	sourceUID := app.Annotations[manager.SourceUIDAnnotation]
	a.deletions.MarkExpected(ktypes.UID(sourceUID))

	deletionPropagation := backend.DeletePropagationBackground
	err = a.appManager.Delete(a.context, a.namespace, app, &deletionPropagation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logCtx.Debug("application is not found, perhaps it is already deleted")
			if a.mode == types.AgentModeManaged {
				a.sourceCache.Application.Delete(app.UID)
			}
			return nil
		}
		return err
	}

	if a.mode == types.AgentModeManaged {
		a.sourceCache.Application.Delete(app.UID)
	}

	err = a.appManager.Unmanage(app.QualifiedName())
	if err != nil {
		a.logGrpcEvent().Warnf("Could not unmanage app %s: %v", app.QualifiedName(), err)
	}
	return nil
}

// createAppProject creates an AppProject upon an event in the agent's work
// queue.
func (a *Agent) createAppProject(incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	// AppProjects must exist in the same namespace as the agent
	incoming.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":     "CreateAppProject",
		"appProject": incoming.Name,
	})

	// In modes other than "managed", we don't process new AppProject events
	// that are incoming.
	if a.mode != types.AgentModeManaged {
		logCtx.Trace("Discarding this event, because agent is not in managed mode")
		return nil, event.NewEventDiscardedErr("cannot create appproject: agent is not in managed mode")
	}

	// If we receive a new AppProject event for an AppProject we already manage, it usually
	// means that we're out-of-sync from the control plane.
	if a.projectManager.IsManaged(incoming.Name) {
		logCtx.Trace("AppProject is already managed on this agent. Updating the existing AppProject")
		return a.updateAppProject(incoming)
	}

	logCtx.Infof("Creating a new AppProject on behalf of an incoming event")

	a.sourceCache.AppProject.Set(incoming.UID, incoming.Spec)

	// Get rid of some fields that we do not want to have on the AppProject
	// as we start fresh.
	if incoming.Annotations != nil {
		delete(incoming.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	}

	created, err := a.projectManager.Create(a.context, incoming)
	if apierrors.IsAlreadyExists(err) {
		logCtx.Debug("appProject already exists")
	}
	return created, err
}

func (a *Agent) updateAppProject(incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	// AppProjects must exist in the same namespace as the agent
	incoming.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":          "UpdateAppProject",
		"appProject":      incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	if !a.projectManager.IsManaged(incoming.Name) {
		logCtx.Trace("AppProject is not managed on this agent. Creating the new AppProject")
		return a.createAppProject(incoming)
	}

	if a.projectManager.IsChangeIgnored(incoming.Name, incoming.ResourceVersion) {
		logCtx.Tracef("Discarding this event, because agent has seen this version %s already", incoming.ResourceVersion)
		return nil, event.NewEventDiscardedErr("the version %s has already been seen by this agent", incoming.ResourceVersion)
	} else {
		logCtx.Tracef("New resource version: %s", incoming.ResourceVersion)
	}

	logCtx.Infof("Updating appProject")

	a.sourceCache.AppProject.Set(incoming.UID, incoming.Spec)

	logCtx.Tracef("Calling update spec for this event")
	return a.projectManager.UpdateAppProject(a.context, incoming)
}

func (a *Agent) deleteAppProject(project *v1alpha1.AppProject) error {
	// AppProjects must exist in the same namespace as the agent
	project.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":     "DeleteAppProject",
		"appProject": project.Name,
	})

	// If we receive an update appproject event for an AppProject we don't know about yet it
	// means that we're out-of-sync from the control plane.
	//
	// TODO(jannfis): Handle this situation properly instead of throwing an error.
	if !a.projectManager.IsManaged(project.Name) {
		return fmt.Errorf("appProject %s is not managed", project.Name)
	}

	logCtx.Infof("Deleting appProject")

	// Fetch the source UID of the existing appProject to mark it as expected deletion.
	project, err := a.projectManager.Get(a.context, project.Name, project.Namespace)
	if err != nil {
		return err
	}

	sourceUID := project.Annotations[manager.SourceUIDAnnotation]
	a.deletions.MarkExpected(ktypes.UID(sourceUID))

	deletionPropagation := backend.DeletePropagationBackground
	err = a.projectManager.Delete(a.context, project, &deletionPropagation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logCtx.Debug("appProject not found, perhaps it is already deleted")
			a.sourceCache.AppProject.Delete(project.UID)
			return nil
		}
		return err
	}

	a.sourceCache.AppProject.Delete(project.UID)

	err = a.projectManager.Unmanage(project.Name)
	if err != nil {
		a.logGrpcEvent().Warnf("Could not unmanage appProject %s: %v", project.Name, err)
	}
	return nil
}

// createRepository creates a Repository upon an event in the agent's work queue.
func (a *Agent) createRepository(incoming *corev1.Secret) (*corev1.Secret, error) {
	// Repository secrets must exist in the same namespace as the agent
	incoming.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "CreateRepository",
		"repo":   incoming.Name,
	})

	// In modes other than "managed", we don't process new repository events
	// that are incoming.
	if a.mode.IsAutonomous() {
		logCtx.Info("Discarding this event, because agent is not in managed mode")
		return nil, event.NewEventDiscardedErr("cannot create repository: agent is not in managed mode")
	}

	// If we receive a new Repository event for a Repository we already manage, it usually
	// means that we're out-of-sync from the control plane.
	if a.repoManager.IsManaged(incoming.Name) {
		logCtx.Trace("Repository is already managed on this agent. Updating the existing Repository")
		return a.updateRepository(incoming)
	}

	logCtx.Infof("Creating a new repository on behalf of an incoming event")

	if incoming.Annotations == nil {
		incoming.Annotations = make(map[string]string)
	}

	a.sourceCache.Repository.Set(incoming.UID, incoming.Data)

	// Get rid of some fields that we do not want to have on the repository as we start fresh.
	delete(incoming.Annotations, "kubectl.kubernetes.io/last-applied-configuration")

	created, err := a.repoManager.Create(a.context, incoming)
	if apierrors.IsAlreadyExists(err) {
		logCtx.Debug("repository already exists")
		return created, nil
	}

	return created, err
}

func (a *Agent) updateRepository(incoming *corev1.Secret) (*corev1.Secret, error) {
	// Repository secrets must exist in the same namespace as the agent
	incoming.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method":          "UpdateRepository",
		"repo":            incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	if !a.repoManager.IsManaged(incoming.Name) {
		logCtx.Trace("Repository is not managed on this agent. Creating the new Repository")
		return a.createRepository(incoming)
	}

	if a.repoManager.IsChangeIgnored(incoming.Name, incoming.ResourceVersion) {
		logCtx.Tracef("Discarding this event, because agent has seen this version %s already", incoming.ResourceVersion)
		return nil, event.NewEventDiscardedErr("the version %s has already been seen by this agent", incoming.ResourceVersion)
	} else {
		logCtx.Tracef("New resource version: %s", incoming.ResourceVersion)
	}

	logCtx.Infof("Updating repository")

	a.sourceCache.Repository.Set(incoming.UID, incoming.Data)

	return a.repoManager.UpdateManagedRepository(a.context, incoming)
}

func (a *Agent) deleteRepository(repo *corev1.Secret) error {
	// Repository secrets must exist in the same namespace as the agent
	repo.SetNamespace(a.namespace)

	logCtx := a.logGrpcEvent().WithFields(logrus.Fields{
		"method": "DeleteRepository",
		"repo":   repo.Name,
	})

	if !a.repoManager.IsManaged(repo.Name) {
		return fmt.Errorf("repository %s is not managed", repo.Name)
	}

	logCtx.Infof("Deleting repository")

	// Fetch the source UID of the existing repository to mark it as expected deletion.
	repo, err := a.repoManager.Get(a.context, repo.Name, repo.Namespace)
	if err != nil {
		return err
	}

	sourceUID := repo.Annotations[manager.SourceUIDAnnotation]
	a.deletions.MarkExpected(ktypes.UID(sourceUID))

	deletionPropagation := backend.DeletePropagationBackground
	err = a.repoManager.Delete(a.context, repo.Name, repo.Namespace, &deletionPropagation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logCtx.Debug("repository is not found, perhaps it is already deleted")
			a.sourceCache.Repository.Delete(repo.UID)
			return nil
		}
		return err
	}

	a.sourceCache.Repository.Delete(repo.UID)

	err = a.repoManager.Unmanage(repo.Name)
	if err != nil {
		a.logGrpcEvent().Warnf("Could not unmanage repository %s: %v", repo.Name, err)
	}

	return nil
}

// getTargetNamespaceForApp returns the namespace where the application should
// be created on the agent. If destinationBasedMapping is enabled, it returns
// the original namespace from the principal. Otherwise, it returns the agent's
// namespace.
func (a *Agent) getTargetNamespaceForApp(app *v1alpha1.Application) string {
	if a.destinationBasedMapping && app.Namespace != "" {
		return app.Namespace
	}
	return a.namespace
}

// ensureNamespaceExists creates the namespace if it doesn't exist.
// This is used when destinationBasedMapping is enabled to ensure
// the target namespace exists before creating applications.
func (a *Agent) ensureNamespaceExists(namespace string) error {
	// Check if namespace already exists
	_, err := a.kubeClient.Clientset.CoreV1().Namespaces().Get(a.context, namespace, metav1.GetOptions{})
	if err == nil {
		// Namespace already exists
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check if namespace %s exists: %w", namespace, err)
	}

	// Create the namespace
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}

	_, err = a.kubeClient.Clientset.CoreV1().Namespaces().Create(a.context, ns, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Namespace was created by another process, that's fine
			return nil
		}
		return fmt.Errorf("failed to create namespace %s: %w", namespace, err)
	}

	log().Infof("Created namespace %s", namespace)
	return nil
}
