package agent

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Test_addAppCreationToQueue(t *testing.T) {
	a := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")
	err := a.queues.Create("agent")
	require.NoError(t, err)

	t.Run("Add some application in autonomous mode", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppCreationToQueue(app)
		defer a.appManager.ClearManaged()

		// Should have an event in queue
		require.Equal(t, 1, a.queues.SendQ("agent").Len())
		ev, _ := a.queues.SendQ("agent").Get()
		assert.NotNil(t, ev)
		// Queue should be empty after get
		assert.Equal(t, 0, a.queues.SendQ("agent").Len())

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
		require.Equal(t, 0, a.queues.SendQ("agent").Len())

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/guestbook"))
	})

	t.Run("Add some application that is already managed", func(t *testing.T) {
		a.appManager.Manage("agent/guestbook")
		defer a.appManager.ClearManaged()
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppCreationToQueue(app)

		// Should not have an event in queue
		items := a.queues.SendQ("agent").Len()
		assert.Equal(t, 0, items)

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/guestbook"))
	})

	t.Run("Missing queue", func(t *testing.T) {
		defer a.appManager.ClearManaged()
		a.remote.SetClientID("notexisting")
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "testapp", Namespace: "agent"}}
		a.addAppCreationToQueue(app)

		// Should not have an event in queue
		items := a.queues.SendQ("agent").Len()
		assert.Equal(t, 0, items)

		// App should be managed by now
		assert.True(t, a.appManager.IsManaged("agent/testapp"))
	})
}

func Test_addAppUpdateToQueue(t *testing.T) {
	a := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")
	err := a.queues.Create("agent")
	require.NoError(t, err)

	t.Run("Update event for autonomous agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.mode = types.AgentModeAutonomous
		a.addAppUpdateToQueue(app, app)
		defer a.appManager.Unmanage("agent/guestbook")

		ev, _ := a.queues.SendQ("agent").Get()
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

		ev, _ := a.queues.SendQ("agent").Get()
		require.NotNil(t, ev)
		assert.Equal(t, event.StatusUpdate.String(), ev.Type())
	})

	t.Run("Update event for unmanaged application", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		a.addAppUpdateToQueue(app, app)
		defer a.appManager.Unmanage("agent/guestbook")
		require.Equal(t, 0, a.queues.SendQ("agent").Len())
	})

}

func Test_addAppDeletionToQueue(t *testing.T) {
	a := newAgent(t)
	a.remote.SetClientID("agent")
	a.emitter = event.NewEventSource("principal")
	err := a.queues.Create("agent")
	require.NoError(t, err)

	t.Run("Deletion event for managed application on autonomous agent", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		_ = a.appManager.Manage("agent/guestbook")
		a.addAppDeletionToQueue(app)
		ev, _ := a.queues.SendQ("agent").Get()
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
		require.Equal(t, 0, a.queues.SendQ("agent").Len())
		require.False(t, a.appManager.IsManaged("agent/guestbook"))
	})
	t.Run("Deletion event for unmanaged application", func(t *testing.T) {
		app := &v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "guestbook", Namespace: "agent"}}
		// App must be already managed for event to be generated
		a.addAppDeletionToQueue(app)
		ev, _ := a.queues.SendQ("agent").Get()
		assert.Equal(t, event.Delete.String(), ev.Type())
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
