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
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
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
