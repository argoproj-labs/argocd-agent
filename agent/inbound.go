package agent

import (
	"fmt"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/*
This file contains a collection of callbacks to process inbound events.

Inbound events are those coming through our gRPC interface, e.g. those that
were received from a server.
*/

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
		err = fmt.Errorf("unknown operation mode: %d", a.mode)
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
	a.appManager.Unmanage(app.QualifiedName())
	return nil
}

func (a *Agent) copyAndMutateApp(app *v1alpha1.Application, create bool) *v1alpha1.Application {
	rapp := &v1alpha1.Application{}
	rapp.ObjectMeta.Annotations = app.ObjectMeta.Annotations
	rapp.ObjectMeta.Labels = app.ObjectMeta.Labels
	rapp.ObjectMeta.Namespace = a.namespace
	rapp.Spec = *app.Spec.DeepCopy()
	rapp.Operation = app.Operation.DeepCopy()
	if rapp.ObjectMeta.Annotations != nil {
		delete(rapp.ObjectMeta.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
	}
	return rapp
}
