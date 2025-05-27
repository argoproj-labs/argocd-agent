package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const ownerLookupRecursionLimit = 5

var ErrUnmanaged = errors.New("resource not managed by app")

// processIncomingResourceRequest processes an incoming event that requests
// to retrieve information from the Kubernetes API.
//
// There can be multiple forms of requests. Currently supported are:
//
// - Request for a particular resource, both namespace and cluster scoped
// - Request for a list of available APIs
func (a *Agent) processIncomingResourceRequest(ev *event.Event) error {
	rreq, err := ev.ResourceRequest()
	if err != nil {
		return err
	}
	logCtx := log().WithFields(logrus.Fields{
		"method": "processIncomingResourceRequest",
		"uuid":   rreq.UUID,
	})

	if rreq.IsEmpty() {
		return fmt.Errorf("group version resource is empty")
	}

	logCtx.Tracef("Start processing %v", rreq)

	ctx, cancel := context.WithTimeout(a.context, defaultResourceRequestTimeout)
	defer cancel()

	var jsonres []byte
	var unres *unstructured.Unstructured
	var unlist *unstructured.UnstructuredList
	var status error

	// Extract required information from the resource request
	gvr, name, namespace := rreqEventToGvk(rreq)

	// If we have a request for a named resource, we fetch that particular
	// resource. If the name is empty, we fetch a list of resources instead.
	if name != "" {
		unres, err = a.getManagedResource(ctx, gvr, name, namespace)
	} else {
		unlist, err = a.getAvailableResources(ctx, gvr, namespace)
	}
	if err != nil {
		logCtx.Errorf("could not request resource: %v", err)
		status = err
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

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Remote queue disappeared")
		return nil
	}
	q.Add(a.emitter.NewResourceResponseEvent(rreq.UUID, event.HttpStatusFromError(status), string(jsonres)))
	logCtx.Tracef("Emitted resource response")

	return nil
}

// rreqEventToGvk returns a GroupVersionResource object, name and namespace for
// the resource requested in rreq.
func rreqEventToGvk(rreq *event.ResourceRequest) (gvr schema.GroupVersionResource, name string, namespace string) {
	gvr.Group = rreq.Group
	gvr.Resource = rreq.Resource
	gvr.Version = rreq.Version
	name = rreq.Name
	namespace = rreq.Namespace
	return
}

// getManagedResource retrieves a single resource from the cluster
// and returns it as unstructured data. The resource to be returned must be
// managed by Argo CD.
func (a *Agent) getManagedResource(ctx context.Context, gvr schema.GroupVersionResource, name, namespace string) (*unstructured.Unstructured, error) {
	var err error
	var res *unstructured.Unstructured

	rif := a.kubeClient.DynamicClient.Resource(gvr)

	if namespace != "" {
		res, err = rif.Namespace(namespace).Get(ctx, name, v1.GetOptions{})
	} else {
		res, err = rif.Get(ctx, name, v1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}
	ok, err := isResourceManaged(a.kubeClient, res, ownerLookupRecursionLimit)
	if !ok {
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrUnmanaged, err)
		} else {
			err = fmt.Errorf("%w: %w", apierrors.NewForbidden(gvr.GroupResource(), name, ErrUnmanaged), ErrUnmanaged)
		}
	}
	return res, err
}

// getAvailableResources retrieves a list of available resources for a given
// combination of group and version specified in gvr. The resource in the gvr
// must be empty, otherwise an error is returned.
func (a *Agent) getAvailableResources(ctx context.Context, gvr schema.GroupVersionResource, namespace string) (*unstructured.UnstructuredList, error) {
	var err error
	var res *unstructured.UnstructuredList

	// We do not want resources to be listed, only APIs
	switch gvr.Resource {
	case "apiresources":
	case "apigroups":
	case "":
		break
	default:
		return nil, fmt.Errorf("not authorized to list resources: %s", gvr.String())
	}

	rif := a.kubeClient.DynamicClient.Resource(gvr)

	if namespace != "" {
		res, err = rif.Namespace(namespace).List(ctx, v1.ListOptions{})
	} else {
		res, err = rif.List(ctx, v1.ListOptions{})
	}

	return res, err
}

// isResourceManaged checks whether a given resource is considered to be
// managed by an Argo CD application.
func isResourceManaged(kube *kube.KubernetesClient, res *unstructured.Unstructured, maxRecurse int) (bool, error) {
	if maxRecurse < 1 {
		return false, fmt.Errorf("recursion limit reached")
	}

	// If the resource carries a tracking label or annotation, regardless of
	// its value, we deem the resource to be managed by Argo CD. At this
	// point in time, we do not care about the particular details of the
	// managing app.
	refs := res.GetOwnerReferences()
	lbls := res.GetLabels()
	annt := res.GetAnnotations()

	// TODO: Read Argo CD configuration for the actual value of the tracking
	// method and the label name.
	_, lok := lbls["app.kubernetes.io/instance"]
	_, aok := annt["argocd.argoproj.io/tracking-id"]
	if lok || aok {
		return true, nil
	}

	// Recursively check any owners referenced by the inspected resource.
	for _, ref := range refs {
		gv, err := schema.ParseGroupVersion(ref.APIVersion)
		if err != nil {
			return false, err
		}

		rif, err := kube.Clientset.Discovery().ServerResourcesForGroupVersion(gv.String())
		if err != nil {
			return false, err
		}

		var gvr schema.GroupVersionResource
		for _, apiResource := range rif.APIResources {
			if ref.Kind == apiResource.Kind {
				gvr = schema.GroupVersionResource{Group: apiResource.Group, Version: apiResource.Version, Resource: apiResource.Name}
				break
			}
		}

		// This should not happen, as it means we couldn't resolve the owner's
		// Kind to a resource available in the cluster.
		if gvr.Resource == "" {
			continue
		}

		// Get the owning object from the cluster.
		owner, err := kube.DynamicClient.Resource(gvr).Namespace(res.GetNamespace()).Get(context.Background(), ref.Name, v1.GetOptions{})
		if err != nil {
			return false, err
		}

		isOwned, err := isResourceManaged(kube, owner, maxRecurse-1)
		if err != nil {
			return false, err
		}

		// If the owner is a managed resource, we consider this resource to be
		// managed by Argo CD. Else, we keep going through the list of owning
		// objects.
		if isOwned {
			return true, nil
		}
	}

	// If we reach this point, we are certain that neither the inspected
	// resource nor any of its owners belong to an Argo CD application.
	return false, nil
}
