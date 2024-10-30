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

package fixture

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	argoapp "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	apps "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

type KubeObject interface {
	metav1.Object
	runtime.Object
}

type KubeObjectList interface {
	metav1.ListInterface
	runtime.Object
}

func ToNamespacedName(object KubeObject) types.NamespacedName {
	return types.NamespacedName{Name: object.GetName(), Namespace: object.GetNamespace()}
}

type KubeClient struct {
	config         *rest.Config
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
