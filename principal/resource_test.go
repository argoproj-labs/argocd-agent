package principal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net/http"
	"net/http/httptest"
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
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClient(), "argocd",
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
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{
				{
					Subject: pkix.Name{CommonName: "agent"},
				},
			},
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
		assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
		defer w.Result().Body.Close()
		body, err := io.ReadAll(w.Result().Body)
		require.NoError(t, err)
		assert.Equal(t, "no authorization found", string(body))
	})
	t.Run("Invalid agent name", func(t *testing.T) {
		s := newResourceTestServer(t)
		r := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{
				{
					Subject: pkix.Name{CommonName: "lob/bo"},
				},
			},
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
		assert.Equal(t, "invalid client certificate", string(body))

	})

	t.Run("Agent not connected", func(t *testing.T) {
		s := newResourceTestServer(t)
		s.queues.Delete("agent", false)
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{
				{
					Subject: pkix.Name{CommonName: "agent"},
				},
			},
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
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{
				{
					Subject: pkix.Name{CommonName: "agent"},
				},
			},
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
		r := httptest.NewRequest("GET", "/", nil)
		r.TLS = &tls.ConnectionState{
			PeerCertificates: []*x509.Certificate{
				{
					Subject: pkix.Name{CommonName: "agent"},
				},
			},
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
