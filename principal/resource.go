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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// resourceRequestRegexp is the regexp used to match requests for retrieving a
// resource or a list of resources from the server. It makes use of named
// capture groups. It also supports Kubernetes subresources.
const resourceRequestRegexp = `^/(?:api|apis|(?:api|apis/(?P<group>[^\/]+))/(?P<version>v[^\/]+)(?:/(?:namespaces/(?P<namespace>[^\/]+)/)?)?(?:(?P<resource>[^\/]+)(?:/(?P<name>[^\/]+)(?:/(?P<subresource>[^\/]+))?)?)?)$`

// requestTimeout is the timeout that's being applied to requests for any live
// resource.
//
// TODO(jannfis): Make the timeout configurable
const requestTimeout = 10 * time.Second

// processResourceRequest is being executed by the resource proxy once it
// received a request for a specific resource. It will encapsulate this request
// into an event and add this event to the target agent's event queue. It will
// then wait for a response from the agent, which comes in asynchronously.
func (s *Server) processResourceRequest(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
	logCtx := log().WithField("function", "resourceRequester")

	// Make sure our request carries a client certificate
	if r.TLS == nil || len(r.TLS.PeerCertificates) < 1 {
		logCtx.Errorf("Unauthenticated request from client %s", r.RemoteAddr)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("no authorization found"))
		return
	}

	// For now, we can only validate the first client cert presented by the
	// client. Under normal circumstances, the client is the Argo CD API,
	// which uses the client certificate of the cluster secret, which is
	// usually configured by us.
	cert := r.TLS.PeerCertificates[0]

	// Make sure the agent name in the certificate is properly formatted
	agentName := cert.Subject.CommonName
	errs := validation.NameIsDNSLabel(agentName, false)
	if len(errs) > 0 {
		logCtx.Errorf("CRITICAL: Invalid agent name in client certificate: %v", errs)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid client certificate"))
		return
	}

	logCtx = logCtx.WithField("agent", cert.Subject.CommonName)

	// Handle exec subresource separately.
	// because it requires WebSocket for bidirectional streaming
	subresource := params.Get("subresource")
	if subresource == "exec" {
		s.processTerminalRequest(w, r, params, agentName)
		return
	}

	// If the agent is not connected, return early
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

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		logCtx.Errorf("Could not read request body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := r.Body.Close(); err != nil {
			logCtx.WithError(err).Error("Uh oh")
		}
	}()

	reqParams := map[string]string{}
	for k, v := range r.URL.Query() {
		reqParams[k] = v[0]
	}

	requestedName := params.Get("name")
	requestedNamespace := params.Get("namespace")
	requestedSubresource := params.Get("subresource")

	// Create the event
	var sentEv *cloudevents.Event
	if requestedSubresource == "log" {
		if requestedNamespace == "" || requestedName == "" {
			logCtx.WithFields(logrus.Fields{
				"namespace": requestedNamespace,
				"pod":       requestedName,
				"params":    reqParams,
				"agent":     agentName,
			}).Error("Missing required parameters: namespace and pod are required")
			http.Error(w, "Missing required parameters: namespace and pod", http.StatusBadRequest)
			return
		}
		sentEv, err = s.events.NewLogRequestEvent(requestedNamespace, requestedName, r.Method, reqParams)
		if err != nil {
			logCtx.WithFields(logrus.Fields{
				"namespace": requestedNamespace,
				"pod":       requestedName,
				"params":    reqParams,
				"agent":     agentName,
			}).Errorf("Could not create container log event: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	} else {
		sentEv, err = s.events.NewResourceRequestEvent(gvr, requestedNamespace, requestedName, requestedSubresource, r.Method, reqBody, reqParams)
		if err != nil {
			logCtx.Errorf("Could not create event: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	// Remember the resource ID of the sent event
	sentUUID := event.EventID(sentEv)

	if requestedSubresource != "" {
		logCtx.Infof("Proxying request for subresource %s of resource %s named %s/%s", requestedSubresource, gvr.String(), requestedNamespace, requestedName)
	} else if requestedName != "" {
		logCtx.Infof("Proxying request for resource of type %s named %s/%s", gvr.String(), requestedNamespace, requestedName)
	} else {
		logCtx.Infof("Proxying request for resources of type %s in namespace %s", gvr.String(), requestedNamespace)
	}

	if requestedSubresource == "log" {
		logCtx.WithFields(logrus.Fields{
			"namespace": requestedNamespace,
			"pod":       requestedName,
			"params":    reqParams,
			"agent":     agentName,
			"uuid":      string(sentUUID),
		}).Info("Proxying pod log request")
		if err := s.logStream.RegisterHTTP(sentUUID, w, r); err != nil {
			logCtx.Errorf("Could not register HTTP writer for log streaming: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Submit the event to the queue
		logCtx.Tracef("Submitting event: %v", sentEv)
		q.Add(sentEv)

		// Decide static vs streaming based on follow=true
		isStreaming := strings.EqualFold(reqParams["follow"], "true")

		if isStreaming {
			// Keep handler alive until client disconnects
			logCtx.WithField("uuid", string(sentUUID)).Info("Streaming logs: waiting for client disconnect")
			<-r.Context().Done()
			logCtx.WithField("uuid", string(sentUUID)).Info("Client disconnected; end streaming handler")
		} else {
			// Static logs: wait for completion signal from logStream
			logCtx.WithField("uuid", string(sentUUID)).Info("Static logs: waiting for completion")
			if ok := s.logStream.WaitForCompletion(sentUUID, requestTimeout); !ok {
				logCtx.WithField("uuid", string(sentUUID)).Warn("Static logs timeout")
				// Best-effort: the writer may have already sent partial data.
				// Return 504 only if nothing has been sent yet. If RegisterHTTP
				// streams early chunks, this will be ignored by client.
				http.Error(w, "Timeout fetching logs from agent", http.StatusGatewayTimeout)
			}
		}
		// IMPORTANT: do not enter the standard eventCh loop for log requests.
		return

	}

	// Start tracking the event, so we can later for non log requests and get the response
	eventCh, err := s.resourceProxy.Track(sentUUID, agentName)
	if err != nil {
		logCtx.Errorf("Could not track event %s: %v", sentUUID, err)
	}
	defer func() {
		err := s.resourceProxy.StopTracking(sentUUID)
		if err != nil {
			logCtx.Warnf("Could not untrack %s: %v", sentUUID, err)
		}
	}()

	// Submit the event to the queue
	logCtx.Tracef("Submitting event: %v", sentEv)
	q.Add(sentEv)

	// Wait for the event from the agent
	ctx, cancel := context.WithTimeout(s.ctx, requestTimeout)
	defer cancel()

	// The response is being read through a channel that is kept open and
	// written to by the resource proxy.
	for {
		select {
		case <-ctx.Done():
			log().Infof("Timeout communicating to the agent, closing proxy connection.")
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		case rcvdEv, ok := <-eventCh:
			// Channel was closed. Bail out.
			if !ok {
				log().Info("EventQueue has closed the channel")
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}

			// Make sure that we have the right response event. This should
			// usually not happen, because the resource proxy has a mapping
			// of request to response, but we'll be vigilant.
			rcvdUUID := event.EventID(rcvdEv)
			if rcvdUUID != sentUUID {
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

			log().Infof("Status: %d", resp.Status)
			if resp.Status == http.StatusOK {
				// We are good to send the response to the caller
				log().Info("Writing resource to caller")
				w.Header().Set("Content-type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, err := w.Write([]byte(resp.Resource))
				if err != nil {
					log().Errorf("Could not write response to client: %v", err)
				}
			} else {
				w.WriteHeader(resp.Status)
			}
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// sendSynchronousRedisMessageToAgent is called to send a message (and wait for a response) to remote agent via principal's machinery
// - should not be used for asynchronous messages.
func (s *Server) sendSynchronousRedisMessageToAgent(agentName string, connectionUUID string, body event.RedisCommandBody) *event.RedisResponseBody {

	logCtx := log().WithField("function", "sendSynchronousRedisMessageToAgent").WithField("connectionUUID", connectionUUID).WithField("agentName", agentName)

	// If the agent is not connected, return early
	if !s.queues.HasQueuePair(agentName) {
		logCtx.Debugf("Agent is not connected, stop proxying")
		return nil
	}

	q := s.queues.SendQ(agentName)
	if q == nil {
		logCtx.Errorf("Help! Queue disappeared")
		return nil
	}

	// Create the event
	sentEv, err := s.events.NewRedisRequestEvent(connectionUUID, body)
	if err != nil {
		logCtx.Errorf("Could not create event: %v", err)
		return nil
	}

	// Remember the resource ID of the sent event
	sentEventUUID := event.EventID(sentEv)

	logCtx = logCtx.WithField("sendEventUUID", sentEventUUID)

	// Start tracking the event, so we can later get the response
	eventCh, err := s.redisProxy.EventIDTracker.Track(sentEventUUID, agentName)
	if err != nil {
		logCtx.Errorf("Could not track event %s: %v", sentEventUUID, err)
	}
	defer func() {
		err := s.redisProxy.EventIDTracker.StopTracking(sentEventUUID)
		if err != nil {
			logCtx.Warnf("Could not untrack %s: %v", sentEventUUID, err)
		}
	}()

	// Submit the event to the queue
	logCtx.Tracef("Submitting event: %v", sentEv)
	q.Add(sentEv)

	// Wait for the event from the agent (timeout after 60 seconds)
	ctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()

	// The response is being read through a channel that is kept open and
	// written to by the resource proxy.
	for {
		select {
		case <-ctx.Done():
			var outText string
			if body.Get != nil {
				outText = fmt.Sprintf("GET %v", *body.Get)
			} else if body.Subscribe != nil {
				outText = fmt.Sprintf("SUBSCRIBE %v", *body.Subscribe)
			}

			logCtx.Errorf("Timeout communicating to the agent: %v %s", ctx.Err(), outText)
			return nil
		case rcvdEv, ok := <-eventCh:
			// Channel was closed. Bail out.
			if !ok {
				logCtx.Tracef("EventQueue has closed the channel")
				return nil
			}

			// Make sure that we have the right response event. This should
			// usually not happen, because the resource proxy has a mapping
			// of request to response, but we'll be vigilant.
			rcvdUUID := event.EventID(rcvdEv)

			logCtx = logCtx.WithField("rcvdUUID", rcvdUUID)

			if rcvdUUID != sentEventUUID {
				logCtx.Error("Received mismatching UUID in response")
				return nil
			}

			// Get the resource out of the event
			resp := &event.RedisResponse{}
			err = rcvdEv.DataAs(resp)
			if err != nil {
				logCtx.WithError(err).Error("Could not get data from event")
				return nil
			}

			logCtx.Trace("Received redis response message")

			return &resp.Body
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}
