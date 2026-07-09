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

// clusterprofile-agent-driver is an OPTIONAL, standalone controller that
// bridges the ClusterProfile API (sigs.k8s.io/cluster-inventory-api) and
// argocd-agent, so that agents can be registered with Argo CD declaratively
// instead of via `argocd-agentctl agent create`.
//
// It watches ClusterProfile objects whose spec.clusterManager.name is
// "argocd-agent" and, for each one, mints a non-expiring resource-proxy JWT
// (using the same signing key as the argocd-agent principal), stores it in
// a companion Secret next to the ClusterProfile, and publishes a
// status.accessProviders entry that clusterprofile-integration-for-argocd
// converts into an Argo CD cluster Secret using the argocd-agent-creds exec
// plugin (see cmd/argocd-agent-creds).
//
// See docs/clusterprofile-integration/README.md for the full design.
package main

import (
	"flag"
	"os"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	clusterv1alpha1 "sigs.k8s.io/cluster-inventory-api/apis/v1alpha1"

	"github.com/argoproj-labs/argocd-agent/internal/clusterprofiledriver"
	"github.com/argoproj-labs/argocd-agent/internal/issuer"

	"github.com/sirupsen/logrus"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		resourceProxyAddress string
		jwtKeyPath           string
		caCertPath           string
		leaderElectionNS     string
		enableLeaderElection bool
		watchNamespace       string
	)

	flag.StringVar(&metricsAddr, "metrics-addr", envOr("DRIVER_METRICS_ADDR", ":8080"), "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "probe-addr", envOr("DRIVER_PROBE_ADDR", ":8081"), "The address the health probe endpoint binds to.")
	flag.StringVar(&resourceProxyAddress, "resource-proxy-address", envOr("DRIVER_RESOURCE_PROXY_ADDRESS", "argocd-agent-resource-proxy.argocd.svc.cluster.local:9090"), "host:port at which Argo CD can reach the argocd-agent resource proxy.")
	flag.StringVar(&jwtKeyPath, "jwt-key-path", envOr("DRIVER_JWT_KEY_PATH", "/app/config/jwt/jwt.key"), "Path to the principal's PEM/PKCS8 JWT signing key (mounted from secret argocd-agent-jwt).")
	flag.StringVar(&caCertPath, "resource-proxy-ca-path", envOr("DRIVER_RESOURCE_PROXY_CA_PATH", "/app/config/ca/ca.crt"), "Path to the resource proxy CA certificate (mounted from secret argocd-agent-ca).")
	flag.StringVar(&leaderElectionNS, "leader-election-namespace", envOr("DRIVER_LEADER_ELECTION_NAMESPACE", ""), "Namespace to use for the leader election lease. Defaults to the pod's own namespace.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", envOr("DRIVER_ENABLE_LEADER_ELECTION", "false") == "true", "Enable leader election for the controller manager.")
	flag.StringVar(&watchNamespace, "watch-namespace", envOr("DRIVER_WATCH_NAMESPACE", ""), "Namespace to watch for ClusterProfile/Secret objects. Empty means cluster-wide (requires a ClusterRole). By design this driver is meant to be deployed once per namespace it manages (typically the Argo CD control-plane namespace), matching clusterprofile-integration-for-argocd's own namespace-scoped RBAC model, so this should normally be set to that single namespace.")
	flag.Parse()

	logf.SetLogger(zap.New(zap.UseDevMode(true)))
	log := ctrl.Log.WithName("clusterprofile-agent-driver")

	log.Info("Starting clusterprofile-agent-driver")

	// The issuer name "argocd-agent-server" MUST match the name the
	// argocd-agent principal uses (see principal/server.go) so that tokens
	// minted here carry the Issuer/Audience claims the principal expects
	// when validating resource-proxy bearer tokens.
	jwtIssuer, err := issuer.NewIssuer("argocd-agent-server", issuer.WithRSAPrivateKeyFromFile(jwtKeyPath))
	if err != nil {
		log.Error(err, "unable to construct JWT issuer from signing key", "path", jwtKeyPath)
		os.Exit(1)
	}

	caData, err := os.ReadFile(caCertPath)
	if err != nil {
		log.Error(err, "unable to read resource proxy CA certificate")
		os.Exit(1)
	}

	scheme := clientgoscheme.Scheme
	if err := clusterv1alpha1.AddToScheme(scheme); err != nil {
		log.Error(err, "unable to add ClusterProfile scheme")
		os.Exit(1)
	}

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress:  probeAddr,
		LeaderElection:          enableLeaderElection,
		LeaderElectionID:        "clusterprofile-agent-driver.argocd-agent.argoproj-labs.io",
		LeaderElectionNamespace: leaderElectionNS,
	}
	if watchNamespace != "" {
		log.Info("Restricting watch cache to a single namespace", "namespace", watchNamespace)
		mgrOpts.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
			},
		}
	} else {
		log.Info("No --watch-namespace set; watching ClusterProfile/Secret objects cluster-wide (requires a ClusterRole)")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOpts)
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&clusterprofiledriver.Reconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		Log:                  ctrl.Log.WithName("controllers").WithName("ClusterProfile"),
		Issuer:               jwtIssuer,
		ResourceProxyAddress: resourceProxyAddress,
		ResourceProxyCAData:  caData,
	}).SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("Starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func init() {
	// Route the standard logrus-based logger used elsewhere in argocd-agent
	// to a sane default too, in case any imported package logs through it.
	logrus.SetLevel(logrus.InfoLevel)
}
