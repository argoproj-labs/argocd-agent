package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	v1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type mode string

const (
	modeSource      mode = "source"
	modeDestination mode = "destination"
)

type direction string

const (
	direction_SpecWriter    direction = "SpecWriter"
	direction_SpecWatcher   direction = "SpecWatcher"
	direction_StatusWriter  direction = "StatusWriter"
	direction_StatusWatcher direction = "StatusWatcher"
)

type applicationEvent struct {

	// appName is the name of Argo CD Application resource
	appName string

	// eventType is the watch event
	eventType watch.EventType

	// direction indicates whether the event was generated from writer/watch side, and whether the event is spec/status related
	direction direction

	// mode is either destination or source, depending on whether the event was an action (write/patch on source) or an observation (from watch on destination peer)
	mode mode

	// value from app.Spec.Source.RepoURL (should be empty if statusValue is non-empty, and vice versa)
	repoURL string

	// value from app.Status.Sync.Revision
	statusValue string

	// dateTime is the time at which the event occurred
	dateTime time.Time

	// monotonically increasing event id
	eventId int

	// resourceVersion is the observed resourceVersion of the the K8s resource. Not guaranteed to have a value at all times (e.g. in the delete case)
	resourceVersion string

	// afterContextCancel indicates whether or not an event was received after the writer context was cancelled:
	// - This is only set by writers
	// - If false, the event was necessarily written to Application via K8s api
	// - If true, the event MAY or MAY NOT have been written to source of truth (it depends which point in time the context was cancelled). We thus ignore this event for some of our correctness checks.
	afterContextCancel bool
}

func (le *applicationEvent) string() string {

	var val string
	if le.repoURL != "" {
		val = le.repoURL
	} else {
		val = le.statusValue
	}

	var post string
	if le.afterContextCancel {
		post = " (AFTER CONTEXT CANCEL)"
	}

	return fmt.Sprintf("%s %s %s %s -> %s @ %v [%s] (%d)%s", le.appName, le.eventType, le.mode, le.direction, val, le.dateTime, le.resourceVersion, le.eventId, post)
}

// deduplicateEvents is used to remove list events with duplicate repoURL or statusValues
// - provide 'keyFunc' param to specify which value to dedupe
func deduplicateEvents(events []*applicationEvent, keyFunc func(*applicationEvent) string) []*applicationEvent {
	seen := map[string]bool{}
	deduped := make([]*applicationEvent, 0, len(events))
	for _, e := range events {
		key := keyFunc(e)
		if seen[key] {
			continue
		}
		seen[key] = true
		deduped = append(deduped, e)
	}
	return deduped
}

// deleteOldApplicationsOnStartup cleans up ALL Applications on workload/control plane
func deleteOldApplicationsOnStartup(controlPlaneClient client.Client, managedAgentClient client.Client) (map[string]bool, error) {

	var deletedMu sync.Mutex
	applicationsDeleted := map[string]bool{}

	// First delete Applications from control plane
	{
		var apps v1alpha1.ApplicationList
		if err := controlPlaneClient.List(context.Background(), &apps, client.InNamespace("agent-managed")); err != nil {
			return nil, fmt.Errorf("startup: list applications on %s: %w", "control-plane", err)
		}

		var toDelete []*v1alpha1.Application
		for i := range apps.Items {
			if strings.HasPrefix(apps.Items[i].Name, "event-generator-app") {
				toDelete = append(toDelete, &apps.Items[i])
			}
		}

		sem := make(chan struct{}, 50)
		var wg sync.WaitGroup
		var delErr error
		var delErrOnce sync.Once
		for _, app := range toDelete {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()

				deletedMu.Lock()
				applicationsDeleted[app.Name] = true
				deletedMu.Unlock()

				// Delete Application, return error if it fails
				if err := controlPlaneClient.Delete(context.Background(), app, &client.DeleteOptions{PropagationPolicy: new(metav1.DeletePropagationForeground)}); err != nil {
					if apierrors.IsNotFound(err) {
						return
					}
					delErrOnce.Do(func() {
						delErr = fmt.Errorf("error on delete application %q on %s: %w", app.Name, "control-plane", err)
					})
					return
				}
				if outputVerboseText {
					fmt.Printf("* Startup: Deleted leftover application %q on %s\n", app.Name, "control-plane")
				}

				elapsedTime := 0
				// Verify Application no longer exists
				for {
					getApp := v1alpha1.Application{ObjectMeta: metav1.ObjectMeta{
						Name:      app.Name,
						Namespace: app.Namespace,
					}}
					err := controlPlaneClient.Get(context.Background(), client.ObjectKeyFromObject(&getApp), &getApp)
					if apierrors.IsNotFound(err) {
						break
					}
					if err != nil {
						delErrOnce.Do(func() {
							delErr = fmt.Errorf("error on waiting for deletion of %q on %s: %w", app.Name, "control-plane", err)
						})
						return
					}
					time.Sleep(1 * time.Second)
					elapsedTime++
					if elapsedTime > 10 {
						fmt.Println("* Waiting for deletion of:", getApp.Name, getApp.Namespace)
					}
				}
			}()
		}
		wg.Wait()
		if delErr != nil {
			return nil, delErr
		}
	}

	// Next, wait for there to no longer exist Applications on managed-agent
	// - Ideally we would ALSO try to delete applications on managed-agent, but that is blocked by argocd-agent/1056
	for {
		var apps v1alpha1.ApplicationList
		if err := managedAgentClient.List(context.Background(), &apps, client.InNamespace("argocd-managed")); err != nil {
			return nil, fmt.Errorf("startup: list applications on %s: %w", "managed-agent", err)
		}

		if len(apps.Items) == 0 {
			break
		} else {
			names := make([]string, len(apps.Items))
			for i := range apps.Items {
				names[i] = apps.Items[i].Name
			}
			fmt.Println("Waiting for deletion:", len(apps.Items), names)
			time.Sleep(1 * time.Second)
		}

	}

	return applicationsDeleted, nil
}

func randString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[rand.IntN(len(letters))]
	}
	return string(b)
}

func randomKeyFromAppNameMap(m map[string]bool) string {
	idx := rand.IntN(len(m))
	for k := range m {
		if idx == 0 {
			return k
		}
		idx--
	}
	exit("unexpected failure state")
	return ""
}

func getK8sClientByContextName(contextName string, qps float32, burst int, disableRateLimiter bool) (client.WithWatch, error) {
	scheme := runtime.NewScheme()

	if err := corev1.AddToScheme(scheme); err != nil {
		exit(fmt.Sprintf("error adding scheme: %v\n", err))
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		exit(fmt.Sprintf("error adding scheme: %v\n", err))
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		exit(fmt.Sprintf("error adding scheme: %v\n", err))
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{CurrentContext: contextName},
	).ClientConfig()
	if err != nil {
		exit(fmt.Sprintf("error getting kubeconfig: %v\n", err))
	}

	cfg.QPS = qps
	cfg.Burst = burst
	if disableRateLimiter {
		cfg.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	}

	return client.NewWithWatch(cfg, client.Options{Scheme: scheme})
}

func (s *eventState) initEvent(event *applicationEvent) {
	event.dateTime = time.Now()
	s.mutex.Lock()
	event.eventId = s.nextEventId
	s.nextEventId++
	s.mutex.Unlock()
}

func (s *eventState) recordEvent(event applicationEvent) {

	// Sanity test that values have been set by caller
	if event.dateTime.IsZero() {
		debug.PrintStack()
		exit("zero data time")
	}
	if event.eventId == 0 {
		debug.PrintStack()
		exit("zero event id")
	}

	s.mutex.Lock()
	s.allEvents = append(s.allEvents, &event)
	s.mutex.Unlock()

	if outputTextWhileRunning {
		fmt.Printf("Event recorded: %s\n", event.string())
	}

}

func outputEventList(name string, events []*applicationEvent, showRepo bool) {
	fmt.Println(name + ":")
	for x, event := range events {

		if showRepo && event.repoURL == "" {
			continue
		}

		if !showRepo && event.repoURL != "" {
			continue
		}

		fmt.Printf("%d) %s\n", x, event.string())
	}
}

func exitSuccess(str string) {
	fmt.Println("* Exit:", str)
	os.Exit(0)
}

func exit(str string) {
	fmt.Fprintln(os.Stderr, "* Exit:", str)
	os.Exit(1)
}

// This function disables the application controller (when Argo CD is installed in default dev-env configuration).
// - One of the goals of this utility is to simulate '.status' updates to Applications, and thus we need to disable app controller to avoid it stepping on our toes (creating new, unexpected events)
// - Returns nil if app controller already is disabled.
func disableApplicationController(managedAgentClient client.Client) error {
	statefulset := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "argocd-application-controller",
			Namespace: "argocd-managed",
		},
	}
	if err := managedAgentClient.Get(context.Background(), client.ObjectKeyFromObject(&statefulset), &statefulset); err != nil {
		return fmt.Errorf("unable to get app controller statefulset: %v", err)
	}

	if statefulset.Spec.Replicas != nil && *statefulset.Spec.Replicas != 0 {

		fmt.Println("* Setting Application Controller replicas to 0 on managed agent cluster")

		statefulset.Spec.Replicas = new(int32(0))
		if err := managedAgentClient.Update(context.Background(), &statefulset); err != nil {
			return fmt.Errorf("unable to update app controller statefulset: %v", err)
		}
	}

	fmt.Println("* Waiting for application controller pod to terminate...")
	for {

		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "argocd-application-controller-0",
				Namespace: "argocd-managed",
			},
		}
		err := managedAgentClient.Get(context.Background(), client.ObjectKeyFromObject(&pod), &pod)
		if apierrors.IsNotFound(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("unable to get app controller pod: %v", err)
		}
		time.Sleep(2 * time.Second)
	}

	return nil
}
