// Copyright 2025 The argocd-agent Authors
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

package fixture

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

/*
The code in this file provides an HTTP client to Argo CD's REST API, without
the need for the full blown gRPC client. It does not support all Argo CD APIs,
only those currently needed in end-to-end tests.

Only to be used in tests.
*/

type ArgoRestClient struct {
	endpoint string
	username string
	password string
	token    string
	client   *http.Client
}

// NewArgoClient returns a new client for Argo CD's REST API
func NewArgoClient(endpoint, username, password string) *ArgoRestClient {
	ac := &ArgoRestClient{
		endpoint: endpoint,
		username: username,
		password: password,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	}
	return ac
}

// SetAuthToken can be used to manually set an authentication token. If an
// authentication token is set, there's no need to call Login() anymore.
func (c *ArgoRestClient) SetAuthToken(token string) {
	c.token = token
}

func post(url *url.URL, data string) *http.Request {
	return &http.Request{
		Method:        http.MethodPost,
		URL:           url,
		Body:          io.NopCloser(bytes.NewReader([]byte(data))),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		ContentLength: int64(len(data)),
	}
}

// Login creates a new Argo CD session
func (c *ArgoRestClient) Login() error {
	// Get session token from API
	authStr := fmt.Sprintf(`{"username": "%s", "password": "%s"}`, c.username, c.password)
	payload := io.NopCloser(bytes.NewReader([]byte(authStr)))
	res, err := c.client.Do(&http.Request{
		Method:        http.MethodPost,
		URL:           &url.URL{Scheme: "https", Host: c.endpoint, Path: "/api/v1/session"},
		Body:          payload,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		ContentLength: int64(len(authStr)),
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = res.Body.Close()
	}()
	if res.StatusCode != 200 {
		return fmt.Errorf("expected HTTP 200, got %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	type tokenResponse struct {
		Token string `json:"token"`
	}
	token := &tokenResponse{}
	err = json.Unmarshal(body, token)
	if err != nil {
		return err
	}
	if token.Token == "" {
		return errors.New("empty token received")
	}
	c.token = token.Token
	return nil
}

func (c *ArgoRestClient) Sync(app *v1alpha1.Application) error {
	payload := fmt.Sprintf(`{"name": "%s", "appNamespace": "%s" }`, app.Name, app.Namespace)
	reqURL := c.url()
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s/sync", app.Name)
	req := post(reqURL, payload)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("expected HTTP 200, got %d", resp.StatusCode)
	}
	return nil
}

// GetResource requests a resource managed through an Application from Argo CD
func (c *ArgoRestClient) GetResource(app *v1alpha1.Application, group, version, kind, namespace, name string) (string, error) {
	reqURL := c.url(
		"appNamespace", app.Namespace,
		"project", app.Spec.Project,
		"namespace", namespace,
		"resourceName", name,
		"group", group,
		"version", version,
		"kind", kind,
	)
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s/resource", app.Name)
	resp, err := c.Do(&http.Request{Method: http.MethodGet, URL: reqURL, Header: make(http.Header)})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("expected HTTP 200, got %d", resp.StatusCode)
	}
	type manifestResponse struct {
		Manifest string `json:"manifest"`
	}
	manifest := &manifestResponse{}
	jsonData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(jsonData, manifest)
	if err != nil {
		return "", err
	}
	return manifest.Manifest, nil
}

func (c *ArgoRestClient) RunResourceAction(app *v1alpha1.Application, action, group, version, kind, namespace, name string) error {
	reqURL := c.url(
		"appNamespace", app.Namespace,
		"project", app.Spec.Project,
		"namespace", namespace,
		"resourceName", name,
		"group", group,
		"version", version,
		"kind", kind,
	)
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s/resource/actions/v2", app.Name)

	// Based on Argo CD Swagger: https://cd.apps.argoproj.io/swagger-ui#tag/ApplicationService/operation/ApplicationService_RunResourceActionV2
	requestBody := map[string]interface{}{
		"action":       action,
		"appNamespace": app.Namespace,
		"group":        group,
		"kind":         kind,
		"name":         name,
		"namespace":    namespace,
		"project":      app.Spec.Project,
		"resourceName": name,
		"version":      version,
	}
	reqBody, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, reqURL.String(), bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return err
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("expected HTTP 200, got %d resp %v", resp.StatusCode, string(respBytes))
	}

	return nil
}

// GetLogs fetches static pod logs for a specific pod and allows specifying container and tailLines.
func (c *ArgoRestClient) GetLogs(app *v1alpha1.Application, namespace, podName, container string, tailLines int) (string, error) {
	if podName == "" {
		return "", fmt.Errorf("pod name is required")
	}
	u := c.url(
		"appNamespace", app.Namespace,
		"project", app.Spec.Project,
		"namespace", namespace,
	)
	q := u.Query()
	if container != "" {
		q.Set("container", container)
	}
	if tailLines > 0 {
		q.Set("tailLines", fmt.Sprint(tailLines))
	}
	u.RawQuery = q.Encode()
	u.Path = fmt.Sprintf("/api/v1/applications/%s/pods/%s/logs", app.Name, podName)

	resp, err := c.Do(&http.Request{Method: http.MethodGet, URL: u, Header: make(http.Header)})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("expected HTTP 200, got %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// GetApplicationLogs fetches logs via Argo CD's application logs endpoint.
func (c *ArgoRestClient) GetApplicationLogs(app *v1alpha1.Application, namespace, podName, container string, tailLines int) (string, error) {
	u := c.url(
		"appNamespace", app.Namespace,
		"project", app.Spec.Project,
		"namespace", namespace,
		"podName", podName,
	)
	q := u.Query()
	if container != "" {
		q.Set("container", container)
	}
	if tailLines > 0 {
		q.Set("tailLines", fmt.Sprint(tailLines))
	}
	u.RawQuery = q.Encode()
	u.Path = fmt.Sprintf("/api/v1/applications/%s/logs", app.Name)

	resp, err := c.Do(&http.Request{Method: http.MethodGet, URL: u, Header: make(http.Header)})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("expected HTTP 200, got %d: %s", resp.StatusCode, string(body))
	}
	return string(body), nil
}

// ListResourceActions lists available actions for the given resource and returns their names.
func (c *ArgoRestClient) ListResourceActions(app *v1alpha1.Application, group, version, kind, namespace, name string) ([]string, error) {
	reqURL := c.url(
		"appNamespace", app.Namespace,
		"project", app.Spec.Project,
		"namespace", namespace,
		"resourceName", name,
		"group", group,
		"version", version,
		"kind", kind,
	)
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s/resource/actions", app.Name)

	resp, err := c.Do(&http.Request{Method: http.MethodGet, URL: reqURL, Header: make(http.Header)})
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("expected HTTP 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Minimal struct to capture action names
	var payload struct {
		Actions []struct {
			Name string `json:"name"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(payload.Actions))
	for _, a := range payload.Actions {
		names = append(names, a.Name)
	}
	return names, nil
}

// url constructs a URL for hitting an Argo CD API endpoint.
func (c *ArgoRestClient) url(params ...string) *url.URL {
	u := &url.URL{Scheme: "https", Host: c.endpoint}
	if len(params)%2 == 0 {
		q := make(url.Values)
		for i := 0; i < len(params)-1; i += 2 {
			q.Add(params[i], params[i+1])
		}
		u.RawQuery = q.Encode()
	} else if len(params) != 0 {
		panic("params must be given in pairs")
	}
	return u
}

// Do sends a request to the Argo CD API. It uses the client's TLS config and
// adds authentication headers.
func (c *ArgoRestClient) Do(req *http.Request) (*http.Response, error) {
	if c.token == "" {
		if err := c.Login(); err != nil {
			return nil, err
		}
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))
	return c.client.Do(req)
}

func GetInitialAdminSecret(k8sClient KubeClient) (string, error) {
	// Read admin secret from principal's cluster
	pwdSecret := &corev1.Secret{}
	err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-initial-admin-secret"}, pwdSecret, metav1.GetOptions{})

	if err != nil {
		return "", fmt.Errorf("unable to get admin secret: %v", err)
	}

	return string(pwdSecret.Data["password"]), nil
}

func GetArgoCDServerEndpoint(k8sClient KubeClient) (string, error) {

	// Get the Argo server endpoint to use
	srvService := &corev1.Service{}
	err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: "argocd", Name: "argocd-server"}, srvService, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	argoEndpoint := srvService.Spec.LoadBalancerIP

	if len(srvService.Status.LoadBalancer.Ingress) > 0 {
		ingress := srvService.Status.LoadBalancer.Ingress[0]
		// Prefer hostname if available, otherwise use IP
		if ingress.Hostname != "" {
			argoEndpoint = ingress.Hostname
		} else if ingress.IP != "" {
			argoEndpoint = ingress.IP
		}
	}

	return argoEndpoint, nil
}

// CreateApplication creates a new application in ArgoCD
func (c *ArgoRestClient) CreateApplication(app *v1alpha1.Application) (*v1alpha1.Application, error) {
	reqURL := c.url()
	reqURL.Path = "/api/v1/applications"
	appBytes, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal application: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, reqURL.String(), bytes.NewBuffer(appBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create application: status %d, body: %s", resp.StatusCode, string(body))
	}

	var createdApp v1alpha1.Application
	if err := json.NewDecoder(resp.Body).Decode(&createdApp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal created application: %w", err)
	}

	return &createdApp, nil
}

// GetApplication retrieves an application by its name
func (c *ArgoRestClient) GetApplication(name string) (*v1alpha1.Application, error) {
	reqURL := c.url()
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s", name)

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get application: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("application '%s' not found", name)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get application: status %d, body: %s", resp.StatusCode, string(body))
	}

	var app v1alpha1.Application
	if err := json.NewDecoder(resp.Body).Decode(&app); err != nil {
		return nil, fmt.Errorf("failed to unmarshal application: %w", err)
	}

	return &app, nil
}

// ListApplications lists all applications
func (c *ArgoRestClient) ListApplications() (*v1alpha1.ApplicationList, error) {
	reqURL := c.url()
	reqURL.Path = "/api/v1/applications"

	req, err := http.NewRequest(http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list applications: status %d, body: %s", resp.StatusCode, string(body))
	}

	var appList v1alpha1.ApplicationList
	if err := json.NewDecoder(resp.Body).Decode(&appList); err != nil {
		return nil, fmt.Errorf("failed to unmarshal application list: %w", err)
	}

	return &appList, nil
}

// DeleteApplication deletes an application by its name
func (c *ArgoRestClient) DeleteApplication(name string) error {
	reqURL := c.url()
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s", name)
	reqURL.RawQuery = "cascade=false"

	req, err := http.NewRequest(http.MethodDelete, reqURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json") // content-type

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete application: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete application: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *ArgoRestClient) TerminateOperation(name, namespace string) error {
	reqURL := c.url(
		"appNamespace", namespace,
		"name", name,
	)
	reqURL.Path = fmt.Sprintf("/api/v1/applications/%s/operation", name)

	req, err := http.NewRequest(http.MethodDelete, reqURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("failed to terminate operation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to terminate operation: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// TerminalClient represents a test client for terminal WebSocket connections.
type TerminalClient struct {
	wsConn   *websocket.Conn
	mu       sync.Mutex
	closed   bool
	output   strings.Builder
	outputMu sync.Mutex
}

// ExecTerminal opens a terminal session to a pod via WebSocket.
// This replicates the behavior of the ArgoCD UI when a user opens a terminal session to an application.
// ArgoCD decides which shell to use based on the configured allowed shells.
func (c *ArgoRestClient) ExecTerminal(app *v1alpha1.Application, namespace, podName, container string) (*TerminalClient, error) {
	if err := c.ensureToken(); err != nil {
		return nil, err
	}

	// Build the exec URL
	u := &url.URL{
		Scheme: "wss",
		Host:   c.endpoint,
		Path:   "/terminal",
	}

	q := u.Query()
	q.Set("pod", podName)
	q.Set("container", container)
	q.Set("appName", app.Name)
	q.Set("appNamespace", app.Namespace)
	q.Set("projectName", app.Spec.Project)
	q.Set("namespace", namespace)
	u.RawQuery = q.Encode()

	// Create WebSocket dialer with TLS config
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}

	// Set token as cookie - ArgoCD expects auth token in argocd.token cookie
	headers := http.Header{}
	headers.Set("Cookie", fmt.Sprintf("argocd.token=%s", c.token))

	// Connect to WebSocket
	wsConn, resp, err := dialer.Dial(u.String(), headers)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("failed to connect to terminal WebSocket: %w (status: %d, body: %s)", err, resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("failed to connect to terminal WebSocket: %w", err)
	}

	session := &TerminalClient{
		wsConn: wsConn,
	}

	// Start reading output in background
	go session.readOutput()

	return session, nil
}

// ensureToken makes sure we have a valid authentication token
func (c *ArgoRestClient) ensureToken() error {
	if c.token == "" {
		return c.Login()
	}
	return nil
}

// terminalMessage is the JSON message format used by ArgoCD terminal WebSocket
type terminalMessage struct {
	Operation string `json:"operation"`
	Data      string `json:"data"`
	Rows      uint16 `json:"rows"`
	Cols      uint16 `json:"cols"`
}

// readOutput continuously reads output from the WebSocket connection
func (s *TerminalClient) readOutput() {
	for {
		_, message, err := s.wsConn.ReadMessage()
		if err != nil {
			// Connection closed or error
			return
		}

		if len(message) < 1 {
			continue
		}

		// Parse JSON message
		var msg terminalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			continue
		}

		switch msg.Operation {
		case "stdout":
			s.outputMu.Lock()
			s.output.WriteString(msg.Data)
			s.outputMu.Unlock()
		}
	}
}

// SendInput sends input to the terminal session
func (s *TerminalClient) SendInput(input string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("session is closed")
	}

	// ArgoCD terminal uses JSON messages (includes rows/cols like the UI)
	msg, err := json.Marshal(terminalMessage{
		Operation: "stdin",
		Data:      input,
		Rows:      24,
		Cols:      80,
	})
	if err != nil {
		return err
	}
	return s.wsConn.WriteMessage(websocket.TextMessage, msg)
}

// SendResize sends a terminal resize message
func (s *TerminalClient) SendResize(cols, rows uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("session is closed")
	}

	// ArgoCD terminal uses JSON messages
	msg, err := json.Marshal(terminalMessage{
		Operation: "resize",
		Cols:      cols,
		Rows:      rows,
	})
	if err != nil {
		return err
	}
	return s.wsConn.WriteMessage(websocket.TextMessage, msg)
}

// GetOutput returns all captured output so far
func (s *TerminalClient) GetOutput() string {
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	return s.output.String()
}

// WaitForOutput waits until the output contains the expected string or timeout
func (s *TerminalClient) WaitForOutput(expected string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.GetOutput(), expected) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// Close closes the terminal session
func (s *TerminalClient) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	return s.wsConn.Close()
}
