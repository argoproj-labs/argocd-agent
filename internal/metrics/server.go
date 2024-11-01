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

package metrics

import (
	"fmt"
	"net/http"
	neturl "net/url"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

type MetricsServerOptions struct {
	host string
	port int
	path string
}

type MetricsServerOption func(*MetricsServerOptions)

func listener(o *MetricsServerOptions) string {
	l := ""
	if o.host != "" {
		l += o.host
	} else {
		l += "0.0.0.0"
	}
	l += fmt.Sprintf(":%d", o.port)
	return l
}

func url(o *MetricsServerOptions) string {
	u := neturl.URL{}
	u.Scheme = "http"
	h := ""
	if o.host != "" {
		h = o.host
	} else {
		h = "0.0.0.0"
	}
	u.Host = fmt.Sprintf("%s:%d", h, o.port)
	u.Path = "/metrics"
	return u.String()
}

func WithListener(hostname string, port int) MetricsServerOption {
	return func(o *MetricsServerOptions) {
		if hostname != "" {
			o.host = hostname
		}
		o.port = port
	}
}

// StartMetricsServer starts the metrics server in a separate go routine and
// returns an error channel.
func StartMetricsServer(opts ...MetricsServerOption) chan error {
	config := &MetricsServerOptions{
		host: "",
		port: 8080,
		path: "/metrics",
	}
	for _, o := range opts {
		o(config)
	}
	errCh := make(chan error)
	logrus.Infof("Starting metrics server on %s", url(config))
	go func() {
		sm := http.NewServeMux()
		sm.Handle(config.path, promhttp.Handler())
		errCh <- http.ListenAndServe(listener(config), sm)
	}()
	return errCh
}
