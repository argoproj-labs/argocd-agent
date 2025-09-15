package principal

import (
	"net/http"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/principal/resourceproxy"
	"github.com/sirupsen/logrus"
)

const containerLogRequestRegexp = `^/api/v1/namespaces/(?P<namespace>[^/]+)/pods/(?P<pod>[^/]+)/log(\?.*)?$`

// processContainerLogRequest handles container log requests from ArgoCD
func (s *Server) processContainerLogRequest(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
	logCtx := log().WithField("function", "processContainerLogRequest")

	// 🔍 LOG: Container log request handler called
	logCtx.WithFields(logrus.Fields{
		"url":         r.URL.String(),
		"method":      r.Method,
		"remote_addr": r.RemoteAddr,
		"accept":      r.Header.Get("Accept"),
		"user_agent":  r.Header.Get("User-Agent"),
	}).Info("🔍 CONTAINER_LOG_HANDLER: Called")

	// 1. TLS auth
	if r.TLS == nil || len(r.TLS.PeerCertificates) < 1 {
		logCtx.Errorf("Unauthenticated request from client %s", r.RemoteAddr)
		http.Error(w, "Client certificate required", http.StatusUnauthorized)
		return
	}
	cert := r.TLS.PeerCertificates[0]
	agentName := cert.Subject.CommonName

	// 2. Agent connection
	if !s.queues.HasQueuePair(agentName) {
		logCtx.Debugf("Agent %s is not connected", agentName)
		http.Error(w, "Agent not connected", http.StatusBadGateway)
		return
	}
	q := s.queues.SendQ(agentName)

	// 3. Params
	namespace := params.Get("namespace")
	podName := params.Get("pod")
	queryParams := r.URL.Query()
	container := queryParams.Get("container")

	// Validate required parameters
	if namespace == "" || podName == "" {
		logCtx.Error("Missing required parameters: namespace and pod are required")
		http.Error(w, "Missing required parameters: namespace and pod", http.StatusBadRequest)
		return
	}

	// Extract log parameters more efficiently
	logParams := make(map[string]string)
	supportedParams := []string{
		"follow", "tailLines", "sinceSeconds", "sinceTime",
		"timestamps", "previous", "insecureSkipTLSVerifyBackend",
		"limitBytes", "pretty", "stream",
	}
	for _, param := range supportedParams {
		if v := queryParams.Get(param); v != "" {
			logParams[param] = v
		}
	}

	logCtx = logCtx.WithFields(logrus.Fields{
		"namespace":  namespace,
		"pod":        podName,
		"container":  container,
		"agent":      agentName,
		"parameters": logParams,
	})
	logCtx.Infof("Processing container log request with %d parameters", len(logParams))

	// 4. Create event
	sentEv, err := s.events.NewLogRequestEvent(namespace, podName, r.Method, logParams)
	if err != nil {
		logCtx.Errorf("Could not create container log event: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	sentUUID := event.EventID(sentEv)

	// 5. Check if this is an EventSource request
	acceptHeader := r.Header.Get("Accept")
	isEventSource := strings.Contains(acceptHeader, "text/event-stream")

	logCtx.WithFields(logrus.Fields{
		"accept_header":  acceptHeader,
		"reqyest_url":    r.URL.String(),
		"is_eventsource": isEventSource,
		"user_agent":     r.Header.Get("User-Agent"),
	}).Info("🔍 DEBUG: Request type detection")

	// 5. Register HTTP writer *before* submitting to agent
	if err := s.logStream.RegisterHTTP(sentUUID, w, r); err != nil {
		logCtx.Errorf("Could not register HTTP writer for log streaming: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// 6. Submit to agent with cleanup on failure
	logCtx.Infof("Submitting container log request for pod %s/%s to agent %s", namespace, podName, agentName)
	q.Add(sentEv)

	// 7. Wait: static completion or client disconnect
	logCtx.WithField("request_id", sentUUID).Info("🔍 DEBUG: Waiting for LogStream to complete...")

	// Check if this is a streaming request (follow=true)
	isStreaming := logParams["follow"] == "true"

	if isStreaming {
		// For streaming logs: keep handler alive until client disconnects
		logCtx.WithField("request_id", sentUUID).Info("🔍 DEBUG: Streaming logs - keeping HTTP handler alive until client disconnect")
		<-r.Context().Done()
		logCtx.WithField("request_id", sentUUID).Info("🔍 DEBUG: HTTP client disconnected - streaming handler ends")
	} else {
		// For static logs: wait for completion with configurable timeout
		logCtx.WithField("request_id", sentUUID).Info("🔍 DEBUG: Static logs - waiting for completion")
		completed := s.logStream.WaitForCompletion(sentUUID, 2*time.Minute)
		if completed {
			logCtx.WithField("request_id", sentUUID).Info("🔍 DEBUG: Static logs completed via LogStream")
		} else {
			logCtx.WithField("request_id", sentUUID).Warn("🔍 DEBUG: Static logs timeout - handler ends")
		}
	}
}
