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

	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SyncApplication syncs the named application using the "hook" strategy
func SyncApplication(ctx context.Context, appKey types.NamespacedName, kclient KubeClient) error {
	operation := argoapp.Operation{
		Sync: &argoapp.SyncOperation{
			SyncStrategy: &argoapp.SyncStrategy{
				Hook: &argoapp.SyncStrategyHook{},
			},
		},
		InitiatedBy: argoapp.OperationInitiator{
			Username: "e2e",
		},
	}
	err := SyncApplicationWithOperation(ctx, appKey, operation, kclient)
	return err
}

// SyncApplicationWithOperation syncs the named application using the provided operation
func SyncApplicationWithOperation(ctx context.Context, appKey types.NamespacedName, operation argoapp.Operation, kclient KubeClient) error {
	var err error
	var app argoapp.Application

	err = kclient.Get(ctx, appKey, &app, metav1.GetOptions{})
	if err != nil {
		return err
	}
	app.Operation = &operation
	err = kclient.Update(ctx, &app, metav1.UpdateOptions{})
	return err
}
