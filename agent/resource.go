package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

const ownerLookupRecursionLimit = 5

var ErrUnmanaged = errors.New("resource not managed by app")

// processIncomingResourceRequest processes an incoming event that requests
// to retrieve information from the Kubernetes API.
//
// There can be multiple forms of requests. Currently supported are:
//
//   - Request for a particular resource, both namespace and cluster scoped
//   - Request for a limited list of resources, both namespace and cluster
//     scoped.
//   - Request for a list of available APIs
func (a *Agent) processIncomingResourceRequest(ev *event.Event) error {
	// If resource proxy is disabled, we return an error immediately.
	if !a.enableResourceProxy {
		return fmt.Errorf("resource proxy is disabled in agent configuration")
	}

	rreq, err := ev.ResourceRequest()
	if err != nil {
		return err
	}
	logCtx := log().WithFields(logrus.Fields{
		"method":      "processIncomingResourceRequest",
		"uuid":        rreq.UUID,
		"http_method": rreq.Method,
	})

	logCtx.Tracef("Start processing %v", rreq)

	ctx, cancel := context.WithTimeout(a.context, defaultResourceRequestTimeout)
	defer cancel()

	var jsonres []byte
	var unres *unstructured.Unstructured
	var unlist *unstructured.UnstructuredList
	var status error

	// Extract required information from the resource request
	gvr, name, namespace, subresource := rreqEventToGvk(rreq)

	if subresource != "" {
		logCtx.Infof("Processing resource request for subresource %s of resource %s named %s/%s", subresource, gvr.String(), namespace, name)
	} else {
		logCtx.Infof("Processing resource request for resource of type %s named %s/%s", gvr.String(), namespace, name)
	}

	switch rreq.Method {
	case http.MethodGet:
		// If we have a request for a named resource, we fetch that particular
		// resource. If the name is empty, we fetch either a list of resources
		// or a list of APIs instead.
		if name != "" {
			if gvr.Resource != "" {
				if subresource != "" {
					logCtx.Debugf("Fetching subresource %s of managed resource %s/%s/%s", subresource, gvr.Group, gvr.Version, gvr.Resource)
					unres, err = a.getManagedResourceSubresource(ctx, gvr, name, namespace, subresource)
				} else {
					logCtx.Debugf("Fetching managed resource %s/%s/%s", gvr.Group, gvr.Version, gvr.Resource)
					unres, err = a.getManagedResource(ctx, gvr, name, namespace)
				}
			}
		} else {
			if gvr.Resource != "" {
				unlist, err = a.getAvailableResources(ctx, gvr, namespace)
			} else {
				logCtx.Debugf("Fetching APIs for group %s and version %s", gvr.Group, gvr.Version)
				unres, err = a.getAvailableAPIs(ctx, gvr.Group, gvr.Version)
			}
		}
	case http.MethodPost:
		if subresource != "" {
			unres, err = a.processIncomingPostSubresourceRequest(ctx, rreq, gvr, subresource)
		} else {
			unres, err = a.processIncomingPostResourceRequest(ctx, rreq, gvr)
		}
	case http.MethodPatch:
		if subresource != "" {
			unres, err = a.processIncomingPatchSubresourceRequest(ctx, rreq, gvr, subresource)
		} else {
			unres, err = a.processIncomingPatchResourceRequest(ctx, rreq, gvr)
		}
	case http.MethodDelete:
		err = a.processIncomingDeleteResourceRequest(ctx, rreq, gvr)
	default:
		err = fmt.Errorf("invalid HTTP method %s for resource request", rreq.Method)
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
		} else {
			logCtx.Warnf("No resource found for request %v", rreq)
		}
		if err != nil {
			return fmt.Errorf("could not marshal resource to json: %w", err)
		}
	}

	q := a.queues.SendQ(defaultQueueName)
	if q == nil {
		logCtx.Error("Remote queue disappeared")
		return nil
	}
	q.Add(a.emitter.NewResourceResponseEvent(rreq.UUID, event.HTTPStatusFromError(status), string(jsonres)))
	logCtx.Tracef("Emitted resource response")

	return nil
}

func (a *Agent) processIncomingPostResourceRequest(ctx context.Context, req *event.ResourceRequest, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	resourceObj := &unstructured.Unstructured{}
	if err := json.Unmarshal(req.Body, resourceObj); err != nil {
		return nil, err
	}

	createOpts := v1.CreateOptions{}
	if params, ok := req.Params["dryRun"]; ok {
		createOpts.DryRun = []string{params}
	}

	if fieldValidation, ok := req.Params["fieldValidation"]; ok {
		createOpts.FieldValidation = fieldValidation
	}

	if fieldMgr, ok := req.Params["fieldManager"]; ok {
		createOpts.FieldManager = fieldMgr
	}

	client := a.kubeClient.DynamicClient.Resource(gvr)
	return client.Namespace(req.Namespace).Create(ctx, resourceObj, createOpts)
}

func (a *Agent) processIncomingPatchResourceRequest(ctx context.Context, req *event.ResourceRequest, gvr schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	patchOpts := v1.PatchOptions{}
	if params, ok := req.Params["dryRun"]; ok {
		patchOpts.DryRun = []string{params}
	}

	if force, ok := req.Params["force"]; ok {
		forceBool, err := strconv.ParseBool(force)
		if err != nil {
			return nil, err
		}
		patchOpts.Force = &forceBool
	}

	if fieldValidation, ok := req.Params["fieldValidation"]; ok {
		patchOpts.FieldValidation = fieldValidation
	}

	if fieldMgr, ok := req.Params["fieldManager"]; ok {
		patchOpts.FieldManager = fieldMgr
	}

	client := a.kubeClient.DynamicClient.Resource(gvr)
	return client.Namespace(req.Namespace).Patch(ctx, req.Name, k8stypes.MergePatchType, req.Body, patchOpts)
}

func (a *Agent) processIncomingDeleteResourceRequest(ctx context.Context, req *event.ResourceRequest, gvr schema.GroupVersionResource) error {
	// DeleteOptions are sent in the request body as a JSON object.
	deleteOpts := &v1.DeleteOptions{}
	if err := json.Unmarshal(req.Body, deleteOpts); err != nil {
		return err
	}

	// Retrieve the resource and check if it is managed by an Argo CD application.
	_, err := a.getManagedResource(ctx, gvr, req.Name, req.Namespace)
	if err != nil {
		return fmt.Errorf("failed to retrieve resource: %w", err)
	}

	client := a.kubeClient.DynamicClient.Resource(gvr)
	return client.Namespace(req.Namespace).Delete(ctx, req.Name, *deleteOpts)
}

// rreqEventToGvk returns a GroupVersionResource object, name and namespace for
// the resource requested in rreq.
func rreqEventToGvk(rreq *event.ResourceRequest) (gvr schema.GroupVersionResource, name string, namespace string, subresource string) {
	gvr.Group = rreq.Group
	gvr.Resource = rreq.Resource
	gvr.Version = rreq.Version
	name = rreq.Name
	namespace = rreq.Namespace
	subresource = rreq.Subresource
	return
}

// validatePathComponent checks for path traversal attempts in URL components
func validatePathComponent(component, componentName string) error {
	if component == "" {
		return fmt.Errorf("%s cannot be empty", componentName)
	}
	if strings.Contains(component, "..") {
		return fmt.Errorf("%s contains invalid path traversal sequence: %s", componentName, component)
	}
	if strings.Contains(component, "/") {
		return fmt.Errorf("%s contains invalid path separator: %s", componentName, component)
	}
	return nil
}

// buildSubresourcePath constructs the API path for a subresource
func (a *Agent) buildSubresourcePath(gvr schema.GroupVersionResource, name, namespace, subresource string) (string, error) {
	// Validate all path components to prevent path traversal attacks
	if err := validatePathComponent(name, "resource name"); err != nil {
		return "", err
	}
	if err := validatePathComponent(subresource, "subresource"); err != nil {
		return "", err
	}
	if namespace != "" {
		if err := validatePathComponent(namespace, "namespace"); err != nil {
			return "", err
		}
	}

	if namespace != "" {
		if gvr.Group == "" {
			// Core API: /api/v1/namespaces/{namespace}/{resource}/{name}/{subresource}
			return fmt.Sprintf("/api/%s/namespaces/%s/%s/%s/%s", gvr.Version, namespace, gvr.Resource, name, subresource), nil
		}
		// Named group API: /apis/{group}/{version}/namespaces/{namespace}/{resource}/{name}/{subresource}
		return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s/%s/%s", gvr.Group, gvr.Version, namespace, gvr.Resource, name, subresource), nil
	}

	if gvr.Group == "" {
		// Core API cluster-scoped: /api/v1/{resource}/{name}/{subresource}
		return fmt.Sprintf("/api/%s/%s/%s/%s", gvr.Version, gvr.Resource, name, subresource), nil
	}
	// Named group API cluster-scoped: /apis/{group}/{version}/{resource}/{name}/{subresource}
	return fmt.Sprintf("/apis/%s/%s/%s/%s/%s", gvr.Group, gvr.Version, gvr.Resource, name, subresource), nil
}

// executeSubresourceRequest executes a REST request and unmarshals the response
func (a *Agent) executeSubresourceRequest(ctx context.Context, req *rest.Request, subresource string) (*unstructured.Unstructured, error) {
	bytes, err := req.DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	result := &unstructured.Unstructured{}
	if err := json.Unmarshal(bytes, &result.Object); err != nil {
		return nil, fmt.Errorf("failed to unmarshal subresource response: %w", err)
	}

	return result, nil
}

// getManagedResourceSubresource retrieves a subresource of a single resource from the cluster
// and returns it as unstructured data. The resource to be returned must be managed by Argo CD.
func (a *Agent) getManagedResourceSubresource(ctx context.Context, gvr schema.GroupVersionResource, name, namespace, subresource string) (*unstructured.Unstructured, error) {
	// First check if the parent resource is managed
	_, err := a.getManagedResource(ctx, gvr, name, namespace)
	if err != nil {
		return nil, err
	}

	// Build the path and execute the request
	path, err := a.buildSubresourcePath(gvr, name, namespace, subresource)
	if err != nil {
		return nil, fmt.Errorf("invalid subresource path: %w", err)
	}
	restClient := a.kubeClient.Clientset.Discovery().RESTClient()
	req := restClient.Get().AbsPath(path)

	result, err := a.executeSubresourceRequest(ctx, req, subresource)
	if err != nil {
		return nil, fmt.Errorf("failed to get subresource %s: %w", subresource, err)
	}

	return result, nil
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
	case "events":
		// We do allow listing events for now. We have to figure out how to
		// properly filter the events for the resource in question.
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

// getAvailableAPIs retrieves a list of available APIs for a given group and version.
// If group and version are empty, all available APIs are returned.
// If group is empty and version is not, all APIs for the given version are returned.
// If group and version are not empty, all APIs for the given group and version are returned.
//
// It does not yet support the new aggregated API.
func (a *Agent) getAvailableAPIs(ctx context.Context, group, version string) (*unstructured.Unstructured, error) {
	groupVersion := fmt.Sprintf("%s/%s", group, version)
	var groupList *v1.APIGroupList
	var resourceList *v1.APIResourceList
	var err error

	if group == "" && version == "" {
		groupList, err = a.kubeClient.Clientset.Discovery().ServerGroups()
	} else if group == "" && version != "" {
		resourceList, err = a.kubeClient.Clientset.Discovery().ServerResourcesForGroupVersion(version)
	} else {
		resourceList, err = a.kubeClient.Clientset.Discovery().ServerResourcesForGroupVersion(groupVersion)
	}
	if err != nil {
		return nil, err
	}

	var obj map[string]any
	if groupList != nil {
		obj, err = runtime.DefaultUnstructuredConverter.ToUnstructured(groupList)
	} else if resourceList != nil {
		obj, err = runtime.DefaultUnstructuredConverter.ToUnstructured(resourceList)
	}
	if err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: obj}, nil
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
				gvr = schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: apiResource.Name}
				break
			}
		}

		// This should not happen, as it means we couldn't resolve the owner's
		// Kind to a resource available in the cluster.
		if gvr.Resource == "" {
			continue
		}

		// Get the owning object from the cluster. Since owner references are
		// not namespaced, we need to use the namespace of the inspected
		// resource to retrieve the owner.
		//
		// If the dependent resource is cluster-scoped, and the owner is
		// not found, we can assume that the owner is not existing.
		//
		// If the dependent resource is namespaced, and the owner is not
		// found, we try to retrieve the owner from the cluster-scope.
		var owner *unstructured.Unstructured
		owner, err = kube.DynamicClient.Resource(gvr).Namespace(res.GetNamespace()).Get(context.Background(), ref.Name, v1.GetOptions{})
		if err != nil {
			if res.GetNamespace() == "" {
				return false, err
			}
			owner, err = kube.DynamicClient.Resource(gvr).Get(context.Background(), ref.Name, v1.GetOptions{})
			if err != nil {
				return false, err
			}
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

// processIncomingPostSubresourceRequest handles POST requests for subresources
func (a *Agent) processIncomingPostSubresourceRequest(ctx context.Context, rreq *event.ResourceRequest, gvr schema.GroupVersionResource, subresource string) (*unstructured.Unstructured, error) {
	// First check if the parent resource is managed
	_, err := a.getManagedResource(ctx, gvr, rreq.Name, rreq.Namespace)
	if err != nil {
		return nil, err
	}

	// Build the path and execute the request
	path, err := a.buildSubresourcePath(gvr, rreq.Name, rreq.Namespace, subresource)
	if err != nil {
		return nil, fmt.Errorf("invalid subresource path: %w", err)
	}
	restClient := a.kubeClient.Clientset.Discovery().RESTClient()
	req := restClient.Post().AbsPath(path).Body(rreq.Body)

	result, err := a.executeSubresourceRequest(ctx, req, subresource)
	if err != nil {
		return nil, fmt.Errorf("failed to post to subresource %s: %w", subresource, err)
	}

	return result, nil
}

// processIncomingPatchSubresourceRequest handles PATCH requests for subresources
func (a *Agent) processIncomingPatchSubresourceRequest(ctx context.Context, rreq *event.ResourceRequest, gvr schema.GroupVersionResource, subresource string) (*unstructured.Unstructured, error) {
	// First check if the parent resource is managed
	_, err := a.getManagedResource(ctx, gvr, rreq.Name, rreq.Namespace)
	if err != nil {
		return nil, err
	}

	// Determine patch type from content-type header if available
	patchType := k8stypes.MergePatchType
	if rreq.Params != nil && rreq.Params["content-type"] != "" {
		switch rreq.Params["content-type"] {
		case "application/json-patch+json":
			patchType = k8stypes.JSONPatchType
		case "application/strategic-merge-patch+json":
			patchType = k8stypes.StrategicMergePatchType
		}
	}

	// Build the path and execute the request
	path, err := a.buildSubresourcePath(gvr, rreq.Name, rreq.Namespace, subresource)
	if err != nil {
		return nil, fmt.Errorf("invalid subresource path: %w", err)
	}
	restClient := a.kubeClient.Clientset.Discovery().RESTClient()
	req := restClient.Patch(patchType).AbsPath(path).Body(rreq.Body)

	result, err := a.executeSubresourceRequest(ctx, req, subresource)
	if err != nil {
		return nil, fmt.Errorf("failed to patch subresource %s: %w", subresource, err)
	}

	return result, nil
}
