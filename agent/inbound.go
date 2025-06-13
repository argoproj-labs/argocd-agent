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
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	appCache "github.com/argoproj-labs/argocd-agent/internal/cache"
	"github.com/argoproj-labs/argocd-agent/internal/checkpoint"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/metrics"
	"github.com/argoproj-labs/argocd-agent/internal/resync"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
)

/*
This file contains a collection of callbacks to process inbound events.

Inbound events are those coming through our gRPC interface, e.g. those that
were received from a server.
*/

const defaultResourceRequestTimeout = 5 * time.Second

func (a *Agent) processIncomingEvent(ev *event.Event) error {

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
	case event.TargetResource:
		err = a.processIncomingResourceRequest(ev)
	case event.TargetResourceResync:
		err = a.processIncomingResourceResyncEvent(ev)
	default:
		err = fmt.Errorf("unknown event target: %s", ev.Target())
	}

	cp.End()

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
	logCtx := log().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})
	incomingApp, err := ev.Application()
	if err != nil {
		return err
	}

	exists, sourceUIDMatch, err := a.appManager.CompareSourceUID(a.context, incomingApp)
	if err != nil {
		return fmt.Errorf("failed to compare the source UID of app: %w", err)
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
	logCtx := log().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})
	incomingAppProject, err := ev.AppProject()
	if err != nil {
		return err
	}

	exists, sourceUIDMatch, err := a.projectManager.CompareSourceUID(a.context, incomingAppProject)
	if err != nil {
		return fmt.Errorf("failed to validate source UID of appProject: %w", err)
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

// processIncomingResourceResyncEvent handles all the resync events that are
// exchanged with the agent/principal restarts
func (a *Agent) processIncomingResourceResyncEvent(ev *event.Event) error {
	logCtx := log().WithFields(logrus.Fields{
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

	resyncHandler := resync.NewRequestHandler(dynClient, sendQ, a.emitter, a.resources, logCtx)

	switch ev.Type() {
	case event.SyncedResourceList:
		if a.mode != types.AgentModeAutonomous {
			return fmt.Errorf("agent can only handle SyncedResourceList request in the autonomous mode")
		}

		req, err := ev.RequestSyncedResourceList()
		if err != nil {
			return err
		}

		return resyncHandler.ProcessSyncedResourceListRequest(a.remote.ClientID(), req)
	case event.ResponseSyncedResource:
		if a.mode != types.AgentModeManaged {
			return fmt.Errorf("agent can only handle SyncedResource request in the managed mode")
		}

		req, err := ev.SyncedResource()
		if err != nil {
			return err
		}

		return resyncHandler.ProcessIncomingSyncedResource(a.context, req, a.remote.ClientID())
	case event.EventRequestUpdate:
		if a.mode != types.AgentModeAutonomous {
			return fmt.Errorf("agent can only handle RequestUpdate in the autonomous mode")
		}

		incoming, err := ev.RequestUpdate()
		if err != nil {
			return err
		}

		incoming.Namespace = a.namespace

		return resyncHandler.ProcessRequestUpdateEvent(a.context, a.remote.ClientID(), incoming)
	case event.EventRequestResourceResync:
		if a.mode != types.AgentModeManaged {
			return fmt.Errorf("agent can only handle ResourceResync request in the managed mode")
		}

		return resyncHandler.ProcessIncomingResourceResyncRequest(a.context, a.remote.ClientID())
	default:
		return fmt.Errorf("invalid type of resource resync: %s", ev.Type())
	}
}

// createApplication creates an Application upon an event in the agent's work
// queue.
func (a *Agent) createApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	incoming.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
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

	created, err := a.appManager.Create(a.context, incoming)
	if apierrors.IsAlreadyExists(err) {
		logCtx.Debug("application already exists")
		return created, nil
	}

	if a.mode == types.AgentModeManaged && err == nil {
		// Store app spec in cache
		appCache.SetApplicationSpec(incoming.UID, incoming.Spec)
	}
	return created, err
}

func (a *Agent) updateApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	incoming.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
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

	// Set target cluster to a sensible value
	// TODO(jannfis): Make this actually configurable per agent
	incoming.Spec.Destination.Server = ""
	incoming.Spec.Destination.Name = "in-cluster"

	logCtx.Infof("Updating application")

	var err error
	var napp *v1alpha1.Application
	switch a.mode {
	case types.AgentModeManaged:
		logCtx.Tracef("Calling update spec for this event")
		napp, err = a.appManager.UpdateManagedApp(a.context, incoming)
		if err == nil {
			// Update app spec in cache
			appCache.SetApplicationSpec(incoming.UID, napp.Spec)
		}
	case types.AgentModeAutonomous:
		logCtx.Tracef("Calling update operation for this event")
		napp, err = a.appManager.UpdateOperation(a.context, incoming)
	default:
		err = fmt.Errorf("unknown operation mode: %s", a.mode)
	}
	return napp, err
}

func (a *Agent) deleteApplication(app *v1alpha1.Application) error {
	app.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
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

	deletionPropagation := backend.DeletePropagationBackground
	err := a.appManager.Delete(a.context, a.namespace, app, &deletionPropagation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logCtx.Debug("application is not found, perhaps it is already deleted")
			if a.mode == types.AgentModeManaged {
				appCache.DeleteApplicationSpec(app.UID)
			}
			return nil
		}
		return err
	}

	if a.mode == types.AgentModeManaged && err == nil {
		appCache.DeleteApplicationSpec(app.UID)
	}

	err = a.appManager.Unmanage(app.QualifiedName())
	if err != nil {
		log().Warnf("Could not unmanage app %s: %v", app.QualifiedName(), err)
	}
	return nil
}

// createAppProject creates an AppProject upon an event in the agent's work
// queue.
func (a *Agent) createAppProject(incoming *v1alpha1.AppProject) (*v1alpha1.AppProject, error) {
	incoming.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
		"method": "CreateAppProject",
		"app":    incoming.Name,
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
		logCtx.Trace("Discarding this event, because AppProject is already managed on this agent")
		return nil, event.NewEventDiscardedErr("appproject %s is already managed", incoming.Name)
	}

	logCtx.Infof("Creating a new AppProject on behalf of an incoming event")

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
	//
	incoming.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
		"method":          "UpdateAppProject",
		"app":             incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	if a.projectManager.IsChangeIgnored(incoming.Name, incoming.ResourceVersion) {
		logCtx.Tracef("Discarding this event, because agent has seen this version %s already", incoming.ResourceVersion)
		return nil, event.NewEventDiscardedErr("the version %s has already been seen by this agent", incoming.ResourceVersion)
	} else {
		logCtx.Tracef("New resource version: %s", incoming.ResourceVersion)
	}

	logCtx.Infof("Updating appProject")

	logCtx.Tracef("Calling update spec for this event")
	return a.projectManager.UpdateAppProject(a.context, incoming)

}

func (a *Agent) deleteAppProject(project *v1alpha1.AppProject) error {
	project.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
		"method": "DeleteAppProject",
		"app":    project.Name,
	})

	// If we receive an update appproject event for an AppProject we don't know about yet it
	// means that we're out-of-sync from the control plane.
	//
	// TODO(jannfis): Handle this situation properly instead of throwing an error.
	if !a.projectManager.IsManaged(project.Name) {
		return fmt.Errorf("appProject %s is not managed", project.Name)
	}

	logCtx.Infof("Deleting appProject")

	deletionPropagation := backend.DeletePropagationBackground
	err := a.projectManager.Delete(a.context, a.namespace, project, &deletionPropagation)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logCtx.Debug("appProject not found, perhaps it is already deleted")
			return nil
		}
		return err
	}
	err = a.projectManager.Unmanage(project.Name)
	if err != nil {
		log().Warnf("Could not unmanage appProject %s: %v", project.Name, err)
	}
	return nil
}
