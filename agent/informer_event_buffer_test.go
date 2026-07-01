package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/clock"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReceiveAddInformerEvent(t *testing.T) {
	t.Run("add event calls add handler immediately", func(t *testing.T) {
		addCalled := 0
		var receivedApp *v1alpha1.Application

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 10*time.Second,
			func(obj *v1alpha1.Application) {
				receivedApp = obj
				addCalled++
			}, nil, nil)

		app := informerbuffer_newTestApp("argocd", "my-app")
		buffer.receiveAddInformerEvent(app)

		assert.Equal(t, 1, addCalled)
		assert.Equal(t, "my-app", receivedApp.Name)
	})

}

func TestReceiveDeleteInformerEvent(t *testing.T) {
	t.Run("delete event calls delete handler immediately", func(t *testing.T) {
		deleteCalled := 0
		var receivedApp *v1alpha1.Application

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 10*time.Second,
			nil, nil,
			func(obj *v1alpha1.Application) {
				receivedApp = obj
				deleteCalled++
			})

		app := informerbuffer_newTestApp("argocd", "my-app")
		buffer.receiveDeleteInformerEvent(app)

		assert.Equal(t, 1, deleteCalled)
		assert.Equal(t, "my-app", receivedApp.Name)
	})

	t.Run("delete event clears pending update for same resource", func(t *testing.T) {
		updateCalled := atomic.Int32{} // We need to use atomics and channels, etc, because receiveUpdateInformerEvent uses go-routines
		deleteCalled := make(chan struct{})

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 10*time.Second,
			nil,
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				updateCalled.Add(1)
			},
			func(obj *v1alpha1.Application) {
				close(deleteCalled)
			})

		app := informerbuffer_newTestApp("argocd", "my-app")

		// First update: processed immediately (first event opens opportunity window)
		buffer.receiveUpdateInformerEvent(app, informerbuffer_newTestAppWithLabel("argocd", "my-app", "Healthy"))
		assert.Eventually(t, func() bool {
			return updateCalled.Load() >= 1
		}, 2*time.Second, 1*time.Millisecond)

		// Second update: buffered because opportunity window is closed
		buffer.receiveUpdateInformerEvent(
			informerbuffer_newTestAppWithLabel("argocd", "my-app", "Healthy"),
			informerbuffer_newTestAppWithLabel("argocd", "my-app", "Degraded"),
		)

		// Delete should clear the buffered update and remove from instance map
		buffer.receiveDeleteInformerEvent(app)
		<-deleteCalled

		buffer.lock.Lock()
		_, exists := buffer.instanceMap["argocd/my-app"]
		buffer.lock.Unlock()
		assert.False(t, exists, "delete should have removed the resource from instanceMap")

		countAfterDelete := updateCalled.Load()

		// Advance clock past the throttle window — if the delete failed to clear the
		// buffered update, the event loop would deliver it now.
		clk.At(clk.Now().Add(time.Minute))

		assert.Never(t, func() bool {
			return updateCalled.Load() > countAfterDelete
		}, 20*time.Millisecond, 1*time.Millisecond, "buffered update should have been cleared by delete")
	})
}

func TestReceiveUpdateInformerEvent(t *testing.T) {
	t.Run("first update event is processed immediately", func(t *testing.T) {
		updateDone := make(chan struct{}, 1)
		var receivedOld, receivedNew *v1alpha1.Application
		var mu sync.Mutex

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 10*time.Second,
			nil,
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				mu.Lock()
				receivedOld = oldObj
				receivedNew = newObj
				mu.Unlock()
				updateDone <- struct{}{}
			}, nil)

		oldApp := informerbuffer_newTestAppWithLabel("argocd", "my-app", "Progressing")
		newApp := informerbuffer_newTestAppWithLabel("argocd", "my-app", "Healthy")

		buffer.receiveUpdateInformerEvent(oldApp, newApp)

		select {
		case <-updateDone:
			mu.Lock()
			assert.Equal(t, "Progressing", receivedOld.Labels["test-event"])
			assert.Equal(t, "Healthy", receivedNew.Labels["test-event"])
			mu.Unlock()
		case <-time.After(2 * time.Second):
			t.Fatal("first update event was not processed in time")
		}
	})

	t.Run("rapid updates are throttled and only latest is delivered", func(t *testing.T) {
		var updateCalls []string
		updateMu := sync.Mutex{}
		updateDone := make(chan struct{}, 2)

		minInterval := 10 * time.Second
		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, minInterval,
			nil,
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				updateMu.Lock()
				updateCalls = append(updateCalls, newObj.Labels["test-event"])
				updateMu.Unlock()
				updateDone <- struct{}{}
			}, nil)

		base := informerbuffer_newTestApp("argocd", "my-app")

		// First update: processed immediately
		buffer.receiveUpdateInformerEvent(base, informerbuffer_newTestAppWithLabel("argocd", "my-app", "event-1"))
		<-updateDone

		// Second and third updates arrive within the throttle window
		buffer.receiveUpdateInformerEvent(base, informerbuffer_newTestAppWithLabel("argocd", "my-app", "event-2"))
		buffer.receiveUpdateInformerEvent(base, informerbuffer_newTestAppWithLabel("argocd", "my-app", "event-3"))

		// Advance clock past the throttle window so the event loop delivers the buffered event
		clk.At(clk.Now().Add(minInterval + time.Second))

		select {
		case <-updateDone:
		case <-time.After(2 * time.Second):
			t.Fatal("throttled update event was not delivered")
		}

		updateMu.Lock()
		defer updateMu.Unlock()

		require.Equal(t, len(updateCalls), 2)
		assert.Equal(t, "event-1", updateCalls[0])
		assert.Equal(t, "event-3", updateCalls[1])
	})

	t.Run("updates for different resources are independent", func(t *testing.T) {
		updateDone := make(chan string, 2)

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 10*time.Second,
			nil,
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				updateDone <- newObj.Name
			}, nil)

		buffer.receiveUpdateInformerEvent(
			informerbuffer_newTestApp("argocd", "app-1"),
			informerbuffer_newTestApp("argocd", "app-1"),
		)
		buffer.receiveUpdateInformerEvent(
			informerbuffer_newTestApp("argocd", "app-2"),
			informerbuffer_newTestApp("argocd", "app-2"),
		)

		received := map[string]bool{}
		for i := range 2 {
			select {
			case name := <-updateDone:
				received[name] = true
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for update %d", i+1)
			}
		}

		assert.True(t, received["app-1"], "app-1 should have been updated")
		assert.True(t, received["app-2"], "app-2 should have been updated")
	})
}

func TestEventBufferThrottleWindow(t *testing.T) {
	t.Run("update after window expires is processed immediately", func(t *testing.T) {
		updateDone := make(chan struct{}, 2)

		minInterval := 10 * time.Second
		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, minInterval,
			nil,
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				updateDone <- struct{}{}
			}, nil)

		app := informerbuffer_newTestApp("argocd", "my-app")

		// First update: immediate
		buffer.receiveUpdateInformerEvent(app, app)
		<-updateDone

		// Advance clock past the throttle window
		clk.At(clk.Now().Add(minInterval + time.Second))

		// Second update should be processed immediately since window expired
		buffer.receiveUpdateInformerEvent(app, app)

		select {
		case <-updateDone:
			// success — processed without needing real time to pass
		case <-time.After(2 * time.Second):
			t.Fatal("update after window expiry was not processed")
		}
	})
}

func TestAddDeleteClearsBufferedState(t *testing.T) {
	t.Run("add event removes entry from instance map", func(t *testing.T) {
		addDone := make(chan struct{})
		updateDone := make(chan struct{}, 1)

		clk := clock.SeededClock(time.Now())
		buffer := informerbuffer_newTestBuffer(clk, 60*time.Second,
			func(obj *v1alpha1.Application) {
				close(addDone)
			},
			func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {
				updateDone <- struct{}{}
			}, nil)

		app := informerbuffer_newTestApp("argocd", "my-app")

		// First update: processed immediately, opens throttle window
		buffer.receiveUpdateInformerEvent(app, app)
		<-updateDone

		// Second update: buffered (window closed)
		buffer.receiveUpdateInformerEvent(app, informerbuffer_newTestAppWithLabel("argocd", "my-app", "Healthy"))

		// Add event should clear buffered state and remove from instance map
		buffer.receiveAddInformerEvent(app)
		<-addDone

		buffer.lock.Lock()
		_, exists := buffer.instanceMap["argocd/my-app"]
		buffer.lock.Unlock()

		assert.False(t, exists, "add should remove entry from instanceMap")
	})
}

func bufferTestLogFn() *logrus.Entry {
	logger := logrus.New()
	logger.SetLevel(logrus.TraceLevel)
	return logrus.NewEntry(logger)
}

func informerbuffer_newTestApp(namespace, name string) *v1alpha1.Application {
	return &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func informerbuffer_newTestAppWithLabel(namespace, name, labelValue string) *v1alpha1.Application {
	return &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels:    map[string]string{"test-event": labelValue},
		},
	}
}

func informerbuffer_newTestBuffer(
	clk clock.Clock,
	minInterval time.Duration,
	addFn func(obj *v1alpha1.Application),
	updateFn func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application),
	deleteFn func(obj *v1alpha1.Application),
) *informerEventBuffer[*v1alpha1.Application] {
	if addFn == nil {
		addFn = func(obj *v1alpha1.Application) {}
	}
	if updateFn == nil {
		updateFn = func(oldObj *v1alpha1.Application, newObj *v1alpha1.Application) {}
	}
	if deleteFn == nil {
		deleteFn = func(obj *v1alpha1.Application) {}
	}
	return newInformerEventBuffer[*v1alpha1.Application](
		addFn, updateFn, deleteFn, minInterval, clk, 1*time.Millisecond,
		func() *logrus.Entry { return bufferTestLogFn() },
	)
}
