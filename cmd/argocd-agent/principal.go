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

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmdutil"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/mtls"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/env"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/internal/labels"
	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/principal"
	cacheutil "github.com/argoproj/argo-cd/v2/util/cache"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// NewPrincipalRunCommand returns a new principal run command.
func NewPrincipalRunCommand() *cobra.Command {
	var (
		listenHost                string
		listenPort                int
		logLevel                  string
		logFormat                 string
		metricsPort               int
		namespace                 string
		allowedNamespaces         []string
		kubeConfig                string
		kubeContext               string
		tlsSecretName             string
		tlsCert                   string
		tlsKey                    string
		jwtSecretName             string
		jwtKey                    string
		allowTLSGenerate          bool
		allowJwtGenerate          bool
		authMethod                string
		rootCaSecretName          string
		rootCaPath                string
		requireClientCerts        bool
		clientCertSubjectMatch    bool
		autoNamespaceAllow        bool
		autoNamespacePattern      string
		autoNamespaceLabels       []string
		enableWebSocket           bool
		enableResourceProxy       bool
		pprofPort                 int
		resourceProxySecretName   string
		resourceProxyCertPath     string
		resourceProxyKeyPath      string
		resourceProxyCaSecretName string
		resourceProxyCAPath       string
		showVersion               bool
		versionFormat             string

		// Minimum time duration for agent to wait before sending next keepalive ping to principal
		// if agent sends ping more often than specified interval then connection will be dropped
		// Ex: "30m", "1h" or "1h20m10s". Valid time units are "s", "m", "h".
		keepAliveMinimumInterval time.Duration

		redisAddress         string
		redisCompressionType string
	)
	var command = &cobra.Command{
		Use:   "principal",
		Short: "Run the argocd-agent principal component",
		Run: func(c *cobra.Command, args []string) {
			if showVersion {
				cmdutil.PrintVersion(version.New("argocd-agent"), versionFormat)
				os.Exit(0)
			}

			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()

			if pprofPort > 0 {
				logrus.Infof("Starting pprof server on 127.0.0.1:%d", pprofPort)

				go func() {
					err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", pprofPort), nil)
					if err != nil {
						cmdutil.Fatal("Error starting pprof server: %v", err)
					}
				}()
			}

			opts := []principal.ServerOption{}
			if logLevel != "" {
				lvl, err := cmdutil.StringToLoglevel(logLevel)
				if err != nil {
					cmdutil.Fatal("invalid log level: %s. Available levels are: %s", logLevel, cmdutil.AvailableLogLevels())
				}
				logrus.Printf("Setting loglevel to %s", logLevel)
				logrus.SetLevel(lvl)
			}
			if formatter, err := cmdutil.LogFormatter(logFormat); err != nil {
				cmdutil.Fatal("%s", err.Error())
			} else {
				logrus.SetFormatter(formatter)
			}

			kubeConfig, err := cmdutil.GetKubeConfig(ctx, namespace, kubeConfig, kubeContext)
			if err != nil {
				cmdutil.Fatal("Could not load Kubernetes config: %v", err)
			}

			opts = append(opts, principal.WithListenerAddress(listenHost))
			opts = append(opts, principal.WithListenerPort(listenPort))
			opts = append(opts, principal.WithGRPC(true))
			nsLabels := make(map[string]string)
			if len(autoNamespaceLabels) > 0 &&
				(len(autoNamespaceLabels) != 1 || autoNamespaceLabels[0] != "") {
				nsLabels, err = labels.StringsToMap(autoNamespaceLabels)
				if err != nil {
					cmdutil.Fatal("Could not parse auto namespace labels: %v", err)
				}
			}
			opts = append(opts, principal.WithAutoNamespaceCreate(autoNamespaceAllow, autoNamespacePattern, nsLabels))

			if metricsPort > 0 {
				opts = append(opts, principal.WithMetricsPort(metricsPort))
			}

			opts = append(opts, principal.WithNamespaces(allowedNamespaces...))

			if allowTLSGenerate {
				logrus.Info("Using one-time generated TLS certificate for gRPC")
				opts = append(opts, principal.WithGeneratedTLS("argocd-agent-principal--generated"))
			} else if tlsCert != "" && tlsKey != "" {
				logrus.Infof("Loading gRPC TLS configuration from files cert=%s and key=%s", tlsCert, tlsKey)
				opts = append(opts, principal.WithTLSKeyPairFromPath(tlsCert, tlsKey))
			} else if (tlsCert != "" && tlsKey == "") || (tlsCert == "" && tlsKey != "") {
				cmdutil.Fatal("Both --tls-cert and --tls-key have to be given")
			} else {
				logrus.Infof("Loading gRPC TLS certificate from secret %s/%s", namespace, tlsSecretName)
				opts = append(opts, principal.WithTLSKeyPairFromSecret(kubeConfig.Clientset, namespace, tlsSecretName))
			}

			if rootCaPath != "" {
				logrus.Infof("Loading root CA certificate from file %s", rootCaPath)
				opts = append(opts, principal.WithTLSRootCaFromFile(rootCaPath))
			} else {
				logrus.Infof("Loading root CA certificate from secret %s/%s", namespace, rootCaSecretName)
				opts = append(opts, principal.WithTLSRootCaFromSecret(kubeConfig.Clientset, namespace, rootCaSecretName, "tls.crt"))
			}

			opts = append(opts, principal.WithRequireClientCerts(requireClientCerts))
			opts = append(opts, principal.WithClientCertSubjectMatch(clientCertSubjectMatch))
			opts = append(opts, principal.WithResourceProxyEnabled(enableResourceProxy))

			if enableResourceProxy {
				var proxyTLS *tls.Config
				if resourceProxyCertPath != "" && resourceProxyKeyPath != "" && resourceProxyCAPath != "" {
					logrus.Infof("Loading resource proxy TLS configuration from files cert=%s, key=%s and ca=%s", resourceProxyCertPath, resourceProxyKeyPath, resourceProxyCAPath)
					proxyTLS, err = getResourceProxyTLSConfigFromFiles(resourceProxyCertPath, resourceProxyKeyPath, resourceProxyCAPath)
				} else {
					logrus.Infof("Loading resource proxy TLS certificate from secrets %s/%s and %s/%s", namespace, resourceProxySecretName, namespace, resourceProxyCaSecretName)
					proxyTLS, err = getResourceProxyTLSConfigFromKube(kubeConfig, namespace, resourceProxySecretName, resourceProxyCaSecretName)
				}
				if err != nil {
					cmdutil.Fatal("Error reading TLS config for resource proxy: %v", err)
				}
				opts = append(opts, principal.WithResourceProxyTLS(proxyTLS))
			}

			if jwtKey != "" {
				logrus.Infof("Loading JWT signing key from file %s", jwtKey)
				opts = append(opts, principal.WithTokenSigningKeyFromFile(jwtKey))
			} else if allowJwtGenerate {
				logrus.Info("Using one-time generated JWT signing key")
				opts = append(opts, principal.WithGeneratedTokenSigningKey())
			} else {
				logrus.Infof("Loading JWT signing key from secret %s/%s", namespace, jwtSecretName)
				opts = append(opts, principal.WithTokenSigningKeyFromSecret(kubeConfig.Clientset, namespace, jwtSecretName))
			}

			authMethods := auth.NewMethods()
			authMethod, authConfig, err := parseAuth(authMethod)
			if err != nil {
				cmdutil.Fatal("Could not parse auth: %v", err)
			}
			switch authMethod {
			case "mtls":
				var regex *regexp.Regexp
				if authConfig != "" {
					regex, err = regexp.Compile(authConfig)
					if err != nil {
						cmdutil.Fatal("Error compiling mtls agent id regex: %v", err)
					}
				}
				mtlsauth := mtls.NewMTLSAuthentication(regex)
				err := authMethods.RegisterMethod("mtls", mtlsauth)
				if err != nil {
					cmdutil.Fatal("Could not register mtls auth method: %v", err)
				}
				// The mtls authentication method requires the use of client
				// certificates, so make them mandatory.
				if !requireClientCerts {
					opts = append(opts, principal.WithRequireClientCerts(true))
				}
			case "userpass":
				userauth := userpass.NewUserPassAuthentication(authConfig)
				err = userauth.LoadAuthDataFromFile(authConfig)
				if err != nil {
					cmdutil.Fatal("Could not load user database: %v", err)
				}
				err = authMethods.RegisterMethod("userpass", userauth)
				if err != nil {
					cmdutil.Fatal("Could not register userpass auth method: %v", err)
				}
			default:
				cmdutil.Fatal("Unknown auth method: %s", authMethod)
			}
			opts = append(opts, principal.WithAuthMethods(authMethods))

			// In debug or higher log level, we start a little observer routine
			// to get some insights.
			if logrus.GetLevel() >= logrus.DebugLevel {
				logrus.Info("Starting observer goroutine")
				observer(10 * time.Second)
			}

			opts = append(opts, principal.WithWebSocket(enableWebSocket))
			opts = append(opts, principal.WithKeepAliveMinimumInterval(keepAliveMinimumInterval))
			opts = append(opts, principal.WithRedis(redisAddress, redisCompressionType))

			s, err := principal.NewServer(ctx, kubeConfig, namespace, opts...)
			if err != nil {
				cmdutil.Fatal("Could not create new server instance: %v", err)
			}
			errch := make(chan error)
			err = s.Start(ctx, errch)
			if err != nil {
				cmdutil.Fatal("Could not start server: %v", err)
			}
			<-ctx.Done()
		},
	}
	command.Flags().StringVar(&listenHost, "listen-host",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LISTEN_HOST", nil, ""),
		"Name of the host to listen on")
	command.Flags().IntVar(&listenPort, "listen-port",
		env.NumWithDefault("ARGOCD_PRINCIPAL_LISTEN_PORT", cmdutil.ValidPort, 8443),
		"Port the gRPC server will listen on")

	command.Flags().StringVar(&logLevel, "log-level",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_LEVEL", nil, logrus.InfoLevel.String()),
		"The log level to use")
	command.Flags().StringVar(&logFormat, "log-format",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_FORMAT", nil, "text"),
		"The log format to use (one of: text, json)")

	command.Flags().IntVar(&metricsPort, "metrics-port",
		env.NumWithDefault("ARGOCD_PRINCIPAL_METRICS_PORT", cmdutil.ValidPort, 8000),
		"Port the metrics server will listen on")

	command.Flags().StringVarP(&namespace, "namespace", "n",
		env.StringWithDefault("ARGOCD_PRINCIPAL_NAMESPACE", nil, ""),
		"The namespace the server will use for configuration. Set only when running out of cluster.")
	command.Flags().StringSliceVar(&allowedNamespaces, "allowed-namespaces",
		env.StringSliceWithDefault("ARGOCD_PRINCIPAL_ALLOWED_NAMESPACES", nil, []string{}),
		"List of namespaces the server is allowed to operate in")
	command.Flags().BoolVar(&autoNamespaceAllow, "namespace-create-enable",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_NAMESPACE_CREATE_ENABLE", false),
		"Whether to allow automatic namespace creation for autonomous agents")
	command.Flags().StringVar(&autoNamespacePattern, "namespace-create-pattern",
		env.StringWithDefault("ARGOCD_PRINCIPAL_NAMESPACE_CREATE_PATTERN", nil, ""),
		"Only automatically create namespaces matching pattern")
	command.Flags().StringSliceVar(&autoNamespaceLabels, "namespace-create-labels",
		env.StringSliceWithDefault("ARGOCD_PRINCIPAL_NAMESPACE_CREATE_LABELS", nil, []string{}),
		"Labels to apply to auto-created namespaces")

	command.Flags().StringVar(&tlsSecretName, "tls-secret-name",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SECRET_NAME", nil, config.SecretNamePrincipalTLS),
		"Secret name of TLS certificate and key")
	command.Flags().StringVar(&tlsCert, "tls-cert",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH", nil, ""),
		"Use TLS certificate from path")
	command.Flags().StringVar(&tlsKey, "tls-key",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH", nil, ""),
		"Use TLS private key from path")
	command.Flags().BoolVar(&allowTLSGenerate, "insecure-tls-generate",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_ALLOW_GENERATE", false),
		"INSECURE: Generate and use temporary TLS cert and key")
	command.Flags().StringVar(&rootCaSecretName, "tls-ca-secret-name",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_SECRET_NAME", nil, config.SecretNamePrincipalCA),
		"Secret name of TLS CA certificate")
	command.Flags().StringVar(&rootCaPath, "root-ca-path",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH", nil, ""),
		"Path to a file containing the root CA certificate for verifying client certs of agents")
	command.Flags().BoolVar(&requireClientCerts, "require-client-certs",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_REQUIRE", false),
		"Whether to require agents to present a client certificate")
	command.Flags().BoolVar(&clientCertSubjectMatch, "client-cert-subject-match",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_MATCH_SUBJECT", false),
		"Whether a client cert's subject must match the agent name")

	command.Flags().StringVar(&resourceProxySecretName, "resource-proxy-secret-name",
		env.StringWithDefault("ARGOCD_PRINCIPAL_RESOURCE_PROXY_SECRET_NAME", nil, config.SecretNameProxyTLS),
		"Secret name of the resource proxy")
	command.Flags().StringVar(&resourceProxyCertPath, "resource-proxy-cert-path",
		env.StringWithDefault("ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CERT_PATH", nil, ""),
		"Path to a file containing the resource proxy's TLS certificate")
	command.Flags().StringVar(&resourceProxyKeyPath, "resource-proxy-key-path",
		env.StringWithDefault("ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_KEY_PATH", nil, ""),
		"Path to a file containing the resource proxy's TLS private key")
	command.Flags().StringVar(&resourceProxyCaSecretName, "resource-proxy-ca-secret-name",
		env.StringWithDefault("ARGOCD_PRINCIPAL_RESOURCE_PROXY_CA_SECRET_NAME", nil, config.SecretNamePrincipalCA),
		"Secret name of the resource proxy's CA certificate")
	command.Flags().StringVar(&resourceProxyCAPath, "resource-proxy-ca-path",
		env.StringWithDefault("ARGOCD_PRINCIPAL_RESOURCE_PROXY_TLS_CA_PATH", nil, ""),
		"Path to a file containing the resource proxy's TLS CA data")

	command.Flags().StringVar(&jwtSecretName, "jwt-secret-name",
		env.StringWithDefault("ARGOCD_PRINCIPAL_JWT_SECRET_NAME", nil, config.SecretNameJWT),
		"Secret name of the JWT signing key")
	command.Flags().StringVar(&jwtKey, "jwt-key",
		env.StringWithDefault("ARGOCD_PRINCIPAL_JWT_KEY_PATH", nil, ""),
		"Use JWT signing key from path")
	command.Flags().BoolVar(&allowJwtGenerate, "insecure-jwt-generate",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_JWT_ALLOW_GENERATE", false),
		"INSECURE: Generate and use temporary JWT signing key")

	command.Flags().StringVar(&authMethod, "auth",
		env.StringWithDefault("ARGOCD_PRINCIPAL_AUTH", nil, ""),
		"Authentication method and the corresponding configuration")

	command.Flags().BoolVar(&enableWebSocket, "enable-websocket",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_ENABLE_WEBSOCKET", false),
		"Principal will rely on gRPC over WebSocket to stream events to the Agent")

	command.Flags().BoolVar(&enableResourceProxy, "enable-resource-proxy",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_ENABLE_RESOURCE_PROXY", true),
		"Whether to enable the resource proxy")
	command.Flags().DurationVar(&keepAliveMinimumInterval, "keepalive-min-interval",
		env.DurationWithDefault("ARGOCD_PRINCIPAL_KEEP_ALIVE_MIN_INTERVAL", nil, 0),
		"Drop agent connections that send keepalive pings more often than the specified interval") // It should be less than "keep-alive-ping-interval" of agent

	command.Flags().StringVar(&redisAddress, "redis-server-address",
		env.StringWithDefault("ARGOCD_PRINCIPAL_REDIS_SERVER_ADDRESS", nil, "argocd-redis:6379"),
		"Redis server hostname and port (e.g. argocd-redis:6379).")

	command.Flags().StringVar(&redisCompressionType, "redis-compression-type",
		env.StringWithDefault("ARGOCD_PRINCIPAL_REDIS_COMPRESSION_TYPE", nil, string(cacheutil.RedisCompressionGZip)),
		"Compression algorithm required by Redis. (possible values: gzip, none. Default value: gzip)")
	command.Flags().IntVar(&pprofPort, "pprof-port",
		env.NumWithDefault("ARGOCD_PRINCIPAL_PPROF_PORT", cmdutil.ValidPort, 0),
		"Port the pprof server will listen on")

	command.Flags().StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig file to use")
	command.Flags().StringVar(&kubeContext, "kubecontext", "", "Override the default kube context")
	command.Flags().BoolVar(&showVersion, "version", false, "Display version information and exit")
	command.Flags().StringVar(&versionFormat, "version-format", "text", "Output version information in format: text, json, json-indent")

	return command

}

// observer puts some valuable debug information in the logs every interval.
// It will launch a its own go routine and run in it indefinitely.
func observer(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		for {
			select {
			case <-ticker.C:
				logrus.Debugf("Number of go routines running: %d", runtime.NumGoroutine())
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
}

// getResourceProxyTLSConfigFromKube reads the kubeproxy TLS configuration from the
// cluster and returns it.
//
// The secret names where the certificates are stored in are hard-coded at the
// moment.
func getResourceProxyTLSConfigFromKube(kubeClient *kube.KubernetesClient, namespace, certName, caName string) (*tls.Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	proxyCert, err := tlsutil.TLSCertFromSecret(ctx, kubeClient.Clientset, namespace, certName)
	if err != nil {
		return nil, fmt.Errorf("error getting proxy certificate: %w", err)
	}

	clientCA, err := tlsutil.X509CertPoolFromSecret(ctx, kubeClient.Clientset, namespace, caName, "tls.crt")
	if err != nil {
		return nil, fmt.Errorf("error getting client CA certificate: %w", err)
	}

	proxyTLS := &tls.Config{
		Certificates: []tls.Certificate{
			proxyCert,
		},
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCA,
	}

	return proxyTLS, nil
}

// getResourceProxyTLSConfigFromFiles reads the kubeproxy TLS configuration from
// given files and returns it.
func getResourceProxyTLSConfigFromFiles(certPath, keyPath, caPath string) (*tls.Config, error) {
	proxyCert, err := tlsutil.TLSCertFromFile(certPath, keyPath, false)
	if err != nil {
		return nil, err
	}

	clientCA, err := tlsutil.X509CertPoolFromFile(caPath)
	if err != nil {
		return nil, err
	}

	proxyTLS := &tls.Config{
		Certificates: []tls.Certificate{
			proxyCert,
		},
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  clientCA,
	}

	return proxyTLS, nil
}

// parseAuth parses an authentication string and extracts the authentication method
// and its associated configuration.
func parseAuth(authStr string) (string, string, error) {
	p := strings.SplitN(authStr, ":", 2)
	if len(p) < 2 {
		return "", "", fmt.Errorf("invalid auth string")
	}
	switch p[0] {
	case "userpass":
		return "userpass", p[1], nil
	case "mtls":
		return "mtls", p[1], nil
	default:
		return "", "", fmt.Errorf("unknown auth method: %s", p[0])
	}
}
