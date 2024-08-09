package main

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/argoproj-labs/argocd-agent/cmd/cmd"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/env"
	"github.com/argoproj-labs/argocd-agent/principal"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func NewPrincipalRunCommand() *cobra.Command {
	var (
		listenHost             string
		listenPort             int
		logLevel               string
		logFormat              string
		metricsPort            int
		disableMetrics         bool
		namespace              string
		allowedNamespaces      []string
		kubeConfig             string
		kubeContext            string
		tlsCert                string
		tlsKey                 string
		jwtKey                 string
		allowTlsGenerate       bool
		allowJwtGenerate       bool
		userDB                 string
		rootCaPath             string
		requireClientCerts     bool
		clientCertSubjectMatch bool
	)
	var command = &cobra.Command{
		Short: "Run the argocd-agent principal component",
		Run: func(c *cobra.Command, args []string) {
			ctx, cancelFn := context.WithCancel(context.Background())
			defer cancelFn()

			opts := []principal.ServerOption{}
			if logLevel != "" {
				lvl, err := cmd.StringToLoglevel(logLevel)
				if err != nil {
					cmd.Fatal("invalid log level: %s. Available levels are: %s", logLevel, cmd.AvailableLogLevels())
				}
				logrus.SetLevel(lvl)
			}
			if formatter, err := cmd.LogFormatter(logFormat); err != nil {
				cmd.Fatal(err.Error())
			} else {
				logrus.SetFormatter(formatter)
			}

			kubeConfig, err := cmd.GetKubeConfig(ctx, namespace, kubeConfig, kubeContext)
			if err != nil {
				cmd.Fatal("Could not load Kubernetes config: %v", err)
			}

			opts = append(opts, principal.WithListenerAddress(listenHost))
			opts = append(opts, principal.WithListenerPort(listenPort))
			opts = append(opts, principal.WithGRPC(true))

			if !disableMetrics {
				opts = append(opts, principal.WithMetricsPort(metricsPort))
			}

			opts = append(opts, principal.WithNamespaces(allowedNamespaces...))

			if tlsCert != "" && tlsKey != "" {
				opts = append(opts, principal.WithTLSKeyPairFromPath(tlsCert, tlsKey))
			} else if (tlsCert != "" && tlsKey == "") || (tlsCert == "" && tlsKey != "") {
				cmd.Fatal("Both --tls-cert and --tls-key have to be given")
			} else if allowTlsGenerate {
				opts = append(opts, principal.WithGeneratedTLS("argocd-agent"))
			} else {
				cmd.Fatal("No TLS configuration given and auto generation not allowed.")
			}

			if rootCaPath != "" {
				opts = append(opts, principal.WithTLSRootCaFromFile(rootCaPath))
			}

			opts = append(opts, principal.WithRequireClientCerts(requireClientCerts))
			opts = append(opts, principal.WithClientCertSubjectMatch(clientCertSubjectMatch))

			if jwtKey != "" {
				opts = append(opts, principal.WithTokenSigningKeyFromFile(jwtKey))
			} else if allowJwtGenerate {
				opts = append(opts, principal.WithGeneratedTokenSigningKey())
			} else {
				cmd.Fatal("No JWT signing key given and auto generation not allowed.")
			}

			authMethods := auth.NewMethods()
			userauth := userpass.NewUserPassAuthentication(userDB)

			if userDB != "" {
				err = userauth.LoadAuthDataFromFile(userDB)
				if err != nil {
					cmd.Fatal("Could not load user database: %v", err)
				}
				err = authMethods.RegisterMethod("userpass", userauth)
				if err != nil {
					cmd.Fatal("Could not register userpass auth method")
				}
				opts = append(opts, principal.WithAuthMethods(authMethods))
			}

			// In debug or higher log level, we start a little observer routine
			// to get some insights.
			if logrus.GetLevel() >= logrus.DebugLevel {
				ticker := time.NewTicker(10 * time.Second)
				go func() {
					logrus.Info("Starting observer goroutine")
					for {
						select {
						case <-ticker.C:
							logrus.Infof("Number of go routines running: %d", runtime.NumGoroutine())
						default:
							time.Sleep(10 * time.Millisecond)
						}
					}
				}()
			}

			s, err := principal.NewServer(ctx, kubeConfig.ApplicationsClientset, namespace, opts...)
			if err != nil {
				cmd.Fatal("Could not create new server instance: %v", err)
			}
			errch := make(chan error)
			err = s.Start(ctx, errch)
			if err != nil {
				cmd.Fatal("Could not start server: %v", err)
			}
			<-ctx.Done()
		},
	}
	command.Flags().StringVar(&listenHost, "listen-host",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LISTEN_HOST", nil, ""),
		"Name of the host to listen on")
	command.Flags().IntVar(&listenPort, "listen-port",
		env.NumWithDefault("ARGOCD_PRINCIPAL_LISTEN_PORT", cmd.ValidPort, 8443),
		"Port the gRPC server will listen on")

	command.Flags().StringVar(&logLevel, "log-level",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_LEVEL", nil, logrus.InfoLevel.String()),
		"The log level to use")
	command.Flags().StringVar(&logFormat, "log-format",
		env.StringWithDefault("ARGOCD_PRINCIPAL_LOG_FORMAT", nil, "text"),
		"The log format to use (one of: text, json)")

	command.Flags().IntVar(&metricsPort, "metrics-port",
		env.NumWithDefault("ARGOCD_PRINCIPAL_METRICS_PORT", cmd.ValidPort, 8000),
		"Port the metrics server will listen on")
	command.Flags().BoolVar(&disableMetrics, "disable-metrics",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_DISABLE_METRICS", false),
		"Disable metrics collection and metrics server")

	command.Flags().StringVarP(&namespace, "namespace", "n",
		env.StringWithDefault("ARGOCD_PRINCIPAL_NAMESPACE", nil, ""),
		"The namespace the server will use for configuration. Set only when running out of cluster.")
	command.Flags().StringSliceVar(&allowedNamespaces, "allowed-namespaces",
		env.StringSliceWithDefault("ARGOCD_PRINCIPAL_ALLOWED_NAMESPACES", nil, []string{}),
		"List of namespaces the server is allowed to operate in")

	command.Flags().StringVar(&tlsCert, "tls-cert",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH", nil, ""),
		"Use TLS certificate from path")
	command.Flags().StringVar(&tlsKey, "tls-key",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH", nil, ""),
		"Use TLS private key from path")
	command.Flags().BoolVar(&allowTlsGenerate, "insecure-tls-generate",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_ALLOW_GENERATE", false),
		"INSECURE: Generate and use temporary TLS cert and key")
	command.Flags().StringVar(&rootCaPath, "root-ca-path",
		env.StringWithDefault("ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH", nil, ""),
		"Path to a file containing root CA certificate for verifying client certs")
	command.Flags().BoolVar(&requireClientCerts, "require-client-certs",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_REQUIRE", false),
		"Whether to require agents to present a client certificate")
	command.Flags().BoolVar(&clientCertSubjectMatch, "client-cert-subject-match",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_TLS_CLIENT_CERT_MATCH_SUBJECT", false),
		"Whether a client cert's subject must match the agent name")

	command.Flags().StringVar(&jwtKey, "jwt-key",
		env.StringWithDefault("ARGOCD_PRINCIPAL_JWT_KEY_PATH", nil, ""),
		"Use JWT signing key from path")
	command.Flags().BoolVar(&allowJwtGenerate, "insecure-jwt-generate",
		env.BoolWithDefault("ARGOCD_PRINCIPAL_JWT_ALLOW_GENERATE", false),
		"INSECURE: Generate and use temporary JWT signing key")

	command.Flags().StringVar(&userDB, "passwd",
		env.StringWithDefault("ARGOCD_PRINCIPAL_USER_DB_PATH", nil, ""),
		"Path to userpass passwd file")

	command.Flags().StringVar(&kubeConfig, "kubeconfig", "", "Path to a kubeconfig file to use")
	command.Flags().StringVar(&kubeContext, "kubecontext", "", "Override the default kube context")

	return command

}

func main() {
	cmd.InitLogging()
	cmd := NewPrincipalRunCommand()
	err := cmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
