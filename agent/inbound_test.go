package agent

import (
	"fmt"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	backend_mocks "github.com/jannfis/argocd-agent/internal/backend/mocks"
	"github.com/jannfis/argocd-agent/internal/manager/application"
	"github.com/jannfis/argocd-agent/pkg/types"
)

func Test_CreateApplication(t *testing.T) {
	a := newAgent(t)
	be := backend_mocks.NewApplication(t)
	a.appManager = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true))
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
	a.appManager = application.NewApplicationManager(be, "argocd", application.WithAllowUpsert(true))
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
