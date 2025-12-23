package agent

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/resources"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	cacheutil "github.com/argoproj/argo-cd/v3/util/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func Test_addAppCreationToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Add some application in autonomous mode", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppCreationToQueue(app)
		defer a.appManager.ClearManaged()

		// Should have an event in queue
		require.Equal(t, 1, a.queues.SendQ(defaultQueueName).Len())
		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		assert.NotNil(t, ev)
		// Queue should be empty after get
		assert.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/guestbook"))
	})

	t.Run("Add some application in managed mode", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.mode = types.AgentModeManaged
		defer func() {
			a.mode = types.AgentModeAutonomous
			a.appManager.ClearManaged()
		}()
		a.addAppCreationToQueue(app)

		// Should not have an event in queue
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/guestbook"))
	})

	t.Run("Add some application that is already managed", func(t *testing.T) {
		a.appManager.Manage("agent/guestbook")
		defer a.appManager.ClearManaged()
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppCreationToQueue(app)

		// Should not have an event in queue
		items := a.queues.SendQ(defaultQueueName).Len()
		assert.Equal(t, 0, items)

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/guestbook"))
	})

	t.Run("Use the default queue irrespective of the Client ID", func(t *testing.T) {
		defer a.appManager.ClearManaged()
		a.remote.SetClientID("notexisting")
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "testapp", Namespace: "agent"}}
		a.addAppCreationToQueue(app)

		// Should have an event in queue
		items := a.queues.SendQ(defaultQueueName).Len()
		assert.Equal(t, 1, items)

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/testapp"))
	})
}

func Test_addAppUpdateToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Update event for autonomous agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.mode = types.AgentModeAutonomous
		a.addAppUpdateToQueue(app, app)
		defer a.appManager.Unmanage("agent/guestbook")

		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		require.NotNil(t, ev)
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())
	})

	t.Run("Update event for managed agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.mode = types.AgentModeManaged
		a.addAppUpdateToQueue(app, app)
		defer a.appManager.Unmanage("agent/guestbook")

		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		require.NotNil(t, ev)
		assert.Equal(t, event.StatusUpdate.String(), ev.Type())
	})

	t.Run("Update event for unmanaged application", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppUpdateToQueue(app, app)
		defer a.appManager.Unmanage("agent/guestbook")
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
	})

}

func Test_addAppDeletionToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Deletion event for managed application on autonomous agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.addAppDeletionToQueue(app)
		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		assert.Equal(t, event.Delete.String(), ev.Type())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("Deletion event for managed application on managed agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.mode = types.AgentModeManaged
		defer func() {
			a.mode = types.AgentModeAutonomous
		}()
		// Delete events will be sent for managed applications on managed agent, whether or not the application is managed.
		_ = a.appManager.Manage("agent/guestbook")
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
		a.addAppDeletionToQueue(app)
		require.Equal(t, 1, a.queues.SendQ(defaultQueueName).Len())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("Deletion event for unmanaged application", func(t *testing.T) {
		a, _ := newAgent(t)
		a.remote.SetClientID("agent")
		a.mode = types.AgentModeAutonomous

		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		a.addAppDeletionToQueue(app)
		items := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 0, items.Len())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("Recreation of application from principal when deleted", func(t *testing.T) {
		// Create a fresh agent for this test
		testAgent, _ := newAgent(t)
		testAgent.remote.SetClientID("agent")
		testAgent.emitter = event.NewEventSource("principal")
		testAgent.mode = types.AgentModeManaged

		// Create an app with source UID annotation (from principal)
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "guestbook",
				Namespace: "agent",
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "uid-123",
				},
			},
		}

		// App must be already managed for event to be generated
		_ = testAgent.appManager.Manage("agent/guestbook")
		defer testAgent.appManager.ClearManaged()

		// Don't mark the deletion as expected, so it should be recreated
		testAgent.addAppDeletionToQueue(app)

		// Should have recreated the app (no event in queue because recreation happened)
		assert.Equal(t, 0, testAgent.queues.SendQ(defaultQueueName).Len())

		// Verify the app is still managed (recreated)
		assert.True(t, testAgent.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("No recreation when deletion is expected from principal", func(t *testing.T) {
		// Create a fresh agent for this test
		testAgent, _ := newAgent(t)
		testAgent.remote.SetClientID("agent")
		testAgent.emitter = event.NewEventSource("principal")
		testAgent.mode = types.AgentModeManaged

		// Create an app with source UID annotation (from principal)
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "guestbook",
				Namespace: "agent",
				Annotations: map[string]string{
					manager.SourceUIDAnnotation: "uid-123",
				},
			},
		}

		// App must be already managed for event to be generated
		_ = testAgent.appManager.Manage("agent/guestbook")
		defer testAgent.appManager.ClearManaged()

		// Mark the deletion as expected
		testAgent.deletions.MarkExpected(k8stypes.UID("uid-123"))

		testAgent.addAppDeletionToQueue(app)

		// Should have sent delete event (not recreated)
		assert.Equal(t, 1, testAgent.queues.SendQ(defaultQueueName).Len())
		ev, _ := testAgent.queues.SendQ(defaultQueueName).Get()
		assert.Equal(t, event.Delete.String(), ev.Type())
	})
}

func Test_deleteNamespaceCallback(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")
	err := a.queues.Create("agent")
	require.NoError(t, err)

	t.Run("Delete queue with same name as namespace", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: "agent"}}
		a.deleteNamespaceCallback(ns)
		require.False(t, a.queues.HasQueuePair("agent"))
		// Recreate queue so that following tests will work
		a.queues.Create("agent")
	})
	t.Run("Unrelated namespace deletion", func(t *testing.T) {
		ns := &corev1.Namespace{ObjectMeta: v1.ObjectMeta{Name: "argocd"}}
		a.deleteNamespaceCallback(ns)
		require.True(t, a.queues.HasQueuePair("agent"))
	})
}

func Test_addAppProjectCreationToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Add appProject in autonomous mode", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.addAppProjectCreationToQueue(appProject)
		defer a.projectManager.ClearManaged()

		// Should have an event in queue
		require.Equal(t, 1, a.queues.SendQ(defaultQueueName).Len())
		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		assert.NotNil(t, ev)
		assert.Equal(t, event.Create.String(), ev.Type())
		// Queue should be empty after get
		assert.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())

		// AppProject should be managed by now
		assert.True(t, a.projectManager.IsManaged("test-project"))
	})

	t.Run("Add appProject in managed mode", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.mode = types.AgentModeManaged
		defer func() {
			a.mode = types.AgentModeAutonomous
			a.projectManager.ClearManaged()
		}()
		a.addAppProjectCreationToQueue(appProject)

		// Should not have an event in queue in managed mode
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())

		// AppProject should be managed by now
		assert.True(t, a.projectManager.IsManaged("test-project"))
	})

	t.Run("Add appProject that is already managed", func(t *testing.T) {
		a.projectManager.Manage("test-project")
		defer a.projectManager.ClearManaged()
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.addAppProjectCreationToQueue(appProject)

		// Should not have an event in queue because it's already managed
		items := a.queues.SendQ(defaultQueueName).Len()
		assert.Equal(t, 0, items)

		// AppProject should still be managed
		assert.True(t, a.projectManager.IsManaged("test-project"))
	})

	t.Run("Use the default queue irrespective of the Client ID", func(t *testing.T) {
		defer a.projectManager.ClearManaged()
		a.remote.SetClientID("notexisting")
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.addAppProjectCreationToQueue(appProject)

		// Should have an event in queue
		items := a.queues.SendQ(defaultQueueName).Len()
		assert.Equal(t, 1, items)

		// AppProject should be managed by now
		assert.True(t, a.projectManager.IsManaged("test-project"))
	})
}

func Test_addAppProjectUpdateToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Update event for autonomous agent", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		// AppProject must be already managed for event to be generated
		_ = a.projectManager.Manage("test-project")
		a.mode = types.AgentModeAutonomous
		a.addAppProjectUpdateToQueue(appProject, appProject)
		defer a.projectManager.Unmanage("test-project")

		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		require.NotNil(t, ev)
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())
	})

	t.Run("Update event for managed agent", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		// AppProject must be already managed for event to be generated
		_ = a.projectManager.Manage("test-project")
		a.mode = types.AgentModeManaged
		a.addAppProjectUpdateToQueue(appProject, appProject)
		defer func() {
			a.mode = types.AgentModeAutonomous
			a.projectManager.Unmanage("test-project")
		}()

		// Should not have an event in queue in managed mode
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
	})

	t.Run("Update event for unmanaged appProject", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.addAppProjectUpdateToQueue(appProject, appProject)

		// Should not have an event in queue for unmanaged appProject
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
	})

	t.Run("Ignore change for already seen resource version", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{
			ObjectMeta: v1.ObjectMeta{
				Name:            "test-project",
				Namespace:       "agent",
				ResourceVersion: "12345",
			},
		}
		// AppProject must be already managed for the change to be checked
		_ = a.projectManager.Manage("test-project")
		defer a.projectManager.Unmanage("test-project")

		// Mark this change as already seen
		a.projectManager.IgnoreChange("test-project", "12345")
		defer a.projectManager.ClearIgnored()

		a.addAppProjectUpdateToQueue(appProject, appProject)

		// Should not have an event in queue for ignored change
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
	})

	t.Run("Handle missing default queue gracefully", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		// AppProject must be already managed for event to be generated
		_ = a.projectManager.Manage("test-project")
		defer a.projectManager.Unmanage("test-project")

		// Delete the default queue to simulate it being missing
		err := a.queues.Delete(defaultQueueName, true)
		require.NoError(t, err)

		a.addAppProjectUpdateToQueue(appProject, appProject)

		// Should not have an event in any queue since default queue is missing
		// Recreate the queue for other tests
		err = a.queues.Create(defaultQueueName)
		require.NoError(t, err)
	})
}

func Test_addAppProjectDeletionToQueue(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Deletion event for managed appProject on autonomous agent", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		// AppProject must be already managed for proper cleanup
		_ = a.projectManager.Manage("test-project")
		a.addAppProjectDeletionToQueue(appProject)

		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		assert.Equal(t, event.Delete.String(), ev.Type())
		require.False(t, a.projectManager.IsManaged("test-project"))
	})

	t.Run("Deletion event for managed appProject on managed agent", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		a.mode = types.AgentModeManaged
		defer func() {
			a.mode = types.AgentModeAutonomous
		}()
		// AppProject must be already managed for proper cleanup
		_ = a.projectManager.Manage("test-project")
		a.addAppProjectDeletionToQueue(appProject)

		// Should not have an event in queue in managed mode
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
		// AppProject should still be managed
		require.True(t, a.projectManager.IsManaged("test-project"))
	})

	t.Run("Deletion event for unmanaged appProject", func(t *testing.T) {
		appProject := &v1alpha1.AppProject{ObjectMeta: v1.ObjectMeta{Name: "test-project", Namespace: "agent"}}
		// AppProject is not managed, but deletion should proceed anyways
		a.addAppProjectDeletionToQueue(appProject)

		ev, _ := a.queues.SendQ(defaultQueueName).Get()
		assert.Equal(t, event.Delete.String(), ev.Type())
		require.False(t, a.projectManager.IsManaged("test-project"))
	})
}

func Test_addClusterCacheInfoUpdateToQueue(t *testing.T) {
	miniRedis, err := miniredis.Run()
	require.NoError(t, err)
	require.NotNil(t, miniRedis)
	defer miniRedis.Close()

	kubec := kube.NewKubernetesFakeClientWithApps("argocd")
	remote, err := client.NewRemote("127.0.0.1", 8080)
	require.NoError(t, err)

	a, err := NewAgent(context.TODO(), kubec, "argocd", WithRemote(remote), WithRedisHost(miniRedis.Addr()), WithCacheRefreshInterval(10*time.Second))
	require.NoError(t, err)

	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	// First populate the cache with dummy data
	clusterMgr, err := cluster.NewManager(a.context, a.namespace, miniRedis.Addr(), "", cacheutil.RedisCompressionGZip, a.kubeClient.Clientset, nil)
	require.NoError(t, err)
	err = clusterMgr.MapCluster("test-agent", &v1alpha1.Cluster{
		Name:   "test-cluster",
		Server: "https://kubernetes.default.svc",
	})
	require.NoError(t, err)

	// Set dummy cluster cache stats
	clusterMgr.SetClusterCacheStats(&event.ClusterCacheInfo{
		ApplicationsCount: 5,
		APIsCount:         10,
		ResourcesCount:    100,
	}, "test-agent")

	// Should not have an event in queue
	require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())

	// Add a cluster cache info update event to the queue
	a.addClusterCacheInfoUpdateToQueue()

	// Should have an event in queue
	require.Equal(t, 1, a.queues.SendQ(defaultQueueName).Len())
}

func Test_handleRepositoryCreation(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	t.Run("Skip repository event in autonomous mode", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: v1.ObjectMeta{
				Name:      "test-repo",
				Namespace: "argocd",
			},
		}

		// Set to autonomous mode
		a.mode = types.AgentModeAutonomous
		defer func() {
			a.mode = types.AgentModeManaged // Reset to default
		}()

		initialResourceCount := a.resources.Len()
		a.handleRepositoryCreation(repo)

		// Should not add to resources or manage
		assert.Equal(t, initialResourceCount, a.resources.Len())
		assert.False(t, a.repoManager.IsManaged("test-repo"))
	})

	t.Run("Skip already managed repository in managed mode", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: v1.ObjectMeta{
				Name:      "existing-repo",
				Namespace: "argocd",
			},
		}

		// Set to managed mode
		a.mode = types.AgentModeManaged

		// Pre-manage the repository
		err := a.repoManager.Manage("existing-repo")
		require.NoError(t, err)
		defer a.repoManager.ClearManaged()

		initialResourceCount := a.resources.Len()
		a.handleRepositoryCreation(repo)

		assert.Equal(t, initialResourceCount+1, a.resources.Len())
		assert.True(t, a.repoManager.IsManaged("existing-repo"))
	})

	t.Run("Successfully handle new repository in managed mode", func(t *testing.T) {
		repo := &corev1.Secret{
			ObjectMeta: v1.ObjectMeta{
				Name:      "new-repo",
				Namespace: "argocd",
			},
		}

		// Set to managed mode
		a.mode = types.AgentModeManaged
		defer a.repoManager.ClearManaged()

		initialResourceCount := a.resources.Len()
		a.handleRepositoryCreation(repo)

		// Should add to resources and manage
		assert.Equal(t, initialResourceCount+1, a.resources.Len())
		assert.True(t, a.repoManager.IsManaged("new-repo"))
	})
}

func Test_handleRepositoryUpdate(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	oldRepo := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:            "test-repo",
			Namespace:       "argocd",
			ResourceVersion: "1",
		},
	}

	newRepo := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:            "test-repo",
			Namespace:       "argocd",
			ResourceVersion: "2",
		},
	}

	t.Run("Skip repository update in autonomous mode", func(t *testing.T) {
		// Set to autonomous mode
		a.mode = types.AgentModeAutonomous
		defer func() {
			a.mode = types.AgentModeManaged // Reset to default
		}()

		// Should not panic or cause issues
		a.handleRepositoryUpdate(oldRepo, newRepo)

		// Should not be managed
		assert.False(t, a.repoManager.IsManaged("test-repo"))
	})

	t.Run("Skip ignored change in managed mode", func(t *testing.T) {
		// Set to managed mode
		a.mode = types.AgentModeManaged

		// Pre-manage the repository and ignore the change
		err := a.repoManager.Manage("test-repo")
		require.NoError(t, err)
		err = a.repoManager.IgnoreChange("test-repo", "2")
		require.NoError(t, err)
		defer a.repoManager.ClearManaged()

		// Should skip due to ignored change
		a.handleRepositoryUpdate(oldRepo, newRepo)

		// Should still be managed but no further action taken
		assert.True(t, a.repoManager.IsManaged("test-repo"))
	})

	t.Run("Skip update for unmanaged repository", func(t *testing.T) {
		// Set to managed mode
		a.mode = types.AgentModeManaged

		// Repository is not managed
		assert.False(t, a.repoManager.IsManaged("test-repo"))

		// Should skip because repository is not managed
		a.handleRepositoryUpdate(oldRepo, newRepo)

		// Should still not be managed
		assert.False(t, a.repoManager.IsManaged("test-repo"))
	})

	t.Run("Process valid repository update", func(t *testing.T) {
		// Set to managed mode
		a.mode = types.AgentModeManaged

		// Pre-manage the repository
		err := a.repoManager.Manage("test-repo")
		require.NoError(t, err)
		defer a.repoManager.ClearManaged()

		// Should process the update successfully
		a.handleRepositoryUpdate(oldRepo, newRepo)

		// Should still be managed
		assert.True(t, a.repoManager.IsManaged("test-repo"))
	})
}

func Test_handleRepositoryDeletion(t *testing.T) {
	a, _ := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")

	repo := &corev1.Secret{
		ObjectMeta: v1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "argocd",
		},
	}

	t.Run("Skip repository deletion in autonomous mode", func(t *testing.T) {
		// Set to autonomous mode
		a.mode = types.AgentModeAutonomous
		defer func() {
			a.mode = types.AgentModeManaged // Reset to default
		}()

		// Add repository to resources first
		a.resources.Add(resources.NewResourceKeyFromRepository(repo))
		initialResourceCount := a.resources.Len()

		a.handleRepositoryDeletion(repo)

		// Should not remove from resources even in autonomous mode
		assert.Equal(t, initialResourceCount, a.resources.Len())
		// Should not be managed
		assert.False(t, a.repoManager.IsManaged("test-repo"))
	})

	t.Run("Skip deletion for unmanaged repository", func(t *testing.T) {
		// Set to managed mode
		a.mode = types.AgentModeManaged

		// Add repository to resources
		a.resources.Add(resources.NewResourceKeyFromRepository(repo))
		initialResourceCount := a.resources.Len()

		// Repository is not managed
		assert.False(t, a.repoManager.IsManaged("test-repo"))

		a.handleRepositoryDeletion(repo)

		// Should remove from resources but skip unmanage
		assert.Equal(t, initialResourceCount-1, a.resources.Len())
		assert.False(t, a.repoManager.IsManaged("test-repo"))
	})
}
