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
	"context"
	"fmt"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"

	backend_mocks "github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/application"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ktypes "k8s.io/apimachinery/pkg/types"
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

func Test_ProcessIncomingAppWithUIDMismatch(t *testing.T) {
	a := newAgent(t)
	a.mode = types.AgentModeManaged
	evs := event.NewEventSource("test")
	var be *backend_mocks.Application
	var getMock, createMock, deleteMock, supportsPatchMock, updateMock *mock.Call

	// incomingApp is the app sent by the principal
	incomingApp := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{
		Name:      "test",
		Namespace: "argocd",
	}}

	// oldApp is the app that is already present on the agent
	oldApp := incomingApp.DeepCopy()
	oldApp.Annotations = map[string]string{
		manager.SourceUIDAnnotation: "old_uid",
	}
	a.appManager.Manage(oldApp.QualifiedName())
	defer a.appManager.ClearManaged()

	incomingApp.UID = ktypes.UID("new_uid")

	// createdApp is the app that is finally created on the cluster.
	createdApp := incomingApp.DeepCopy()
	createdApp.UID = ktypes.UID("random_uid")
	createdApp.Annotations = map[string]string{
		// the final version of the app should have the source UID from the incomingApp
		manager.SourceUIDAnnotation: string(incomingApp.UID),
	}

	configureManager := func(t *testing.T, mode manager.ManagerMode) {
		t.Helper()
		be = backend_mocks.NewApplication(t)
		var err error
		a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true),
			application.WithRole(manager.ManagerRoleAgent), application.WithMode(mode))
		require.NoError(t, err)
		require.NotNil(t, a)

		// Get is used to retrieve the oldApp and compare its UID annotation.
		getMock = be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldApp, nil)

		// Create is used to create the latest version of the app on the agent.
		createMock = be.On("Create", mock.Anything, mock.Anything).Return(createdApp, nil)

		supportsPatchMock = be.On("SupportsPatch").Return(false)
		updateMock = be.On("Update", mock.Anything, mock.Anything).Return(oldApp, nil)

		// Delete is used to delete the oldApp from the agent.
		deleteMock = be.On("Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
	}

	unsetMocks := func(t *testing.T) {
		t.Helper()
		getMock.Unset()
		createMock.Unset()
		deleteMock.Unset()
		supportsPatchMock.Unset()
		updateMock.Unset()
	}

	t.Run("Create: Old app with diff UID must be deleted before creating the incoming app", func(t *testing.T) {
		configureManager(t, manager.ManagerModeManaged)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		ev := event.New(evs.ApplicationEvent(event.Create, incomingApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID, delete old app, and create a new app.
		expectedCalls := []string{"Get", "Delete", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new app has the updated source UID annotation.
		appInterface := be.Calls[2].ReturnArguments[0]
		latestApp, ok := appInterface.(*v1alpha1.Application)
		require.True(t, ok)
		require.Equal(t, string(incomingApp.UID), latestApp.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Create: Old app with the same UID must be updated", func(t *testing.T) {
		configureManager(t, manager.ManagerModeManaged)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		// The incoming app's UID matches with the UID in the annotation
		newApp := incomingApp.DeepCopy()
		newApp.UID = ktypes.UID("old_uid")
		newApp.Labels = map[string]string{
			"name": "test",
		}

		ev := event.New(evs.ApplicationEvent(event.Create, newApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		expectedCalls := []string{"Get", "Get", "SupportsPatch", "Update"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		appInterface := be.Calls[3].ReturnArguments[0]
		latestApp, ok := appInterface.(*v1alpha1.Application)
		require.True(t, ok)
		require.Equal(t, newApp.Labels, latestApp.Labels)
	})

	t.Run("Update: Old app with diff UID must be deleted and a new app must be created", func(t *testing.T) {
		configureManager(t, manager.ManagerModeManaged)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		// Create an Update event for the new app
		ev := event.New(evs.ApplicationEvent(event.SpecUpdate, incomingApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID, delete old app, and create a new app.
		expectedCalls := []string{"Get", "Delete", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new app has the updated source UID annotation.
		appInterface := be.Calls[2].ReturnArguments[0]

		latestApp, ok := appInterface.(*v1alpha1.Application)
		require.True(t, ok)
		require.Equal(t, string(incomingApp.UID), latestApp.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Update: incoming app must be created if it doesn't exist while handling update event", func(t *testing.T) {
		configureManager(t, manager.ManagerModeManaged)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		newApp := incomingApp.DeepCopy()
		newApp.Name = "new-app"

		getMock.Unset()
		notFoundError := kerrors.NewNotFound(schema.GroupResource{
			Group: "argoproj.io", Resource: "application"}, newApp.Name)
		getMock = be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundError)

		createMock.Unset()
		createMock = be.On("Create", mock.Anything, mock.Anything).Return(newApp, nil)

		// Create an Update event for the new app
		ev := event.New(evs.ApplicationEvent(event.SpecUpdate, newApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		expectedCalls := []string{"Get", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new app has been created.
		appInterface := be.Calls[1].ReturnArguments[0]
		latestApp, ok := appInterface.(*v1alpha1.Application)
		require.True(t, ok)
		require.Equal(t, newApp, latestApp)
	})

	t.Run("Update: Don't compare source UID for the autonomous agent", func(t *testing.T) {
		configureManager(t, manager.ManagerModeAutonomous)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		a.mode = types.AgentModeAutonomous
		defer func() {
			a.mode = types.AgentModeManaged
		}()

		// Create a fake sync operation for the autonomous agent app.
		newApp := incomingApp.DeepCopy()
		newApp.Operation = &v1alpha1.Operation{
			Sync: &v1alpha1.SyncOperation{
				Revision: "1.0.0",
			},
		}

		updateMock.Unset()
		updateMock = be.On("Update", mock.Anything, mock.Anything).Return(newApp, nil)

		ev := event.New(evs.ApplicationEvent(event.SpecUpdate, newApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		expectedCalls := []string{"Get", "SupportsPatch", "Update"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		appInterface := be.Calls[2].ReturnArguments[0]
		latestApp, ok := appInterface.(*v1alpha1.Application)
		require.True(t, ok)
		require.Equal(t, newApp.Operation, latestApp.Operation)
	})

	t.Run("Delete: Old app with diff UID must be deleted", func(t *testing.T) {
		configureManager(t, manager.ManagerModeManaged)
		defer unsetMocks(t)
		a.appManager.Manage(oldApp.QualifiedName())
		defer a.appManager.ClearManaged()

		// Create a delete event for the new app
		ev := event.New(evs.ApplicationEvent(event.Delete, incomingApp), event.TargetApplication)
		err := a.processIncomingApplication(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID and delete old app
		expectedCalls := []string{"Get", "Delete"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)
		require.False(t, a.appManager.IsManaged(incomingApp.QualifiedName()))
	})
}

func Test_ProcessIncomingAppProjectWithUIDMismatch(t *testing.T) {
	a := newAgent(t)
	a.mode = types.AgentModeManaged
	evs := event.NewEventSource("test")
	var be *backend_mocks.AppProject
	var getMock, createMock, deleteMock, supportsPatchMock, updateMock *mock.Call

	// incomingAppProject is the appProject sent by the principal
	incomingAppProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{
		Name:      "test",
		Namespace: "argocd",
	}}

	// oldAppProject is the appProject that is already present on the agent
	oldAppProject := incomingAppProject.DeepCopy()
	oldAppProject.Annotations = map[string]string{
		manager.SourceUIDAnnotation: "old_uid",
	}

	incomingAppProject.UID = ktypes.UID("new_uid")

	// createdAppProject is the final version of appProject created on the cluster.
	createdAppProject := incomingAppProject.DeepCopy()
	createdAppProject.UID = ktypes.UID("random_uid")
	createdAppProject.Annotations = map[string]string{
		manager.SourceUIDAnnotation: string(incomingAppProject.UID),
	}

	configureManager := func(t *testing.T) {
		t.Helper()
		be = backend_mocks.NewAppProject(t)
		var err error
		a.projectManager, err = appproject.NewAppProjectManager(be, "argocd", appproject.WithAllowUpsert(true))
		require.NoError(t, err)
		require.NotNil(t, a)

		// Get is used to retrieve the oldAppProject and compare its UID annotation.
		getMock = be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(oldAppProject, nil)

		// Create is used to create the latest version of the appProject on the agent.
		createMock = be.On("Create", mock.Anything, mock.Anything).Return(createdAppProject, nil)

		supportsPatchMock = be.On("SupportsPatch").Return(false)
		updateMock = be.On("Update", mock.Anything, mock.Anything).Return(oldAppProject, nil)

		// Delete is used to delete the oldAppProject from the agent.
		deleteMock = be.On("Delete", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
	}

	unsetMocks := func(t *testing.T) {
		t.Helper()
		getMock.Unset()
		createMock.Unset()
		deleteMock.Unset()
		supportsPatchMock.Unset()
		updateMock.Unset()
	}

	t.Run("Create: Old appProject with diff UID must be deleted before creating the incoming appProject", func(t *testing.T) {
		configureManager(t)
		defer unsetMocks(t)
		a.projectManager.Manage(oldAppProject.Name)
		defer a.projectManager.ClearManaged()
		ev := event.New(evs.AppProjectEvent(event.Create, incomingAppProject), event.TargetAppProject)
		err := a.processIncomingAppProject(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID, delete old appProject, and create a new appProject.
		expectedCalls := []string{"Get", "Delete", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new app has the updated source UID annotation.
		appInterface := be.Calls[2].ReturnArguments[0]
		latestAppProject, ok := appInterface.(*v1alpha1.AppProject)
		require.True(t, ok)
		require.Equal(t, string(incomingAppProject.UID), latestAppProject.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Create: Old appProject with the same UID must be updated", func(t *testing.T) {
		configureManager(t)
		defer unsetMocks(t)
		a.projectManager.Manage(oldAppProject.Name)
		defer a.projectManager.ClearManaged()

		// The incoming app's UID matches with the UID in the annotation
		newAppProject := incomingAppProject.DeepCopy()
		newAppProject.UID = ktypes.UID("old_uid")
		newAppProject.Labels = map[string]string{
			"name": "test",
		}

		ev := event.New(evs.AppProjectEvent(event.Create, newAppProject), event.TargetAppProject)
		err := a.processIncomingAppProject(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		expectedCalls := []string{"Get", "Get", "SupportsPatch", "Update"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		appInterface := be.Calls[3].ReturnArguments[0]
		latestAppProject, ok := appInterface.(*v1alpha1.AppProject)
		require.True(t, ok)
		require.Equal(t, newAppProject.Labels, latestAppProject.Labels)
	})

	t.Run("Update: Old appProject with diff UID must be deleted and a new appProject must be created", func(t *testing.T) {
		configureManager(t)
		defer unsetMocks(t)
		a.projectManager.Manage(oldAppProject.Name)
		defer a.projectManager.ClearManaged()

		// Create an Update event for the new appProject
		ev := event.New(evs.AppProjectEvent(event.SpecUpdate, incomingAppProject), event.TargetAppProject)
		err := a.processIncomingAppProject(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID, delete old appProject, and create a new appProject.
		expectedCalls := []string{"Get", "Delete", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new appProject has the updated source UID annotation.
		appInterface := be.Calls[2].ReturnArguments[0]

		latestAppProject, ok := appInterface.(*v1alpha1.AppProject)
		require.True(t, ok)
		require.Equal(t, string(incomingAppProject.UID), latestAppProject.Annotations[manager.SourceUIDAnnotation])
	})

	t.Run("Update: incoming appProject must be created if it doesn't exist while handling update event", func(t *testing.T) {
		configureManager(t)
		defer unsetMocks(t)
		a.projectManager.Manage(oldAppProject.Name)
		defer a.projectManager.ClearManaged()

		newAppProject := incomingAppProject.DeepCopy()
		newAppProject.Name = "new-app-project"

		getMock.Unset()
		notFoundError := kerrors.NewNotFound(schema.GroupResource{
			Group: "argoproj.io", Resource: "appproject"}, newAppProject.Name)
		getMock = be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundError)

		createMock.Unset()
		createMock = be.On("Create", mock.Anything, mock.Anything).Return(newAppProject, nil)

		// Create an Update event for the new appProject
		ev := event.New(evs.AppProjectEvent(event.SpecUpdate, newAppProject), event.TargetAppProject)
		err := a.processIncomingAppProject(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		expectedCalls := []string{"Get", "Create"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)

		// Check if the new app has been created.
		appInterface := be.Calls[1].ReturnArguments[0]
		latestApp, ok := appInterface.(*v1alpha1.AppProject)
		require.True(t, ok)
		require.Equal(t, newAppProject, latestApp)
	})

	t.Run("Delete: Old appProject with the same UID must be deleted", func(t *testing.T) {
		configureManager(t)
		defer unsetMocks(t)
		a.projectManager.Manage(oldAppProject.Name)
		defer a.projectManager.ClearManaged()

		// Create a delete event for the new appProject
		ev := event.New(evs.AppProjectEvent(event.Delete, incomingAppProject), event.TargetAppProject)
		err := a.processIncomingAppProject(ev)
		require.Nil(t, err)

		// Check if the API calls were made in the same order:
		// compare the UID and delete old appProject
		expectedCalls := []string{"Get", "Delete"}
		gotCalls := []string{}
		for _, call := range be.Calls {
			gotCalls = append(gotCalls, call.Method)
		}
		require.Equal(t, expectedCalls, gotCalls)
		require.False(t, a.appManager.IsManaged(incomingAppProject.Name))
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
		a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true), application.WithMode(manager.ManagerModeManaged), application.WithRole(manager.ManagerRoleAgent))
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
		a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true), application.WithMode(manager.ManagerModeManaged), application.WithRole(manager.ManagerRoleAgent))
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
		a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true), application.WithMode(manager.ManagerModeAutonomous), application.WithRole(manager.ManagerRoleAgent))

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
		a.appManager, err = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true), application.WithMode(manager.ManagerModeAutonomous), application.WithRole(manager.ManagerRoleAgent))

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

func Test_CreateAppProject(t *testing.T) {
	a := newAgent(t)
	be := backend_mocks.NewAppProject(t)
	var err error
	a.projectManager, err = appproject.NewAppProjectManager(be, "argocd", appproject.WithAllowUpsert(true))
	require.NoError(t, err)
	require.NotNil(t, a)
	project := &v1alpha1.AppProject{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		}, Spec: v1alpha1.AppProjectSpec{
			SourceNamespaces: []string{"default", "argocd"},
		},
	}

	t.Run("Discard event in unmanaged mode", func(t *testing.T) {
		napp, err := a.createAppProject(project)
		require.Nil(t, napp)
		require.ErrorContains(t, err, "not in managed mode")
	})

	t.Run("Update appproject when already managed", func(t *testing.T) {
		defer a.projectManager.Unmanage(project.Name)
		a.mode = types.AgentModeManaged
		a.projectManager.Manage(project.Name)

		getMock := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer getMock.Unset()

		supportsPatchMock := be.On("SupportsPatch").Return(false)
		defer supportsPatchMock.Unset()

		updateMock := be.On("Update", mock.Anything, mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer updateMock.Unset()

		napp, err := a.createAppProject(project)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

	t.Run("Create appproject", func(t *testing.T) {
		defer a.projectManager.Unmanage(project.Name)
		a.mode = types.AgentModeManaged
		createMock := be.On("Create", mock.Anything, mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer createMock.Unset()
		napp, err := a.createAppProject(project)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

}

func Test_UpdateAppProject(t *testing.T) {
	a := newAgent(t)
	be := backend_mocks.NewAppProject(t)
	var err error
	a.projectManager, err = appproject.NewAppProjectManager(be, "argocd", appproject.WithAllowUpsert(true))
	require.NoError(t, err)
	require.NotNil(t, a)
	project := &v1alpha1.AppProject{
		ObjectMeta: v1.ObjectMeta{
			Name:            "test",
			Namespace:       "argocd",
			ResourceVersion: "12345",
		},
		Spec: v1alpha1.AppProjectSpec{
			SourceNamespaces: []string{"default", "argocd"},
		}}

	t.Run("Update appproject using patch", func(t *testing.T) {
		a.mode = types.AgentModeManaged
		a.projectManager, err = appproject.NewAppProjectManager(be, "argocd", appproject.WithAllowUpsert(true), appproject.WithMode(manager.ManagerModeManaged), appproject.WithRole(manager.ManagerRoleAgent))
		a.projectManager.Manage(project.Name)
		defer a.projectManager.Unmanage(project.Name)
		getEvent := be.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer getEvent.Unset()
		supportsPatchEvent := be.On("SupportsPatch").Return(true)
		defer supportsPatchEvent.Unset()
		patchEvent := be.On("Patch", mock.Anything, "test", "argocd", mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer patchEvent.Unset()
		napp, err := a.updateAppProject(project)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

	t.Run("Create the appproject if it doesn't exist", func(t *testing.T) {
		a.mode = types.AgentModeManaged
		a.projectManager, err = appproject.NewAppProjectManager(be, "argocd", appproject.WithAllowUpsert(true), appproject.WithMode(manager.ManagerModeManaged), appproject.WithRole(manager.ManagerRoleAgent))
		createMock := be.On("Create", mock.Anything, mock.Anything).Return(&v1alpha1.AppProject{}, nil)
		defer createMock.Unset()
		napp, err := a.updateAppProject(project)
		require.NoError(t, err)
		require.NotNil(t, napp)
	})

}

func Test_processIncomingResourceResyncEvent(t *testing.T) {
	a := newAgent(t)
	a.kubeClient.RestConfig = &rest.Config{}
	a.namespace = "test"
	a.context = context.Background()

	err := a.queues.Create(a.remote.ClientID())
	assert.Nil(t, err)
	a.emitter = event.NewEventSource("test")

	t.Run("discard SyncedResourceList request in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged

		ev, err := a.emitter.RequestSyncedResourceListEvent([]byte{})
		assert.Nil(t, err)

		expected := "agent can only handle SyncedResourceList request in the autonomous mode"
		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process SyncedResourceList request in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous

		ev, err := a.emitter.RequestSyncedResourceListEvent([]byte{})
		assert.Nil(t, err)

		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Nil(t, err)
	})

	t.Run("process SyncedResources in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged

		res := resources.ResourceKey{
			Name:      "sample",
			Namespace: "test",
			Kind:      "Application",
		}
		ev, err := a.emitter.SyncedResourceEvent(res)
		assert.Nil(t, err)

		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.NotNil(t, err)
	})

	t.Run("discard SyncedResources in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous

		ev, err := a.emitter.SyncedResourceEvent(resources.ResourceKey{})
		assert.Nil(t, err)

		expected := "agent can only handle SyncedResource request in the managed mode"
		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process request update in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous

		update := &event.RequestUpdate{
			Name: "test",
			Kind: "Application",
		}
		ev, err := a.emitter.RequestUpdateEvent(update)
		assert.Nil(t, err)

		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.NotNil(t, err)
	})

	t.Run("discard request update in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged

		update := &event.RequestUpdate{}
		ev, err := a.emitter.RequestUpdateEvent(update)
		assert.Nil(t, err)

		expected := "agent can only handle RequestUpdate in the autonomous mode"
		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Equal(t, expected, err.Error())
	})

	t.Run("process RequestResourceResync in managed mode", func(t *testing.T) {
		a.mode = types.AgentModeManaged

		ev, err := a.emitter.RequestResourceResyncEvent()
		assert.Nil(t, err)

		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Nil(t, err)
	})

	t.Run("discard RequestResourceResync in autonomous mode", func(t *testing.T) {
		a.mode = types.AgentModeAutonomous

		ev, err := a.emitter.RequestResourceResyncEvent()
		assert.Nil(t, err)

		expected := "agent can only handle ResourceResync request in the managed mode"
		err = a.processIncomingResourceResyncEvent(event.New(ev, event.TargetResourceResync))
		assert.Equal(t, expected, err.Error())
	})
}
