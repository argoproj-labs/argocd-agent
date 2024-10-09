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

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/big"
	"path"
	"sync/atomic"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/agent"
	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/kube"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/argoproj-labs/argocd-agent/pkg/client"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/argoproj-labs/argocd-agent/test/proxy"
	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	fakekube "github.com/argoproj-labs/argocd-agent/test/fake/kube"
)

var certTempl = x509.Certificate{
	SerialNumber:          big.NewInt(1),
	KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
	ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	BasicConstraintsValid: true,
	NotBefore:             time.Now().Add(-1 * time.Hour),
	NotAfter:              time.Now().Add(1 * time.Hour),
}

var testNamespace = "default"

func newConn(t *testing.T, kubeClt *kube.KubernetesClient) (*grpc.ClientConn, *principal.Server) {
	t.Helper()
	tempDir := t.TempDir()
	templ := certTempl
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)
	errch := make(chan error)

	s, err := principal.NewServer(context.TODO(), kubeClt, testNamespace,
		principal.WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
		principal.WithListenerPort(0),
		principal.WithListenerAddress("127.0.0.1"),
		principal.WithShutDownGracePeriod(2*time.Second),
		principal.WithGRPC(true),
		principal.WithEventProcessors(10),
		principal.WithGeneratedTokenSigningKey(),
	)
	require.NoError(t, err)
	err = s.Start(context.Background(), errch)
	assert.NoError(t, err)

	am := userpass.NewUserPassAuthentication("")
	am.UpsertUser("default", "password")
	err = s.AuthMethodsForE2EOnly().RegisterMethod("userpass", am)
	require.NoError(t, err)

	tlsC := &tls.Config{InsecureSkipVerify: true}
	creds := credentials.NewTLS(tlsC)
	conn, err := grpc.Dial(s.ListenerForE2EOnly().Address(),
		grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	return conn, s
}

func Test_EndToEnd_Subscribe(t *testing.T) {
	// token, err := s.TokenIssuer().Issue("default", 1*time.Minute)
	// require.NoError(t, err)

	appC := fakeappclient.NewSimpleClientset()
	conn, s := newConn(t, fakekube.NewKubernetesFakeClient())
	defer conn.Close()

	authC := authapi.NewAuthenticationClient(conn)
	eventC := eventstreamapi.NewEventStreamClient(conn)

	clientCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Get authentication token and store in context
	authr, err := authC.Authenticate(clientCtx, &authapi.AuthRequest{Method: "userpass", Mode: "managed", Credentials: map[string]string{
		userpass.ClientIDField:     "default",
		userpass.ClientSecretField: "password",
	}})
	require.NoError(t, err)
	clientCtx = metadata.AppendToOutgoingContext(clientCtx, "authorization", authr.AccessToken)

	sub, err := eventC.Subscribe(clientCtx)
	require.NotNil(t, sub)
	require.NoError(t, err)

	waitc := make(chan struct{})
	serverRunning := true
	appsCreated := 0
	numRecvd := atomic.Int32{}
	go func() {
		for {
			ev, err := sub.Recv()
			require.NoError(t, err)
			numRecvd.Add(1)
			logrus.WithField("module", "test-client").Infof("Received event %v", ev)
			if numRecvd.Load() >= 4 {
				logrus.Infof("Finished receiving")
				break
			}
		}
		close(waitc)
	}()

	for serverRunning {
		select {
		case <-clientCtx.Done():
			logrus.Infof("Done")
			serverRunning = false
		case <-waitc:
			logrus.Infof("Client closed the connection")
			cancel()
			serverRunning = false
		default:
			if appsCreated > 4 {
				log().Infof("Reached limit")
				serverRunning = false
				cancel()
				continue
			}
			time.Sleep(100 * time.Millisecond)
			_, err := appC.ArgoprojV1alpha1().Applications(testNamespace).Create(context.TODO(), &v1alpha1.Application{
				ObjectMeta: v1.ObjectMeta{
					Name:      fmt.Sprintf("app%d", appsCreated+1),
					Namespace: testNamespace,
				},
			}, v1.CreateOptions{})
			require.NoError(t, err)
			appsCreated += 1
		}
	}
	<-waitc
	err = s.Shutdown()
	require.NoError(t, err)
	assert.Equal(t, 5, appsCreated)
	assert.Equal(t, int32(4), numRecvd.Load())
}

func Test_EndToEnd_Push(t *testing.T) {
	objs := make([]runtime.Object, 10)
	for i := 0; i < 10; i += 1 {
		objs[i] = runtime.Object(&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: fmt.Sprintf("test%d", i), Namespace: "default"}})
	}
	appC := fakeappclient.NewSimpleClientset(objs...)
	conn, s := newConn(t, fakekube.NewKubernetesFakeClient())
	defer conn.Close()
	authC := authapi.NewAuthenticationClient(conn)
	eventC := eventstreamapi.NewEventStreamClient(conn)

	clientCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get authentication token and store in context
	authr, err := authC.Authenticate(clientCtx, &authapi.AuthRequest{Method: "userpass", Mode: "managed", Credentials: map[string]string{
		userpass.ClientIDField:     "default",
		userpass.ClientSecretField: "password",
	}})
	require.NoError(t, err)
	clientCtx = metadata.AppendToOutgoingContext(clientCtx, "authorization", authr.AccessToken)

	pushc, err := eventC.Push(clientCtx)
	require.NoError(t, err)
	start := time.Now()
	for i := 0; i < 10; i += 1 {
		ev := event.NewEventSource("").ApplicationEvent(event.SpecUpdate, &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("test%d", i),
				Namespace: "default",
			}})
		cev, cerr := format.ToProto(ev)
		require.NoError(t, cerr)
		_ = pushc.Send(&eventstreamapi.Event{Event: cev})
	}
	summary, err := pushc.CloseAndRecv()
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, int32(10), summary.Received)
	end := time.Now()

	// Wait until the context is done
	<-clientCtx.Done()

	log().Infof("Took %v to process", end.Sub(start))
	_ = s.Shutdown()

	// Should have been grabbed by queue processor
	q := s.QueuesForE2EOnly()
	assert.Equal(t, 0, q.RecvQ("default").Len())

	// All applications should have been created by now on the server
	apps, err := appC.ArgoprojV1alpha1().Applications("default").List(context.Background(), v1.ListOptions{})
	assert.NoError(t, err)
	assert.Len(t, apps.Items, 10)
}

func Test_AgentServer(t *testing.T) {
	app := &v1alpha1.Application{
		ObjectMeta: v1.ObjectMeta{
			Name:      "testapp",
			Namespace: "client",
		},
	}
	sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
	actx, acancel := context.WithTimeout(context.Background(), 20*time.Second)
	fakeAppcServer := fakeappclient.NewSimpleClientset()
	am := auth.NewMethods()
	up := userpass.NewUserPassAuthentication("")
	err := am.RegisterMethod("userpass", up)
	require.NoError(t, err)
	up.UpsertUser("client", "insecure")
	s, err := principal.NewServer(sctx, fakekube.NewKubernetesFakeClient(), "server",
		principal.WithGRPC(true),
		principal.WithListenerPort(0),
		principal.WithServerName("control-plane"),
		principal.WithGeneratedTLS("control-plane"),
		principal.WithAuthMethods(am),
		principal.WithNamespaces("client"),
		principal.WithGeneratedTokenSigningKey(),
	)
	require.NoError(t, err)
	require.NotNil(t, s)
	errch := make(chan error)
	_ = s.Start(sctx, errch)
	defer scancel()
	defer acancel()

	remote, err := client.NewRemote(s.ListenerForE2EOnly().Host(), s.ListenerForE2EOnly().Port(),
		client.WithInsecureSkipTLSVerify(),
		client.WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "client", userpass.ClientSecretField: "insecure"}),
	)
	require.NoError(t, err)
	fakeAppcAgent := fakeappclient.NewSimpleClientset()
	a, err := agent.NewAgent(actx, fakeAppcAgent, "client",
		agent.WithRemote(remote),
		agent.WithMode(types.AgentModeManaged.String()),
	)
	require.NotNil(t, a)
	require.NoError(t, err)
	_ = a.Start(actx)
	for !a.IsConnected() {
		log().Infof("waiting to connect")
		time.Sleep(100 * time.Millisecond)
	}
	log().Infof("Creating application")
	_, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Create(sctx, app, v1.CreateOptions{})
	require.NoError(t, err)
	for i := 0; i < 5; i += 1 {
		app, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Get(actx, "testapp", v1.GetOptions{})
		if err == nil {
			break
		}
		time.Sleep(1 * time.Second)
	}
	require.NoError(t, err)
	require.NotNil(t, app)
	app.Spec.Project = "hulahup"
	time.Sleep(1 * time.Second)
	_, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Update(actx, app, v1.UpdateOptions{})
	require.NoError(t, err)

	<-sctx.Done()
	<-actx.Done()
	err = s.Shutdown()
	require.NoError(t, err)
}

func Test_WithHTTP1WebSocket(t *testing.T) {
	fakeAppcServer := fakeappclient.NewSimpleClientset()
	am := auth.NewMethods()
	up := userpass.NewUserPassAuthentication("")
	err := am.RegisterMethod("userpass", up)
	require.NoError(t, err)
	up.UpsertUser("client", "insecure")

	proxyPort := 9090
	hostPort := func(host string, port int) string {
		return fmt.Sprintf("%s:%d", host, port)
	}

	startPrincipal := func(t *testing.T, ctx context.Context, enableWebSocket bool) *principal.Server {
		t.Helper()

		serverOpts := []principal.ServerOption{
			principal.WithGRPC(true),
			principal.WithListenerPort(0),
			principal.WithServerName("control-plane"),
			principal.WithGeneratedTLS("control-plane"),
			principal.WithAuthMethods(am),
			principal.WithNamespaces("client"),
			principal.WithGeneratedTokenSigningKey(),
		}

		if enableWebSocket {
			serverOpts = append(serverOpts, principal.WithWebSocket(true))
		}

		s, err := principal.NewServer(ctx, fakekube.NewKubernetesFakeClient(), "server", serverOpts...)

		require.NoError(t, err)
		require.NotNil(t, s)
		errch := make(chan error)
		err = s.Start(ctx, errch)
		require.NoError(t, err)

		return s
	}

	createRemote := func(t *testing.T, enableWebSocket bool, port int) *client.Remote {
		t.Helper()
		opts := []client.RemoteOption{
			client.WithInsecureSkipTLSVerify(),
			client.WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "client", userpass.ClientSecretField: "insecure"}),
		}

		if enableWebSocket {
			opts = append(opts, client.WithWebSocket(true))
		}

		remote, err := client.NewRemote("", port, opts...)
		require.NoError(t, err)

		return remote
	}

	startAgent := func(t *testing.T, ctx context.Context, remote *client.Remote) (*client.Remote, *agent.Agent) {
		t.Helper()

		fakeAppcAgent := fakeappclient.NewSimpleClientset()
		a, err := agent.NewAgent(ctx, fakeAppcAgent, "client",
			agent.WithRemote(remote),
			agent.WithMode(types.AgentModeManaged.String()),
		)
		require.NotNil(t, a)
		require.NoError(t, err)
		err = a.Start(ctx)
		require.NoError(t, err)
		return remote, a
	}

	t.Run("agent should not connect via proxy with WebSocket disabled", func(t *testing.T) {
		sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
		actx, acancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer scancel()
		defer acancel()

		// Create the principal server with WebSocket disabled.
		s := startPrincipal(t, sctx, false)
		defer s.Shutdown()

		// Create a reverse proxy that downgrades the incoming requests to HTTP/1.1
		http1Proxy := proxy.StartHTTP2DowngradingProxy(t, hostPort("", proxyPort), hostPort("", s.ListenerForE2EOnly().Port()))
		defer http1Proxy.Shutdown(sctx)

		// Create a remote agent with WebSocket disabled
		remote := createRemote(t, false, proxyPort)

		// We should not be able to connect since WebSocket is disabled and proxy downgrades to HTTP/1.1
		err = remote.Connect(actx, false)
		require.Error(t, err)
	})

	t.Run("agent should connect via proxy with WebSocket enabled", func(t *testing.T) {
		sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
		actx, acancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer scancel()
		defer acancel()

		// Create the principal server with WebSocket enabled.
		s := startPrincipal(t, sctx, true)
		defer s.Shutdown()

		// Create a reverse proxy that downgrades the incoming requests to HTTP/1.1
		http1Proxy := proxy.StartHTTP2DowngradingProxy(t, hostPort("", proxyPort), hostPort("", s.ListenerForE2EOnly().Port()))
		defer http1Proxy.Shutdown(sctx)

		// Create an agent with WebSocket enabled
		remote, a := startAgent(t, actx, createRemote(t, true, proxyPort))
		defer a.Stop()

		// The agent should be able to connect to the principal via the HTTP/2 incompatible proxy.
		require.NoError(t, remote.Connect(actx, false))
		require.True(t, a.IsConnected())

		log().Infof("Creating application")
		app := &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      "testapp",
				Namespace: "client",
			},
		}
		_, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Create(sctx, app, v1.CreateOptions{})
		require.NoError(t, err)
		for i := 0; i < 5; i += 1 {
			app, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Get(actx, "testapp", v1.GetOptions{})
			if err == nil {
				break
			}
			time.Sleep(1 * time.Second)
		}
		require.NoError(t, err)
		require.NotNil(t, app)
		app.Spec.Project = "hulahup"
		time.Sleep(1 * time.Second)
		_, err = fakeAppcServer.ArgoprojV1alpha1().Applications("client").Update(actx, app, v1.UpdateOptions{})
		require.NoError(t, err)
	})

	t.Run("principal with WebSocket enabled should also support agents that use raw gRPC", func(t *testing.T) {
		// Check if the principal with WebSocket enabled can also handle raw gRPC requests
		// from agents that are not behind HTTP/1.1 proxy.
		sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
		actx, acancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer scancel()
		defer acancel()

		// Create the principal server with WebSocket enabled.
		s := startPrincipal(t, sctx, true)
		defer s.Shutdown()

		// Create an agent with WebSocket disabled that is not behind a proxy. The agent will use a regular gRPC client.
		remote, a := startAgent(t, actx, createRemote(t, false, s.ListenerForE2EOnly().Port()))
		defer a.Stop()

		// The agent should be able to connect to the principal via regular gRPC.
		require.NoError(t, remote.Connect(actx, false))
		require.True(t, a.IsConnected())
	})
}

func log() *logrus.Entry {
	return logrus.WithField("TEST", "test")
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
