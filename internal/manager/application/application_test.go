package application

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jannfis/argocd-agent/internal/backend/kubernetes"
	appmock "github.com/jannfis/argocd-agent/internal/backend/mocks"
	appinformer "github.com/jannfis/argocd-agent/internal/informers/application"
	"github.com/jannfis/argocd-agent/internal/metrics"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var appExistsError = errors.NewAlreadyExists(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")
var appNotFoundError = errors.NewNotFound(schema.GroupResource{Group: "argoproj.io", Resource: "application"}, "existing")

func Test_ManagerOptions(t *testing.T) {
	t.Run("NewManager with default options", func(t *testing.T) {
		m := NewApplicationManager(nil, "")
		assert.Equal(t, false, m.AllowUpsert)
		assert.Nil(t, m.Metrics)
	})

	t.Run("NewManager with metrics", func(t *testing.T) {
		m := NewApplicationManager(nil, "", WithMetrics(metrics.NewApplicationClientMetrics()))
		assert.NotNil(t, m.Metrics)
	})

	t.Run("NewManager with upsert enabled", func(t *testing.T) {
		m := NewApplicationManager(nil, "", WithAllowUpsert(true))
		assert.True(t, m.AllowUpsert)
	})
}

func Test_ManagerCreate(t *testing.T) {
	t.Run("Create an application that exists", func(t *testing.T) {
		mockedBackend := appmock.NewApplication(t)
		mockedBackend.On("Create", mock.Anything, mock.Anything, mock.Anything).
			Return(func(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error) {
				if app.Name == "existing" {
					return nil, appExistsError
				} else {
					return nil, nil
				}
			})
		m := NewApplicationManager(mockedBackend, "")
		_, err := m.Create(context.TODO(), &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "existing", Namespace: "default"}})
		assert.ErrorIs(t, err, appExistsError)
	})

	t.Run("Create a new application", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
		}
		mockedBackend := appmock.NewApplication(t)
		m := NewApplicationManager(mockedBackend, "")
		mockedBackend.On("Create", mock.Anything, mock.Anything).Return(app, nil)
		rapp, err := m.Create(context.TODO(), app)
		assert.NoError(t, err)
		assert.Equal(t, "test", rapp.Name)
	})
}

func prettyPrint(app *v1alpha1.Application) {
	b, err := json.MarshalIndent(app, "", "  ")
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s", b)
}

func Test_ManagerUpdateManaged(t *testing.T) {
	t.Run("Update spec", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           "kustomize-guestbook",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeOutOfSync,
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "admin"},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"bar":  "foo",
					"some": "other",
				},
				Annotations: map[string]string{
					"bar":                        "bar",
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeSynced,
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		// We are on the agent
		appC := fakeappclient.NewSimpleClientset(existing)
		informer := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		be := kubernetes.NewKubernetesBackend(appC, informer, true)
		mgr := NewApplicationManager(be, "argocd")

		updated, err := mgr.UpdateManagedApp(context.Background(), incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)

		// Application must have agent's namespace
		require.Equal(t, updated.Namespace, "argocd")
		// Refresh annotation should only be overwritten by the controller, not
		// by the incoming app
		require.Contains(t, updated.Annotations, "argocd.argoproj.io/refresh")
		// Labels and annotations must be in sync with incoming
		require.Equal(t, incoming.Labels, updated.Labels)
		// Non-refresh annotations must be in sync with incoming
		require.Equal(t, incoming.Annotations["bar"], updated.Annotations["bar"])
		// Refresh annotation must not be removed
		require.Contains(t, updated.Annotations, "argocd.argoproj.io/refresh")
		// Status must not have been touched
		require.Equal(t, existing.Status, updated.Status)
		// Spec must be in sync with incoming
		require.Equal(t, incoming.Spec, updated.Spec)
	})
}

func Test_ManagerUpdateStatus(t *testing.T) {
	t.Run("Update spec", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           "kustomize-guestbook",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Status: v1alpha1.ApplicationStatus{
				Sync: v1alpha1.SyncStatus{
					Status: v1alpha1.SyncStatusCodeOutOfSync,
				},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"bar":  "foo",
					"some": "other",
				},
				Annotations: map[string]string{
					"bar":                        "foo",
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		appC := fakeappclient.NewSimpleClientset(existing)
		informer := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		be := kubernetes.NewKubernetesBackend(appC, informer, true)
		mgr := NewApplicationManager(be, "argocd")
		updated, err := mgr.UpdateStatus(context.Background(), "cluster-1", incoming)
		require.NoError(t, err)
		b, err := json.MarshalIndent(updated, "", " ")
		fmt.Printf("%s\n", b)
		require.NoError(t, err)
		require.NotNil(t, updated)
		require.Equal(t, "cluster-1", updated.Namespace)
		require.NotContains(t, updated.Annotations, "argocd.argoproj.io/refresh")
		require.NotContains(t, updated.Labels, "foo")
		require.Contains(t, updated.Labels, "bar")
		require.Contains(t, updated.Labels, "some")
		require.Equal(t, updated.Spec.Source.Path, ".")
		require.Nil(t, updated.Operation)
	})
}

func Test_ManagerUpdateAutonomous(t *testing.T) {
	t.Run("Update status", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "cluster-1",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"bar":                        "foo",
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}

		appC := fakeappclient.NewSimpleClientset(existing)
		informer := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		be := kubernetes.NewKubernetesBackend(appC, informer, true)
		mgr := NewApplicationManager(be, "argocd")
		updated, err := mgr.UpdateAutonomousApp(context.TODO(), "cluster-1", incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "cluster-1", updated.Namespace)
		require.NotContains(t, updated.ObjectMeta.Annotations, "argocd.argoproj.io/refresh")
		require.Equal(t, map[string]string{"foo": "bar"}, updated.Labels)
	})
}

func Test_ManagerUpdateOperation(t *testing.T) {
	t.Run("Update status", func(t *testing.T) {
		incoming := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
				},
				Annotations: map[string]string{
					"argocd.argoproj.io/refresh": "normal",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "hello"},
			},
		}
		existing := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foobar",
				Namespace: "argocd",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "foo",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Source: &v1alpha1.ApplicationSource{
					RepoURL:        "github.com",
					TargetRevision: "HEAD",
					Path:           ".",
				},
				Destination: v1alpha1.ApplicationDestination{
					Server:    "in-cluster",
					Namespace: "guestbook",
				},
			},
			Operation: &v1alpha1.Operation{
				InitiatedBy: v1alpha1.OperationInitiator{Username: "foobar"},
			},
		}

		appC := fakeappclient.NewSimpleClientset(existing)
		informer := appinformer.NewAppInformer(context.Background(), appC, "argocd")
		be := kubernetes.NewKubernetesBackend(appC, informer, true)
		mgr := NewApplicationManager(be, "argocd")
		updated, err := mgr.UpdateOperation(context.TODO(), incoming)
		require.NoError(t, err)
		require.NotNil(t, updated)
		prettyPrint(updated)
	})
}

func Test_ManageApp(t *testing.T) {
	t.Run("Mark app as managed", func(t *testing.T) {
		appm := NewApplicationManager(nil, "")
		assert.False(t, appm.IsManaged("foo"))
		err := appm.Manage("foo")
		assert.NoError(t, err)
		assert.True(t, appm.IsManaged("foo"))
		err = appm.Manage("foo")
		assert.Error(t, err)
		assert.True(t, appm.IsManaged("foo"))
		appm.ClearManaged()
		assert.False(t, appm.IsManaged("foo"))
		assert.Len(t, appm.managedApps, 0)
	})

	t.Run("Mark app as unmanaged", func(t *testing.T) {
		appm := NewApplicationManager(nil, "")
		err := appm.Manage("foo")
		assert.True(t, appm.IsManaged("foo"))
		assert.NoError(t, err)
		err = appm.Unmanage("foo")
		assert.NoError(t, err)
		assert.False(t, appm.IsManaged("foo"))
		err = appm.Unmanage("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsManaged("foo"))
	})
}

func Test_IgnoreChange(t *testing.T) {
	t.Run("Ignore a change", func(t *testing.T) {
		appm := NewApplicationManager(nil, "")
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err := appm.IgnoreChange("foo", "1")
		assert.NoError(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.IgnoreChange("foo", "1")
		assert.Error(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		appm.ClearIgnored()
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		assert.Len(t, appm.managedApps, 0)
	})

	t.Run("Unignore a change", func(t *testing.T) {
		appm := NewApplicationManager(nil, "")
		err := appm.UnignoreChange("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.IgnoreChange("foo", "1")
		assert.NoError(t, err)
		assert.True(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.UnignoreChange("foo")
		assert.NoError(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
		err = appm.UnignoreChange("foo")
		assert.Error(t, err)
		assert.False(t, appm.IsChangeIgnored("foo", "1"))
	})
}

func Test_stampLastUpdated(t *testing.T) {
	t.Run("Stamp app without labels", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "baz",
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Labels, LastUpdatedLabel)
		assert.Len(t, app.Labels, 1)
	})
	t.Run("Stamp app with existing labels", func(t *testing.T) {
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "foo",
				Namespace: "bar",
				Labels: map[string]string{
					"foo": "bar",
					"bar": "baz",
				},
			},
			Spec: v1alpha1.ApplicationSpec{
				Project: "baz",
			},
		}
		stampLastUpdated(app)
		assert.Contains(t, app.Labels, LastUpdatedLabel)
		assert.Len(t, app.Labels, 3)
	})
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
