// Copyright 2024 The argocd-agent Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package principal

import (
	"context"
	"net/http"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/resourceproxy"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// resourceRequestRegexp is the regexp used to match requests for retrieving a
// resource or a list of resources from the server
const resourceRequestRegexp = `^/(?:api|apis/(?P<group>[^\/]+))/(?P<version>v[^\/]+)(?:/(?:namespaces/(?P<namespace>[^\/]+)/)?)?(?:(?P<resource>[^\/]+)(?:/(?P<name>[^\/]+))?)?$`

const requestTimeout = 10 * time.Second

func (s *Server) resourceRequester(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
	logCtx := log().WithField("function", "resourceRequester")

	// Make sure our request carries a client certificate
	if len(r.TLS.PeerCertificates) < 1 {
		logCtx.Errorf("Unauthenticated request from client %s", r.RemoteAddr)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// For now, we can only validate the first client cert presented by the
	// client. Under normal circumstances, the client is the Argo CD API,
	// which uses the client certificate of the cluster secret, which is
	// usually configured by us.
	cert := r.TLS.PeerCertificates[0]

	// Make sure the agent name in the certificate is good
	agentName := cert.Subject.CommonName
	errs := validation.NameIsDNSLabel(agentName, false)
	if len(errs) > 0 {
		logCtx.Errorf("CRITICAL: Invalid agent name in client certificate: %v", errs)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	logCtx = logCtx.WithField("agent", cert.Subject.CommonName)

	// Agent is not connected. Return early.
	if !s.queues.HasQueuePair(agentName) {
		logCtx.Debugf("Agent is not connected, stop proxying")
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Errorf("Help! Queue disappeared")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	gvr := metav1.GroupVersionResource{
		Group:    params.Get("group"),
		Resource: params.Get("resource"),
		Version:  params.Get("version"),
	}

	// Create the event
	sentEv, err := s.events.NewResourceRequestEvent(gvr, params.Get("namespace"), params.Get("name"))
	if err != nil {
		logCtx.Errorf("Could not create event: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Remember the resource ID of the sent event
	sentUuid := event.EventID(sentEv)

	// Start tracking the event, so we can later get the response
	eventCh, err := s.resourceProxy.Track(sentUuid, agentName)
	if err != nil {
		logCtx.Errorf("Could not track event %s: %v", sentUuid, err)
	}
	defer s.resourceProxy.StopTracking(sentUuid)

	// Submit the event to the queue
	logCtx.Tracef("Submitting event: %v", sentEv)
	q.Add(sentEv)

	logCtx.Infof("Proxying request for resource %s %s/%s", params.Get("resource"), params.Get("namespace"), params.Get("name"))

	// Wait for the event from the agent
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	defer cancel()
	defer func() {
		if err := r.Body.Close(); err != nil {
			logCtx.WithError(err).Error("Uh oh")
		}
	}()
	for {
		select {
		case <-ctx.Done():
			log().Infof("Timeout communicating to the agent, closing proxy connection.")
			w.WriteHeader(http.StatusGatewayTimeout)
			w.Write([]byte("Timeout communicating to agent.\n"))
			return
		case rcvdEv, ok := <-eventCh:
			// Channel was closed. Bail out.
			if !ok {
				log().Info("EventQueue has closed the channel")
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			// Make sure that we have the right response event
			rcvdUuid := event.EventID(rcvdEv)
			if rcvdUuid != sentUuid {
				log().Error("Received mismatching UUID in response")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// Get the resource out of the event
			resp := &event.ResourceResponse{}
			err = rcvdEv.DataAs(resp)
			if err != nil {
				logCtx.WithError(err).Error("Could not get data from event")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			// We are good to send the response to the caller
			log().Info("Writing resource to caller")
			w.Header().Set("Content-type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(resp.Resource))
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}
