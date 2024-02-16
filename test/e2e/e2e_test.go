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

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	fakeappclient "github.com/argoproj/argo-cd/v2/pkg/client/clientset/versioned/fake"
	format "github.com/cloudevents/sdk-go/binding/format/protobuf/v2"
	"github.com/jannfis/argocd-agent/agent"
	"github.com/jannfis/argocd-agent/internal/auth"
	"github.com/jannfis/argocd-agent/internal/auth/userpass"
	"github.com/jannfis/argocd-agent/internal/backend"
	"github.com/jannfis/argocd-agent/internal/event"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/authapi"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/eventstreamapi"
	"github.com/jannfis/argocd-agent/pkg/client"
	"github.com/jannfis/argocd-agent/pkg/types"
	"github.com/jannfis/argocd-agent/principal"
	fakecerts "github.com/jannfis/argocd-agent/test/fake/testcerts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	fakekube "github.com/jannfis/argocd-agent/test/fake/kube"
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

func newConn(t *testing.T, appC *fakeappclient.Clientset) (*grpc.ClientConn, *principal.Server) {
	t.Helper()
	tempDir := t.TempDir()
	templ := certTempl
	fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)
	errch := make(chan error)

	s, err := principal.NewServer(context.TODO(), appC, testNamespace,
		principal.WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
		principal.WithListenerPort(0),
		principal.WithListenerAddress("127.0.0.1"),
		principal.WithShutDownGracePeriod(2*time.Second),
		principal.WithGRPC(true),
		principal.WithEventProcessors(10),
	)
	require.NoError(t, err)
	err = s.Start(context.Background(), errch)
	assert.NoError(t, err)

	am := userpass.NewUserPassAuthentication()
	am.UpsertUser("default", "password")
	s.AuthMethods().RegisterMethod("userpass", am)

	tlsC := &tls.Config{InsecureSkipVerify: true}
	creds := credentials.NewTLS(tlsC)
	conn, err := grpc.Dial(s.Listener().Address(),
		grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	return conn, s
}

func newServer(t *testing.T) *principal.Server {
	return nil
}

func newAgent(t *testing.T) *agent.Agent {
	return nil
}

// func newStreamingClient(t *testing.T, conn) (eventstreamapi.EventStreamClient, *grpc.ClientConn) {
// 	t.Helper()
// 	return eventstreamapi.NewEventStreamClient(conn), conn
// }

func Test_EndToEnd_Subscribe(t *testing.T) {
	// token, err := s.TokenIssuer().Issue("default", 1*time.Minute)
	// require.NoError(t, err)

	appC := fakeappclient.NewSimpleClientset()
	conn, s := newConn(t, appC)
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
	s.Shutdown()
	assert.Equal(t, 5, appsCreated)
	assert.Equal(t, int32(4), numRecvd.Load())
}

func Test_EndToEnd_Push(t *testing.T) {
	// objs := make([]runtime.Object, 10)
	// for i := 0; i < 10; i += 1 {
	// 	objs[i] = runtime.Object(&v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: fmt.Sprintf("test%d", i), Namespace: "default"}})
	// }
	appC := fakeappclient.NewSimpleClientset() //objs...)
	conn, s := newConn(t, appC)
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
		ev := event.NewEventEmitter("").NewApplicationEvent(event.ApplicationSpecUpdated, &v1alpha1.Application{
			ObjectMeta: v1.ObjectMeta{
				Name:      fmt.Sprintf("test%d", i),
				Namespace: "default",
			}})
		cev, cerr := format.ToProto(ev)
		require.NoError(t, cerr)
		pushc.Send(&eventstreamapi.Event{Event: cev})
	}
	summary, err := pushc.CloseAndRecv()
	require.NoError(t, err)
	require.NotNil(t, summary)
	assert.Equal(t, int32(10), summary.Received)
	end := time.Now()

	// Wait until the context is done
	<-clientCtx.Done()

	log().Infof("Took %v to process", end.Sub(start))
	s.Shutdown()

	// Should have been grabbed by queue processor
	q := s.Queues()
	assert.Equal(t, 0, q.RecvQ("default").Len())

	// All applications should have been created by now on the server
	apps, err := s.AppManager().Application.List(context.Background(), backend.ApplicationSelector{})
	assert.NoError(t, err)
	assert.Len(t, apps, 10)
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
	up := userpass.NewUserPassAuthentication()
	am.RegisterMethod("userpass", up)
	up.UpsertUser("client", "insecure")
	s, err := principal.NewServer(sctx, fakeAppcServer, "server",
		principal.WithGRPC(true),
		principal.WithListenerPort(0),
		principal.WithServerName("control-plane"),
		principal.WithGeneratedTLS("control-plane"),
		principal.WithAuthMethods(am),
		principal.WithNamespaces("client"),
	)
	require.NoError(t, err)
	require.NotNil(t, s)
	errch := make(chan error)
	s.Start(sctx, errch)
	defer scancel()
	defer acancel()

	remote, err := client.NewRemote(s.Listener().Host(), s.Listener().Port(),
		client.WithInsecureSkipTLSVerify(),
		client.WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "client", userpass.ClientSecretField: "insecure"}),
	)
	require.NoError(t, err)
	fakeAppcAgent := fakeappclient.NewSimpleClientset()
	fakeKubecAgent := fakekube.NewFakeKubeClient()
	a, err := agent.NewAgent(actx, fakeKubecAgent, fakeAppcAgent, "client",
		agent.WithRemote(remote),
		agent.WithMode(types.AgentModeManaged.String()),
	)
	require.NotNil(t, a)
	require.NoError(t, err)
	a.Start(actx)
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

func log() *logrus.Entry {
	return logrus.WithField("TEST", "test")
}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
