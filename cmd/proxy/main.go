package main

import (
	"context"
	"net/http"
	"os"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/resourceproxy"
	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetLevel(logrus.TraceLevel)
	if len(os.Args) != 2 {
		cmdutil.Fatal("Incorrect number of arguments")
	}
	kubeCtx := os.Args[1]

	rest, err := kube.NewRestConfig("", kubeCtx)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating REST config")
	}
	proxy, err := resourceproxy.New("127.0.0.1:9090",
		resourceproxy.WithRestConfig(rest),
		resourceproxy.WithLogMatcher(func(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
			logrus.Infof("Logs!")
			logrus.Infof("%v", params)
			w.WriteHeader(http.StatusNotFound)
		}),
		resourceproxy.WithExecMatcher(func(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
			logrus.Infof("Exec")
			logrus.Infof("%v", params)
			w.WriteHeader(http.StatusNotFound)
		}),
		resourceproxy.WithPodMatcher(func(w http.ResponseWriter, r *http.Request, params resourceproxy.Params) {
			logrus.Infof("Pods")
			logrus.Infof("%v", params)
			w.WriteHeader(http.StatusOK)
		}),
	)
	if err != nil {
		cmdutil.FatalWithExitCode(1, "Error starting proxy: %s", err)
	}
	errCh, err := proxy.Start(context.Background())
	if err != nil {
		cmdutil.FatalWithExitCode(1, "Error starting proxy: %s", err)
	}
	<-errCh
}
