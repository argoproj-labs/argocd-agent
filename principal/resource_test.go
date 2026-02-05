package principal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
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
		// Create a certificate with agent name in CN
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "agent",
			},
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
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
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		ch := make(chan interface{})
		go func() {
			s.processResourceRequest(w, r, resourceproxy.NewParams())
			ch <- 1
		}()
		<-ch
		// No authentication method available
		assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "authentication failed", string(body))
	})

	t.Run("TLS cert with empty CN fails", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/", nil)
		// TLS cert with empty Common Name
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
		// No bearer token and TLS cert has empty CN
		assert.Equal(t, http.StatusUnauthorized, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "authentication failed", string(body))
	})

	t.Run("Invalid agent name in TLS cert CN", func(t *testing.T) {
		s := newResourceTestServer(t)
		// Create a certificate with invalid agent name in CN
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "lob/bo",
			},
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}
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
		assert.Equal(t, "invalid agent name", string(body))
	})

	t.Run("Agent not connected", func(t *testing.T) {
		s := newResourceTestServer(t)
		s.queues.Delete("agent", false)
		// Create a certificate with agent name in CN
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "agent",
			},
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
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
		// Create a certificate with agent name in CN
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "agent",
			},
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
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
		// Create a certificate with agent name in CN
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "agent",
			},
		}
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
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

func Test_extractAgentFromAuth(t *testing.T) {
	t.Run("Extracts agent from valid bearer token", func(t *testing.T) {
		s := newResourceTestServer(t)

		// Issue a valid resource proxy token
		token, err := s.issuer.IssueResourceProxyToken("test-agent")
		require.NoError(t, err)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)

		agentName, err := s.extractAgentFromAuth(r)
		require.NoError(t, err)
		assert.Equal(t, "test-agent", agentName)
	})

	t.Run("Returns error for invalid bearer token", func(t *testing.T) {
		s := newResourceTestServer(t)

		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer invalid-token")

		_, err := s.extractAgentFromAuth(r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid bearer token")
	})

	t.Run("No authorization returns error", func(t *testing.T) {
		s := newResourceTestServer(t)

		r := httptest.NewRequest("GET", "/", nil)

		_, err := s.extractAgentFromAuth(r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no authorization found")
	})

	t.Run("Legacy mode extracts agent from TLS cert CN", func(t *testing.T) {
		s := newResourceTestServer(t)

		// Create a certificate with a Common Name
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "tls-cert-agent",
			},
		}

		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}

		agentName, err := s.extractAgentFromAuth(r)
		require.NoError(t, err)
		assert.Equal(t, "tls-cert-agent", agentName)
	})

	t.Run("TLS cert with empty CN returns error", func(t *testing.T) {
		s := newResourceTestServer(t)

		// Create a certificate with empty Common Name
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "",
			},
		}

		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}

		_, err := s.extractAgentFromAuth(r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no authorization found")
	})

	t.Run("Bearer token takes precedence over TLS cert CN", func(t *testing.T) {
		s := newResourceTestServer(t)

		token, err := s.issuer.IssueResourceProxyToken("token-agent")
		require.NoError(t, err)

		// Create a certificate with a different agent name
		cert := &x509.Certificate{
			Subject: pkix.Name{
				CommonName: "cert-agent",
			},
		}

		// Request has both bearer token and TLS cert with different agent names
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{cert},
		}

		agentName, err := s.extractAgentFromAuth(r)
		require.NoError(t, err)
		// Should use bearer token's agent name, not cert CN
		assert.Equal(t, "token-agent", agentName)
	})
}
