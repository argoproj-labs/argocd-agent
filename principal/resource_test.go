package principal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResourceTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps("argocd"), "argocd",
		WithGeneratedTLS("principal"),
		WithGeneratedTokenSigningKey())
	require.NoError(t, err)
	s.queues.Create("agent")
	s.events = event.NewEventSource("principal")
	rp, err := resourceproxy.New("127.0.0.1:0")
	require.NoError(t, err)
	s.resourceProxy = rp
	return s
}

func Test_resourceRequester(t *testing.T) {
	t.Run("Successfully request a resource", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/?agentName=agent", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()

		// We need the submission channel from the resource tracker, which is bound
		// to the event's UUID. So we just take the event the resource proxy has
		// created from the send queue.
		sendq := s.queues.SendQ("agent")
		assert.NotNil(t, sendq)
		ev, shutdown := sendq.Get()
		assert.False(t, shutdown)
		assert.NotNil(t, ev)
		agent, sendCh := s.resourceProxy.Tracked(event.EventID(ev))
		assert.Equal(t, "agent", agent)
		require.NotNil(t, sendCh)
		rev := s.events.NewResourceResponseEvent(event.EventID(ev), 200, "foo")
		sendCh <- rev
		<-ch
		assert.Equal(t, 200, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "foo", string(body))
	})

	t.Run("No TLS data in request", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/?agentName=agent", nil)
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()
		<-ch
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "no authorization found", string(body))
	})

	t.Run("Missing agentName query parameter", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()
		<-ch
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "missing agentName query parameter", string(body))
	})

	t.Run("Invalid agent name", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/?agentName=lob/bo", nil)
		w := httptest.NewRecorder()
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()
		<-ch
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "invalid agentName query parameter", string(body))

	})

	t.Run("Agent not connected", func(t *testing.T) {
		s := newResourceTestServer(t)
		s.queues.Delete("agent", false)
		r := httptest.NewRequest("GET", "/?agentName=agent", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()
		<-ch
		assert.Equal(t, http.StatusBadGateway, w.Result().StatusCode)
		defer w.Result().Body.Close()
	})

	t.Run("Receiving a different resource", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/?agentName=agent", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()

		// We need the submission channel from the resource tracker, which is bound
		// to the event's UUID. So we just take the event the resource proxy has
		// created from the send queue.
		sendq := s.queues.SendQ("agent")
		assert.NotNil(t, sendq)
		ev, shutdown := sendq.Get()
		assert.False(t, shutdown)
		assert.NotNil(t, ev)
		agent, sendCh := s.resourceProxy.Tracked(event.EventID(ev))
		assert.Equal(t, "agent", agent)
		require.NotNil(t, sendCh)
		rev := s.events.NewResourceResponseEvent("1-2-3", 200, "foo")
		sendCh <- rev
		<-ch
		assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		defer w.Result().Body.Close()
	})

	t.Run("Receiving a different event", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/?agentName=agent", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{}},
		}
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()

		// We need the submission channel from the resource tracker, which is bound
		// to the event's UUID. So we just take the event the resource proxy has
		// created from the send queue.
		sendq := s.queues.SendQ("agent")
		assert.NotNil(t, sendq)
		ev, shutdown := sendq.Get()
		assert.False(t, shutdown)
		assert.NotNil(t, ev)
		agent, sendCh := s.resourceProxy.Tracked(event.EventID(ev))
		assert.Equal(t, "agent", agent)
		require.NotNil(t, sendCh)
		rev := cloudevents.NewEvent()
		rev.SetExtension("eventid", event.EventID(ev))
		rev.SetData("text/plain", "foo")
		sendCh <- &rev
		<-ch
		assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
		defer w.Result().Body.Close()
	})

}

func Test_resourceRegexp(t *testing.T) {
	tc := []struct {
		url       string
		expMatch  bool
		expParams []string // in order: group, version, resource, namespace, name
	}{
		// API requests
		{"/api", true, []string{"", "", "", "", ""}},
		{"/apis", true, []string{"", "", "", "", ""}},

		{"/api/v1/namespaces/foo/secrets/bar", true, []string{"", "v1", "secrets", "foo", "bar"}},
		{"/api/v1/secrets/bar", true, []string{"", "v1", "secrets", "", "bar"}},
		{"/apis/apps/v1/namespaces/foo/secrets/bar", true, []string{"apps", "v1", "secrets", "foo", "bar"}},
		{"/apis/apps/v1/secrets/bar", true, []string{"apps", "v1", "secrets", "", "bar"}},
		{"/apis/apps/v1", true, []string{"apps", "v1", "", "", ""}},

		// There are no namespace scoped kinds
		{"/apis/apps/v1/namespaces/foo", true, []string{"apps", "v1", "namespaces", "", "foo"}},
		{"/api/v1/namespaces/foo", true, []string{"", "v1", "namespaces", "", "foo"}},

		// The /apis endpoint needs a group qualifier
		{"/apis/v1/namespaces/foo/secrets/bar", false, []string{"", "v1", "secrets", "foo", "bar"}},
		{"/apis/v1/secrets/bar", false, []string{"", "v1", "secrets", "foo", "bar"}},

		// The /api endpoint must not have a group qualifier
		{"/api/apps/v1/namespaces/foo/deployments/bar", false, []string{"", "v1", "secrets", "foo", "bar"}},
		{"/api/apps/v1/deployments/bar", false, []string{"", "v1", "secrets", "foo", "bar"}},
	}

	for i, tt := range tc {
		t.Run(fmt.Sprintf("TC %d", i+1), func(t *testing.T) {
			re := regexp.MustCompile(resourceRequestRegexp)
			matches := re.FindStringSubmatch(tt.url)
			if tt.expMatch {
				require.NotEmpty(t, matches)
			} else {
				require.Empty(t, matches)
				return
			}
			for i, n := range []string{"group", "version", "resource", "namespace", "name"} {
				idx := re.SubexpIndex(n)
				require.NotEqual(t, -1, idx) // all groups must be set, even when empty
				assert.Equal(t, tt.expParams[i], matches[idx], "%s: expecting %s to be set to %s, but is %s", tt.url, n, tt.expParams[i], matches[idx])
			}
		})
	}
}
