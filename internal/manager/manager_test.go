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

package manager

import (
	"context"
	"testing"

	argoapp "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func Test_IsPrincipal(t *testing.T) {
	r := ManagerRoleAgent
	assert.True(t, r.IsAgent())
	assert.False(t, r.IsPrincipal())
}
func Test_IsAgent(t *testing.T) {
	r := ManagerRolePrincipal
	assert.True(t, r.IsPrincipal())
	assert.False(t, r.IsAgent())
}

func Test_IsAutonomous(t *testing.T) {
	m := ManagerModeAutonomous
	assert.True(t, m.IsAutonomous())
	assert.False(t, m.IsManaged())
}

func Test_IsManaged(t *testing.T) {
	m := ManagerModeManaged
	assert.True(t, m.IsManaged())
	assert.False(t, m.IsAutonomous())
}

type fakeManager[R kubeResource] struct {
	created R
	retErr  error
}

func (f *fakeManager[R]) Create(ctx context.Context, obj R) (R, error) {
	f.created = obj
	return obj, f.retErr
}

func newLogger() *logrus.Entry {
	l := logrus.New()
	l.SetLevel(logrus.DebugLevel)
	return logrus.NewEntry(l)
}

func TestRevertUserInitiatedDeletion(t *testing.T) {
	t.Run("Application", func(t *testing.T) {
		requires := require.New(t)

		// No annotation -> no recreate
		app := &argoapp.Application{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "app",
				Namespace: "ns",
			},
		}
		mgr := &fakeManager[*argoapp.Application]{}
		deletions := NewDeletionTracker()
		ok := RevertUserInitiatedDeletion(context.Background(), app, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		// With annotation but valid deletion -> no recreate
		app.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("u1"))}
		mgr = &fakeManager[*argoapp.Application]{}
		deletions.MarkExpected(types.UID("u1"))
		ok = RevertUserInitiatedDeletion(context.Background(), app, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		// With annotation and invalid deletion -> recreate
		app.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("u2"))}
		mgr = &fakeManager[*argoapp.Application]{}
		ok = RevertUserInitiatedDeletion(context.Background(), app, deletions, mgr, newLogger())
		requires.True(ok)
		requires.NotNil(mgr.created)
		requires.Equal(types.UID("u2"), mgr.created.GetUID())
		requires.Equal("", mgr.created.GetResourceVersion())
		requires.Nil(mgr.created.GetDeletionTimestamp())
	})

	t.Run("AppProject", func(t *testing.T) {
		requires := require.New(t)

		proj := &argoapp.AppProject{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "proj",
				Namespace: "argocd",
			},
		}
		deletions := NewDeletionTracker()
		mgr := &fakeManager[*argoapp.AppProject]{}
		ok := RevertUserInitiatedDeletion(context.Background(), proj, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		proj.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("p1"))}
		mgr = &fakeManager[*argoapp.AppProject]{}
		deletions.MarkExpected(types.UID("p1"))
		ok = RevertUserInitiatedDeletion(context.Background(), proj, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		proj.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("p2"))}
		mgr = &fakeManager[*argoapp.AppProject]{}
		ok = RevertUserInitiatedDeletion(context.Background(), proj, deletions, mgr, newLogger())
		requires.True(ok)
		requires.NotNil(mgr.created)
		requires.Equal(types.UID("p2"), mgr.created.GetUID())
		requires.Equal("", mgr.created.GetResourceVersion())
		requires.Nil(mgr.created.GetDeletionTimestamp())
	})

	t.Run("Repository Secret", func(t *testing.T) {
		requires := require.New(t)

		repo := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "repo",
				Namespace: "argocd",
			},
		}
		deletions := NewDeletionTracker()
		mgr := &fakeManager[*corev1.Secret]{}
		ok := RevertUserInitiatedDeletion(context.Background(), repo, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		repo.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("r1"))}
		mgr = &fakeManager[*corev1.Secret]{}
		deletions.MarkExpected(types.UID("r1"))
		ok = RevertUserInitiatedDeletion(context.Background(), repo, deletions, mgr, newLogger())
		requires.False(ok)
		requires.Nil(mgr.created)

		repo.Annotations = map[string]string{SourceUIDAnnotation: string(types.UID("r2"))}
		mgr = &fakeManager[*corev1.Secret]{}
		ok = RevertUserInitiatedDeletion(context.Background(), repo, deletions, mgr, newLogger())
		requires.True(ok)
		requires.NotNil(mgr.created)
		requires.Equal(types.UID("r2"), mgr.created.GetUID())
		requires.Equal("", mgr.created.GetResourceVersion())
		requires.Nil(mgr.created.GetDeletionTimestamp())
	})
}
