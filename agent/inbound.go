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

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/*
This file contains a collection of callbacks to process inbound events.

Inbound events are those coming through our gRPC interface, e.g. those that
were received from a server.
*/

func (a *Agent) processIncomingEvent(ev *event.Event) error {
	var err error
	switch ev.Target() {
	case event.TargetApplication:
		err = a.processIncomingApplication(ev)
	case event.TargetAppProject:
	default:
		err = fmt.Errorf("unknown event target: %s", ev.Target())
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

	switch ev.Type() {
	case event.Create:
		_, err = a.createApplication(incomingApp)
		if err != nil {
			logCtx.Errorf("Error creating application: %v", err)
		}
	case event.SpecUpdate:
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
		logCtx.Trace("Discarding this event, because agent is not in managed mode")
		return nil, fmt.Errorf("cannot create application: agent is not in managed mode")
	}

	// If we receive a new app event for an app we already manage, it usually
	// means that we're out-of-sync from the control plane.
	//
	// TODO(jannfis): Handle this situation properly instead of throwing an error.
	if a.appManager.IsManaged(incoming.QualifiedName()) {
		logCtx.Trace("Discarding this event, because application is already managed on this agent")
		return nil, fmt.Errorf("application %s is already managed", incoming.QualifiedName())
	}

	logCtx.Infof("Creating a new application on behalf of an incoming event")

	// Get rid of some fields that we do not want to have on the application
	// as we start fresh.
	if incoming.Annotations != nil {
		delete(incoming.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	}

	created, err := a.appManager.Create(a.context, incoming)
	return created, err
}

func (a *Agent) updateApplication(incoming *v1alpha1.Application) (*v1alpha1.Application, error) {
	//
	incoming.ObjectMeta.SetNamespace(a.namespace)
	logCtx := log().WithFields(logrus.Fields{
		"method":          "UpdateApplication",
		"app":             incoming.QualifiedName(),
		"resourceVersion": incoming.ResourceVersion,
	})
	if a.appManager.IsChangeIgnored(incoming.QualifiedName(), incoming.ResourceVersion) {
		logCtx.Tracef("Discarding this event, because agent has seen this version %s already", incoming.ResourceVersion)
		return nil, fmt.Errorf("the version %s has already been seen by this agent", incoming.ResourceVersion)
	} else {
		logCtx.Tracef("New resource version: %s", incoming.ResourceVersion)
	}

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

	delPol := v1.DeletePropagationBackground
	err := a.appclient.ArgoprojV1alpha1().Applications(a.namespace).Delete(a.context, app.Name, v1.DeleteOptions{PropagationPolicy: &delPol})
	if err != nil {
		return err
	}
	err = a.appManager.Unmanage(app.QualifiedName())
	if err != nil {
		log().Warnf("Could not unmanage app %s: %v", app.QualifiedName(), err)
	}
	return nil
}
