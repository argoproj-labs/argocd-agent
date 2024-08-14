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
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	backend_mocks "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
)

func Test_CreateApplication(t *testing.T) {
	a := newAgent(t)
	be := backend_mocks.NewApplication(t)
	var err error
	a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true))
	require.NoError(t, err)
	require.NotNil(t, a)
	app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{
		Name:      "test",
		Namespace: "default",
	}}
	t.Run("Discard event in unmanaged mode", func(t *testing.T) {
		napp, err := a.createApplication(app)
		require.Nil(t, napp)
		require.ErrorContains(t, err, "not in managed mode")
	})

	t.Run("Discard event because app is already managed", func(t *testing.T) {
		defer a.appManager.Unmanage(app.QualifiedName())
		a.mode = types.AgentModeManaged
		a.appManager.Manage(app.QualifiedName())
		napp, err := a.createApplication(app)
		require.ErrorContains(t, err, "is already managed")
		require.Nil(t, napp)
	})

	t.Run("Create application", func(t *testing.T) {
		defer a.appManager.Unmanage(app.QualifiedName())
		a.mode = types.AgentModeManaged
		createMock := be.On("Create", mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer createMock.Unset()
		napp, err := a.createApplication(app)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

}

func Test_UpdateApplication(t *testing.T) {
	a := newAgent(t)
	be := backend_mocks.NewApplication(t)
	var err error
	a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true))
	require.NoError(t, err)
	require.NotNil(t, a)
	app := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:            "test",
			Namespace:       "default",
			ResourceVersion: "12345",
		}}
	t.Run("Discard event because version has been seen already", func(t *testing.T) {
		defer a.appManager.ClearIgnored()
		a.appManager.IgnoreChange(fmt.Sprintf("%s/test", a.namespace), "12345")
		napp, err := a.updateApplication(app)
		require.Nil(t, napp)
		require.ErrorContains(t, err, "has already been seen")
	})

	t.Run("Update application using patch in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged
		a.appManager.Role = manager.ManagerRoleAgent
		a.appManager.Mode = manager.ManagerModeManaged
		getEvent := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer getEvent.Unset()
		supportsPatchEvent := be.On("SupportsPatch").Return(true)
		defer supportsPatchEvent.Unset()
		patchEvent := be.On("Patch", mock.Anything, "test", "argocd", mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer patchEvent.Unset()
		napp, err := a.updateApplication(app)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

	t.Run("Update application using update in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged
		a.appManager.Role = manager.ManagerRoleAgent
		a.appManager.Mode = manager.ManagerModeManaged
		getEvent := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer getEvent.Unset()
		supportsPatchEvent := be.On("SupportsPatch").Return(false)
		defer supportsPatchEvent.Unset()
		patchEvent := be.On("Update", mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer patchEvent.Unset()
		napp, err := a.updateApplication(app)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

	t.Run("Update application using patch in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous
		a.appManager.Role = manager.ManagerRoleAgent
		a.appManager.Mode = manager.ManagerModeAutonomous
		getEvent := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer getEvent.Unset()
		supportsPatchEvent := be.On("SupportsPatch").Return(true)
		defer supportsPatchEvent.Unset()
		patchEvent := be.On("Patch", mock.Anything, "test", "argocd", mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer patchEvent.Unset()
		napp, err := a.updateApplication(app)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

	t.Run("Update application using update in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous
		a.appManager.Role = manager.ManagerRoleAgent
		a.appManager.Mode = manager.ManagerModeAutonomous
		getEvent := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer getEvent.Unset()
		supportsPatchEvent := be.On("SupportsPatch").Return(false)
		defer supportsPatchEvent.Unset()
		patchEvent := be.On("Update", mock.Anything, mock.Anything).Return(&v1alpha1.Application{}, nil)
		defer patchEvent.Unset()
		napp, err := a.updateApplication(app)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

}
