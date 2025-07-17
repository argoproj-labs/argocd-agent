package agent

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_addAppCreationToQueue(t *testing.T) {
	a := newAgent(t)
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
	a := newAgent(t)
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
	a := newAgent(t)
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
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.addAppDeletionToQueue(app)
		require.Equal(t, 0, a.queues.SendQ(defaultQueueName).Len())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("Deletion event for unmanaged application", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		a.addAppDeletionToQueue(app)
		items := a.queues.SendQ(defaultQueueName)
		assert.Equal(t, 0, items.Len())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
}

func Test_deleteNamespaceCallback(t *testing.T) {
	a := newAgent(t)
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
	a := newAgent(t)
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

		// AppProject should be not be managed by now
		assert.False(t, a.projectManager.IsManaged("test-project"))
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
	a := newAgent(t)
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
	a := newAgent(t)
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
