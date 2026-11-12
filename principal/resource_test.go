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
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/argocd/cluster"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/event/targets"
	"github.com/argoproj-labs/argocd-agent/principal/apis/eventstream"
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
	s.eventStreamSrv = eventstream.NewServer(
		s.queues, event.NewEventWritersMap(), nil, &cluster.Manager{},
	)
	rp, err := resourceproxy.New("127.0.0.1:0")
	require.NoError(t, err)
	s.resourceProxy = rp
	return s
}

func Test_resourceRequester(t *testing.T) {
	t.Run("Successfully request a resource", func(t *testing.T) {
		s := newResourceTestServer(t)
		s.eventStreamSrv.MarkConnected("agent")
		defer s.eventStreamSrv.MarkDisconnected("agent")
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
		ch := make(chan any)
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
		ch := make(chan any)
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
		ch := make(chan any)
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
		ch := make(chan any)
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
		assert.NotNil(t, s.queues.SendQ("agent"))
		assert.False(t, s.eventStreamSrv.IsAgentConnected("agent"))
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
		ch := make(chan any)
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
		s.eventStreamSrv.MarkConnected("agent")
		defer s.eventStreamSrv.MarkDisconnected("agent")
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
		ch := make(chan any)
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
		s.eventStreamSrv.MarkConnected("agent")
		defer s.eventStreamSrv.MarkDisconnected("agent")
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
		ch := make(chan any)
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

func Test_resourceRequester_impersonation(t *testing.T) {
	newRequest := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: "agent"}}},
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return r
	}

	// proxy runs processResourceRequest to completion and returns the resource
	// request that reached the agent's queue, if any.
	proxy := func(t *testing.T, r *http.Request, subresource string) (*event.ResourceRequest, *httptest.ResponseRecorder) {
		t.Helper()
		s := newResourceTestServer(t)
		s.eventStreamSrv.MarkConnected("agent")
		defer s.eventStreamSrv.MarkDisconnected("agent")
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			params := resourceproxy.NewParams()
			if subresource != "" {
				params.Set("subresource", subresource)
			}
			s.processResourceRequest(w, r, params)
		}()

		sendq := s.queues.SendQ("agent")
		require.NotNil(t, sendq)
		var rreq *event.ResourceRequest
		require.Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
				return sendq.Len() > 0
			}
		}, 5*time.Second, 5*time.Millisecond)
		select {
		case <-done:
			// Refused before anything was queued.
			assert.Equal(t, 0, sendq.Len())
		default:
			ev, shutdown := sendq.Get()
			require.False(t, shutdown)
			var err error
			rreq, err = event.New(ev, targets.Resource).ResourceRequest()
			require.NoError(t, err)
			_, sendCh := s.resourceProxy.Tracked(event.EventID(ev))
			require.NotNil(t, sendCh)
			sendCh <- s.events.NewResourceResponseEvent(event.EventID(ev), 200, "ok")
			<-done
		}
		return rreq, w
	}

	t.Run("service account from the Impersonate-User header reaches the agent", func(t *testing.T) {
		const user = "system:serviceaccount:tenant-a:deployer"
		rreq, w := proxy(t, newRequest(map[string]string{"Impersonate-User": user}), "")
		require.NotNil(t, rreq)
		assert.Equal(t, user, rreq.Impersonate)
		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	t.Run("no header means no impersonation", func(t *testing.T) {
		rreq, w := proxy(t, newRequest(nil), "")
		require.NotNil(t, rreq)
		assert.Empty(t, rreq.Impersonate)
		assert.Equal(t, http.StatusOK, w.Result().StatusCode)
	})

	refused := []struct {
		name    string
		headers map[string]string
	}{
		{"user that is not a service account", map[string]string{"Impersonate-User": "system:masters"}},
		{"plain user name", map[string]string{"Impersonate-User": "admin"}},
		{"malformed service account", map[string]string{"Impersonate-User": "system:serviceaccount:tenant-a"}},
		{"groups next to a valid user", map[string]string{
			"Impersonate-User":  "system:serviceaccount:tenant-a:deployer",
			"Impersonate-Group": "system:masters",
		}},
		{"groups without a user", map[string]string{"Impersonate-Group": "system:masters"}},
		{"uid next to a valid user", map[string]string{
			"Impersonate-User": "system:serviceaccount:tenant-a:deployer",
			"Impersonate-Uid":  "1234",
		}},
		{"extra next to a valid user", map[string]string{
			"Impersonate-User":         "system:serviceaccount:tenant-a:deployer",
			"Impersonate-Extra-Scopes": "all",
		}},
		{"empty user", map[string]string{"Impersonate-User": ""}},
	}
	for _, tt := range refused {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			rreq, w := proxy(t, newRequest(tt.headers), "")
			assert.Nil(t, rreq, "request must not reach the agent")
			assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		})
	}

	// Logs and exec do not carry the identity to the agent yet, so they must
	// not be served at all rather than with the agent's own identity.
	for _, sub := range []string{"log", "exec"} {
		t.Run("refuses "+sub+" while impersonating", func(t *testing.T) {
			r := newRequest(map[string]string{"Impersonate-User": "system:serviceaccount:tenant-a:deployer"})
			rreq, w := proxy(t, r, sub)
			assert.Nil(t, rreq, "request must not reach the agent")
			assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
		})
	}

	t.Run("refuses several Impersonate-User values", func(t *testing.T) {
		r := newRequest(nil)
		r.Header.Add("Impersonate-User", "system:serviceaccount:tenant-a:deployer")
		r.Header.Add("Impersonate-User", "system:serviceaccount:tenant-b:admin")
		rreq, w := proxy(t, r, "")
		assert.Nil(t, rreq, "request must not reach the agent")
		assert.Equal(t, http.StatusForbidden, w.Result().StatusCode)
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
