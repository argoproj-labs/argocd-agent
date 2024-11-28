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
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

/*
This file contains a collection of callbacks to process inbound events.

Inbound events are those coming through our gRPC interface, e.g. those that
were received from a server.
*/

const defaultResourceRequestTimeout = 5 * time.Second

func (a *Agent) processIncomingEvent(ev *event.Event) error {
	var err error
	switch ev.Target() {
	case event.TargetApplication:
		err = a.processIncomingApplication(ev)
	case event.TargetAppProject:
		err = a.processIncomingAppProject(ev)
	case event.TargetResource:
		err = a.processIncomingResourceRequest(ev)
	default:
		err = fmt.Errorf("unknown event target: %s", ev.Target())
	}

	return err
}

// processIncomingResourceRequest processes an incoming event that requests
// to retrieve information from the Kubernetes API.
//
// There can be multiple forms of requests. Currently supported are:
//
// - Request for a particular resource, both namespace and cluster scoped
// - Request for a list of resources of a particular kind (e.g. configmaps,
//   pods, etc), both namespace and custer scoped
// - Request for a list of available APIs and

func (a *Agent) processIncomingResourceRequest(ev *event.Event) error {
	rreq, err := ev.ResourceRequest()
	if err != nil {
		return err
	}
	logCtx := log().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
		"uuid":   rreq.UUID,
	})
	logCtx.Tracef("Start processing %v", rreq)

	// Create a dynamic kubernetes client and retrieve the resource from the
	// cluster.
	dynClient, err := dynamic.NewForConfig(a.kubeClient.RestConfig)
	if err != nil {
		return fmt.Errorf("could not create a dynamic client: %w", err)
	}

	// Some of GVR may be empty, that's ok
	gvk := schema.GroupVersionResource{Group: rreq.Group, Version: rreq.Version, Resource: rreq.Resource}
	rif := dynClient.Resource(gvk)

	ctx, cancel := context.WithTimeout(a.context, defaultResourceRequestTimeout)
	defer cancel()

	var jsonres []byte
	var unres *unstructured.Unstructured
	var unlist *unstructured.UnstructuredList
	status := ""
	if rreq.Name != "" {
		if rreq.Namespace != "" {
			unres, err = rif.Namespace(rreq.Namespace).Get(ctx, rreq.Name, v1.GetOptions{})
		} else {
			unres, err = rif.Get(ctx, rreq.Name, v1.GetOptions{})
		}
	} else {
		if rreq.Namespace != "" {
			unlist, err = rif.Namespace(rreq.Namespace).List(ctx, v1.ListOptions{})
		} else {
			unlist, err = rif.List(ctx, v1.ListOptions{})
		}
	}
	if err != nil {
		logCtx.Errorf("could not request resource: %v", err)
		status = "failure"
	} else {
		// Marshal the unstructured resource to JSON for submission
		if unres != nil {
			jsonres, err = json.Marshal(unres)
		} else if unlist != nil {
			jsonres, err = json.Marshal(unlist)
		}
		if err != nil {
			return fmt.Errorf("could not marshal resource to json: %w", err)
		}
		logCtx.Tracef("marshaled resource")
	}

	q := a.queues.SendQ(a.remote.ClientID())
	q.Add(a.emitter.NewResourceResponseEvent(rreq.UUID, status, string(jsonres)))
	logCtx.Tracef("Emitted resource response")

	return nil
}

func (a *Agent) processIncomingApplication(ev *event.Event) error {
	logCtx := log().WithFields(logrus.Fields{
		"method": "processIncomingEvents",
	})
	incomingApp, err := ev.Application()
	if err != nil {
		return err
	}

	sourceUIDMatch, err := a.appManager.CompareSourceUID(a.context, incomingApp)
	if err != nil {
		return fmt.Errorf("failed to compare the source UID of app: %w", err)
	}

	switch ev.Type() {
	case event.Create:
		if !sourceUIDMatch {
			logCtx.Debug("An app already exists with a different source UID. Deleting the existing app")
			if err := a.deleteApplication(incomingApp); err != nil {
				return fmt.Errorf("could not delete existing app prior to creation: %w", err)
			}
		}

		_, err = a.createApplication(incomingApp)
		if err != nil {
			logCtx.Errorf("Error creating application: %v", err)
		}
	case event.SpecUpdate:
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

	sourceUIDMatch, err := a.projectManager.CompareSourceUID(a.context, incomingAppProject)
	if err != nil {
		return fmt.Errorf("failed to validate source UID of appProject: %w", err)
	}

	switch ev.Type() {
	case event.Create:
		if !sourceUIDMatch {
			logCtx.Debug("An appProject already exists with a different source UID. Deleting the existing appProject")
			if err := a.deleteAppProject(incomingAppProject); err != nil {
				return fmt.Errorf("could not delete existing appProject prior to creation: %w", err)
			}
		}

		_, err = a.createAppProject(incomingAppProject)
		if err != nil {
			logCtx.Errorf("Error creating appproject: %v", err)
		}
	case event.SpecUpdate:
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

// createApplication creates an Application upon an event in the agent's work
// queue.
func (a *Agent) createApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	incoming.ObjectMeta.SetNamespace(a.namespace)
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
	incoming.Spec.Destination.Server = ""
	incoming.Spec.Destination.Name = "in-cluster"

	created, err := a.appManager.Create(a.context, incoming)
	if apierrors.IsAlreadyExists(err) {
		logCtx.Debug("application already exists")
		return created, nil
	}

	return created, err
}

func (a *Agent) updateApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	incoming.ObjectMeta.SetNamespace(a.namespace)
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
	incoming.Spec.Destination.Server = ""
	incoming.Spec.Destination.Name = "in-cluster"

	logCtx.Infof("Updating application")

	var err error
	var napp *v1alpha1.Application
	if a.mode == types.AgentModeManaged {
		logCtx.Tracef("Calling update spec for this event")
		napp, err = a.appManager.UpdateManagedApp(a.context, incoming)
	} else if a.mode == types.AgentModeAutonomous {
		logCtx.Tracef("Calling update operation for this event")
		napp, err = a.appManager.UpdateOperation(a.context, incoming)
	} else {
		err = fmt.Errorf("unknown operation mode: %s", a.mode)
	}
	return napp, err
}

func (a *Agent) deleteApplication(app *v1alpha1.Application) error {
	app.ObjectMeta.SetNamespace(a.namespace)
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
			return nil
		}
		return err
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
	incoming.ObjectMeta.SetNamespace(a.namespace)
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
	if a.appManager.IsManaged(incoming.Name) {
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
	incoming.ObjectMeta.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
		"method":          "UpdateAppProject",
		"app":             incoming.Name,
		"resourceVersion": incoming.ResourceVersion,
	})

	if a.appManager.IsChangeIgnored(incoming.Name, incoming.ResourceVersion) {
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
	project.ObjectMeta.SetNamespace(a.namespace)
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
