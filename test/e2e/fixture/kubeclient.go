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

// Package fixture provides a client interface similar to the one provided by
// the controller-runtime package, in order to avoid creating a dependency on
// the controller-runtime package.
package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	apps "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

// KubeObject represents a Kubernetes object. This allows the client interface
// to work seamlessly with any resource that implements both the metav1.Object
// and runtime.Object interfaces. This is similar to the controller-runtime's
// client.Object interface.
type KubeObject interface {
	metav1.Object
	runtime.Object
}

// KubeObjectList represents a Kubernetes object list. This allows the client
// interface to work seamlessly with any resource that implements both the
// metav1.ListInterface and runtime.Object interfaces. This is similar to the
// controller-runtime's client.ObjectList interface.
type KubeObjectList interface {
	metav1.ListInterface
	runtime.Object
}

func ToNamespacedName(object KubeObject) types.NamespacedName {
	return types.NamespacedName{Name: object.GetName(), Namespace: object.GetNamespace()}
}

type KubeClient struct {
	Config         *rest.Config
	scheme         *runtime.Scheme
	dclient        *dynamic.DynamicClient
	mapper         meta.RESTMapper
	typeToResource map[reflect.Type]schema.GroupVersionResource
}

func NewKubeClient(config *rest.Config) (KubeClient, error) {
	var kclient KubeClient

	scheme := runtime.NewScheme()
	err := argoapp.AddToScheme(scheme)
	if err != nil {
		return kclient, err
	}
	err = corev1.AddToScheme(scheme)
	if err != nil {
		return kclient, err
	}
	err = apps.AddToScheme(scheme)
	if err != nil {
		return kclient, err
	}
	err = batchv1.AddToScheme(scheme)
	if err != nil {
		return kclient, err
	}
	err = rbacv1.AddToScheme(scheme)
	if err != nil {
		return kclient, err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return kclient, err
	}

	groupResources, err := restmapper.GetAPIGroupResources(discoveryClient)
	if err != nil {
		return kclient, err
	}

	dclient, err := dynamic.NewForConfig(config)
	if err != nil {
		return kclient, err
	}

	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	typeToResource := make(map[reflect.Type]schema.GroupVersionResource)

	return KubeClient{
		config,
		scheme,
		dclient,
		mapper,
		typeToResource,
	}, nil
}

// Get returns the object with the specified key from the cluster. object must
// be a struct pointer so it can be updated with the result returned by the
// server.
func (c KubeClient) Get(ctx context.Context, key types.NamespacedName, object KubeObject, options metav1.GetOptions) error {
	resource, err := c.resourceFor(object)
	if err != nil {
		return err
	}

	result, err := c.dclient.Resource(resource).Namespace(key.Namespace).Get(ctx, key.Name, options)
	if err != nil {
		return err
	}

	// Can't use the following code because it produces a panic for an ArgoCD Application type
	// panic: reflect: reflect.Value.Set using value obtained using unexported field
	// err = runtime.DefaultUnstructuredConverter.FromUnstructured(result.UnstructuredContent(), object)

	b, err := result.MarshalJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, object)
	return err
}

// List returns a list of objects matching the criteria specified by the given
// list options from the given namespace. list must be a struct pointer so that
// the Items field in the list can be populated with the results returned by the
// server.
func (c KubeClient) List(ctx context.Context, namespace string, list KubeObjectList, options metav1.ListOptions) error {
	resource, err := c.resourceFor(list)
	if err != nil {
		return err
	}

	ulist, err := c.dclient.Resource(resource).Namespace(namespace).List(ctx, options)
	if err != nil {
		return err
	}

	b, err := ulist.MarshalJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, list)
	return err
}

// Create creates the given object in the cluster. object must be a struct
// pointer so that it can be updated with the result returned by the server.
func (c KubeClient) Create(ctx context.Context, object KubeObject, options metav1.CreateOptions) error {
	resource, err := c.resourceFor(object)
	if err != nil {
		return err
	}

	objectKind := object.GetObjectKind()
	if len(objectKind.GroupVersionKind().Group) == 0 {
		gvks, _, err := c.scheme.ObjectKinds(object)
		if err != nil {
			return err
		}
		object.GetObjectKind().SetGroupVersionKind(gvks[0])
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return err
	}
	result, err := c.dclient.Resource(resource).Namespace(object.GetNamespace()).Create(ctx, &unstructured.Unstructured{Object: obj}, options)
	if err != nil {
		return err
	}

	b, err := result.MarshalJSON()
	if err != nil {
		return err
	}

	err = json.Unmarshal(b, object)
	return err
}

// Update updates the given object in the cluster. object must be a struct
// pointer so that it can be updated with the result returned by the server.
func (c KubeClient) Update(ctx context.Context, object KubeObject, options metav1.UpdateOptions) error {
	resource, err := c.resourceFor(object)
	if err != nil {
		return err
	}

	obj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object)
	if err != nil {
		return err
	}
	result, err := c.dclient.Resource(resource).Namespace(object.GetNamespace()).Update(ctx, &unstructured.Unstructured{Object: obj}, options)
	if err != nil {
		return err
	}

	b, err := result.MarshalJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, object)
	return err
}

// Patch patches the given object in the cluster using the JSONPatch patch type.
// object must be a struct pointer so that it can be updated with the result
// returned by the server.
func (c KubeClient) Patch(ctx context.Context, object KubeObject, jsonPatch []interface{}, options metav1.PatchOptions) error {
	resource, err := c.resourceFor(object)
	if err != nil {
		return err
	}

	payload, err := json.Marshal(jsonPatch)
	if err != nil {
		return err
	}

	result, err := c.dclient.Resource(resource).Namespace(object.GetNamespace()).Patch(ctx, object.GetName(), types.JSONPatchType, payload, options)
	if err != nil {
		return err
	}

	b, err := result.MarshalJSON()
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, object)
	return err
}

// Delete deletes the given object from the server.
func (c KubeClient) Delete(ctx context.Context, object KubeObject, options metav1.DeleteOptions) error {
	resource, err := c.resourceFor(object)
	if err != nil {
		return err
	}

	err = c.dclient.Resource(resource).Namespace(object.GetNamespace()).Delete(ctx, object.GetName(), options)
	return err
}

func (c KubeClient) resourceFor(object runtime.Object) (schema.GroupVersionResource, error) {
	objectType := reflect.TypeOf(object)
	resource, found := c.typeToResource[objectType]
	if !found {
		gvks, _, err := c.scheme.ObjectKinds(object)
		if err != nil {
			return resource, err
		}
		if len(gvks) == 0 {
			return resource, fmt.Errorf("got no GroupVersionKind values for type %T", object)
		}

		gvk := gvks[0]

		// for a list, we want to return the resource of the items
		if strings.HasSuffix(gvk.Kind, "List") && meta.IsListType(object) {
			gvk.Kind = gvk.Kind[:len(gvk.Kind)-4]
		}

		mapping, err := c.mapper.RESTMapping(gvk.GroupKind())
		if err != nil {
			return resource, err
		}

		resource = mapping.Resource
		c.typeToResource[objectType] = resource
	}
	return resource, nil
}

// EnsureApplicationUpdate ensures the argocd application with the given key is
// updated by retrying if there is a conflicting change.
func (c KubeClient) EnsureApplicationUpdate(ctx context.Context, key types.NamespacedName, modify func(*argoapp.Application) error, options metav1.UpdateOptions) error {
	var err error
	for {
		var app argoapp.Application
		err = c.Get(ctx, key, &app, metav1.GetOptions{})
		if err != nil {
			return err
		}

		err = modify(&app)
		if err != nil {
			return err
		}

		err = c.Update(ctx, &app, options)
		if !errors.IsConflict(err) {
			return err
		}
	}
}

// EnsureAppProjectUpdate ensures the argocd appProject with the given key is
// updated by retrying if there is a conflicting change.
func (c KubeClient) EnsureAppProjectUpdate(ctx context.Context, key types.NamespacedName, modify func(*argoapp.AppProject) error, options metav1.UpdateOptions) error {
	var err error
	for {
		var appProject argoapp.AppProject
		err = c.Get(ctx, key, &appProject, metav1.GetOptions{})
		if err != nil {
			return err
		}

		err = modify(&appProject)
		if err != nil {
			return err
		}

		err = c.Update(ctx, &appProject, options)
		if !errors.IsConflict(err) {
			return err
		}
	}
}

// EnsureRepositoryUpdate ensures the argocd repository with the given key is
// updated by retrying if there is a conflicting change.
func (c KubeClient) EnsureRepositoryUpdate(ctx context.Context, key types.NamespacedName, modify func(*corev1.Secret) error, options metav1.UpdateOptions) error {
	var err error
	for {
		var repo corev1.Secret
		err = c.Get(ctx, key, &repo, metav1.GetOptions{})
		if err != nil {
			return err
		}

		err = modify(&repo)
		if err != nil {
			return err
		}

		err = c.Update(ctx, &repo, options)
		if !errors.IsConflict(err) {
			return err
		}
	}
}

// EnsureDeploymentUpdate ensures the kubernetes deployment with the given key is
// updated by retrying if there is a conflicting change.
func (c KubeClient) EnsureDeploymentUpdate(ctx context.Context, key types.NamespacedName, modify func(*apps.Deployment) error, options metav1.UpdateOptions) error {
	var err error
	for {
		var deployment apps.Deployment
		err = c.Get(ctx, key, &deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}

		err = modify(&deployment)
		if err != nil {
			return err
		}

		err = c.Update(ctx, &deployment, options)
		if !errors.IsConflict(err) {
			return err
		}
	}
}
