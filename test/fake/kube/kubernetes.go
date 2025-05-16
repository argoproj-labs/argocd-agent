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

package kube

import (
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func NewFakeKubeClient() *kubefake.Clientset {
	clientset := kubefake.NewSimpleClientset()
	return clientset
}

func NewFakeClientsetWithResources(objects ...runtime.Object) *kubefake.Clientset {
	clientset := kubefake.NewSimpleClientset(objects...)
	return clientset
}

func NewKubernetesFakeClientWithApps(apps ...runtime.Object) *kube.KubernetesClient {
	c := &kube.KubernetesClient{}
	c.Clientset = NewFakeClientsetWithResources()
	c.ApplicationsClientset = fakeappclient.NewSimpleClientset(apps...)
	return c
}

func NewKubernetesFakeClientWithResources(objects ...runtime.Object) *kube.KubernetesClient {
	c := &kube.KubernetesClient{}
	c.Clientset = NewFakeClientsetWithResources(objects...)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	c.DynamicClient = dynfake.NewSimpleDynamicClient(scheme, objects...)
	c.ApplicationsClientset = fakeappclient.NewSimpleClientset()
	return c
}

func NewDynamicFakeClient(objects ...runtime.Object) *kube.KubernetesClient {
	c := &kube.KubernetesClient{}
	c.Clientset = NewFakeClientsetWithResources(objects...)
	fakeDiscoveryClient := c.Clientset.Discovery().(*fakediscovery.FakeDiscovery)

	groupVersion := schema.GroupVersion{Group: "apps", Version: "v1"}
	resourceList := &metav1.APIResourceList{
		GroupVersion: groupVersion.String(),
		APIResources: []metav1.APIResource{
			{Version: "v1", Name: "deployments", SingularName: "deployment", Namespaced: true, Kind: "Deployment", Verbs: []string{"get", "list", "watch"}},
			{Version: "v1", Name: "replicasets", SingularName: "replicaset", Namespaced: true, Kind: "ReplicaSet", Verbs: []string{"get", "list", "watch"}},
		},
	}

	coreGroupVersion := schema.GroupVersion{Group: "", Version: "v1"} // Core API group has an empty string for the group
	coreResourceList := &metav1.APIResourceList{
		GroupVersion: coreGroupVersion.String(),
		APIResources: []metav1.APIResource{
			{Version: "v1", Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "list", "watch"}},
			{Version: "v1", Name: "services", SingularName: "service", Namespaced: true, Kind: "Service", Verbs: []string{"get", "list", "watch"}},
		},
	}
	fakeDiscoveryClient.Resources = []*metav1.APIResourceList{resourceList, coreResourceList}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	fakeDyn := dynfake.NewSimpleDynamicClient(scheme, objects...)
	c.DynamicClient = fakeDyn

	c.DiscoveryClient = fakeDiscoveryClient
	return c
}
