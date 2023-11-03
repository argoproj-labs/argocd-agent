package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type MetricsServerOptions struct {
	host string
	port int
}

type MetricsServerOption func(*MetricsServerOptions)

func listener(o *MetricsServerOptions) string {
	l := ""
	if o.host != "" {
		l += o.host
	}
	l += fmt.Sprintf(":%d", o.port)
	return l
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
	}
	for _, o := range opts {
		o(config)
	}
	errCh := make(chan error)
	go func() {
		sm := http.NewServeMux()
		sm.Handle("/metrics", promhttp.Handler())
		errCh <- http.ListenAndServe(listener(config), sm)
	}()
	return errCh
}
