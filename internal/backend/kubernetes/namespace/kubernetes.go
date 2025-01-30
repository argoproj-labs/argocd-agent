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

package namespace

import (
	"context"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/informer"
)

var _ backend.Namespace = &KubernetesBackend{}

// KubernetesBackend is an implementation of the backend.Namespace interface, which is used to track the state of namespaces
// local to the agent/principal. KubernetesBackend is used by both the principal and agent components.
type KubernetesBackend struct {
	// informer is used to watch for changes to the namespaces.
	informer informer.InformerInterface
}

func NewKubernetesBackend(nsInformer informer.InformerInterface) *KubernetesBackend {
	return &KubernetesBackend{
		informer: nsInformer,
	}
}

func (be *KubernetesBackend) StartInformer(ctx context.Context) error {
	return be.informer.Start(ctx)
}

func (be *KubernetesBackend) EnsureSynced(timeout time.Duration) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), timeout)
	defer cancelFunc()
	return be.informer.WaitForSync(ctx)
}
