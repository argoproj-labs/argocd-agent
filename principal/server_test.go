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

package principal

import (
	"context"
	"crypto/x509"
	"math/big"
	"os"
	"path"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/backend"
	"github.com/argoproj-labs/argocd-agent/internal/backend/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/config"
	"github.com/argoproj-labs/argocd-agent/internal/event"
	"github.com/argoproj-labs/argocd-agent/internal/manager"
	"github.com/argoproj-labs/argocd-agent/internal/manager/appproject"
	"github.com/argoproj-labs/argocd-agent/internal/manager/gpgkey"
	"github.com/argoproj-labs/argocd-agent/internal/manager/repository"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	fakecerts "github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
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

func Test_ServerWithTLSConfig(t *testing.T) {
	tempDir := t.TempDir()
	t.Run("Valid TLS key pair", func(t *testing.T) {
		templ := certTempl
		fakecerts.WriteSelfSignedCert(t, "rsa", path.Join(tempDir, "test-cert"), templ)
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "test-cert.crt"), path.Join(tempDir, "test-cert.key")),
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.NoError(t, err)
		assert.NotNil(t, tlsConfig)
	})
	t.Run("Non-existing TLS key pair", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath(path.Join(tempDir, "other-cert.crt"), path.Join(tempDir, "other-cert.key")),
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
		)
		require.NoError(t, err)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, tlsConfig)
	})

	t.Run("Invalid TLS certificate", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithTLSKeyPairFromPath("server_test.go", "server_test.go"),
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
		)
		require.NoError(t, err)
		require.NotNil(t, s)
		tlsConfig, err := s.loadTLSConfig()
		assert.ErrorContains(t, err, "failed to find any PEM data")
		assert.Nil(t, tlsConfig)
	})
}

func Test_NewServer(t *testing.T) {
	t.Run("Instantiate new server object with non-default options", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithListenerAddress("0.0.0.0"), WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotEqual(t, defaultOptions(), s.options)
		assert.Equal(t, "0.0.0.0", s.options.address)
	})

	t.Run("Instantiate new server object with invalid option", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithListenerPort(-1), WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		assert.Error(t, err)
		assert.Nil(t, s)
	})

	t.Run("Redis proxy should be nil when disabled", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey(), WithRedisProxyDisabled())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Nil(t, s.redisProxy)
	})

	t.Run("Redis proxy should be created when not disabled", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotNil(t, s.redisProxy)
	})

	t.Run("Informer sync timeout should be configurable", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey(), WithInformerSyncTimeout(10*time.Second))
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, 10*time.Second, s.options.informerSyncTimeout)
	})

	t.Run("Informer sync timeout should default to 60s when not set", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace, WithGeneratedTokenSigningKey())
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, 60*time.Second, s.options.informerSyncTimeout)
	})

	t.Run("Agent registration manager should be initialized", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
		)
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.NotNil(t, s.agentRegistrationManager)
	})

	t.Run("Agent registration should be disabled by default", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
		)
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.False(t, s.agentRegistrationManager.IsSelfAgentRegistrationEnabled())
	})

	t.Run("Agent registration should be enabled when configured", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
			WithAgentRegistration(true),
		)
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.True(t, s.agentRegistrationManager.IsSelfAgentRegistrationEnabled())
	})

	t.Run("Resource proxy address should be configurable", func(t *testing.T) {
		s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
			WithGeneratedTokenSigningKey(),
			WithRedisProxyDisabled(),
			WithResourceProxyAddress("custom-proxy:8443"),
		)
		assert.NoError(t, err)
		assert.NotNil(t, s)
		assert.Equal(t, "custom-proxy:8443", s.options.resourceProxyAddress)
	})
}

func Test_handleResyncOnConnect(t *testing.T) {
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithGeneratedTokenSigningKey(),
		WithRedisProxyDisabled(),
	)
	s.kubeClient.RestConfig = &rest.Config{}
	require.NoError(t, err)
	s.events = event.NewEventSource("test")

	agent := types.NewAgent("test", types.AgentModeAutonomous.String())
	err = s.queues.Create(agent.Name())
	assert.Nil(t, err)

	t.Run("return if the agent is already synced", func(t *testing.T) {
		s.resyncStatus.resynced(agent.Name())

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Zero(t, sendQ.Len())
	})

	t.Run("request SyncedResourceList in autonomous mode", func(t *testing.T) {
		s.resyncStatus = newResyncStatus()

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.SyncedResourceList.String(), ev.Type())
	})

	t.Run("request resource resync in managed mode", func(t *testing.T) {
		agent = types.NewAgent("test", types.AgentModeManaged.String())
		s.resyncStatus = newResyncStatus()

		err = s.handleResyncOnConnect(agent)
		assert.Nil(t, err)

		sendQ := s.queues.SendQ(agent.Name())
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.EventRequestResourceResync.String(), ev.Type())
	})
}

func Test_RunHandlersOnConnect(t *testing.T) {
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithGeneratedTokenSigningKey(),
		WithRedisProxyDisabled(),
	)
	require.NoError(t, err)
	s.events = event.NewEventSource("test")

	expected := types.NewAgent("test", types.AgentModeAutonomous.String())

	output := make(chan types.Agent)

	s.handlersOnConnect = []handlersOnConnect{func(agent types.Agent) error {
		output <- types.NewAgent(agent.Name(), agent.Mode())
		return nil
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go s.RunHandlersOnConnect(ctx)

	s.notifyOnConnect <- expected

	got := <-output
	assert.Equal(t, expected, got)
}

func Test_ServerStartWithDefaultSyncTimeout(t *testing.T) {
	s, err := NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps(testNamespace), testNamespace,
		WithGeneratedTokenSigningKey(),
		WithRedisProxyDisabled(),
	)
	require.NoError(t, err)
	s.kubeClient.RestConfig = &rest.Config{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errch := make(chan error, 1)
	err = s.Start(ctx, errch)
	require.NoError(t, err)

	defer s.Shutdown()
}

func Test_SendCurrentStateToAgent(t *testing.T) {
	const agentName = "test-agent"
	const ns = "argocd"

	// newServer returns a Server obj with appropriate mock harness
	newServer := func(t *testing.T, mockProjBackend *mocks.AppProject, mockRepoBackend *mocks.Repository) *Server {
		t.Helper()
		t.Cleanup(func() {
			mockProjBackend.AssertExpectations(t)
			mockRepoBackend.AssertExpectations(t)
		})
		projMgr, err := appproject.NewAppProjectManager(mockProjBackend, ns)
		require.NoError(t, err)
		repoMgr := repository.NewManager(mockRepoBackend, ns, false)
		mockGPGKeyBackend := mocks.NewGPGKey(t)
		mockGPGKeyBackend.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil, k8serrors.NewNotFound(schema.GroupResource{Group: "", Resource: "configmaps"}, "argocd-gpg-keys-cm")).Maybe()
		gpgMgr := gpgkey.NewManager(mockGPGKeyBackend, ns)
		s := &Server{
			ctx:            context.Background(),
			namespace:      ns,
			queues:         queue.NewSendRecvQueues(),
			events:         event.NewEventSource("test"),
			projectManager: projMgr,
			repoManager:    repoMgr,
			gpgKeyManager:  gpgMgr,
			repoToAgents:   NewMapToSet(),
			projectToRepos: NewMapToSet(),
		}
		require.NoError(t, s.queues.Create(agentName))
		return s
	}

	// generateAppProject returns a simple AppProject with the given name, which allows Applications (et al) from agent namespace
	generateAppProject := func(name string) v1alpha1.AppProject {
		return v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: v1alpha1.AppProjectSpec{
				Destinations: []v1alpha1.ApplicationDestination{
					{Name: agentName, Namespace: "*", Server: "*"},
				},
				SourceNamespaces: []string{agentName},
			},
		}
	}

	// generateRepoSecret returns an simple Argo CD repository Secret for a given project
	generateRepoSecret := func(name, projectName string) corev1.Secret {
		return corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels: map[string]string{
					common.LabelKeySecretType: common.LabelValueSecretTypeRepository,
				},
			},
			Data: map[string][]byte{
				"project": []byte(projectName),
			},
		}
	}

	t.Run("sends matching project to agent", func(t *testing.T) {
		proj := generateAppProject("proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, mock.Anything).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 1, sendQ.Len())

		ev, shutdown := sendQ.Get()
		assert.False(t, shutdown)
		assert.Equal(t, event.SpecUpdate.String(), ev.Type())
		assert.Equal(t, event.TargetAppProject.String(), ev.DataSchema())
	})

	t.Run("skips project that does not match the agent", func(t *testing.T) {
		proj := v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{Name: "proj1", Namespace: ns},
			Spec: v1alpha1.AppProjectSpec{
				Destinations: []v1alpha1.ApplicationDestination{
					{Name: "other-agent", Namespace: "*", Server: "*"},
				},
				SourceNamespaces: []string{"other-agent"},
			},
		}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, mock.Anything).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 0, sendQ.Len())
	})

	t.Run("skips project with SourceUIDAnnotation", func(t *testing.T) {
		proj := generateAppProject("proj1")
		proj.Annotations = map[string]string{manager.SourceUIDAnnotation: "some-uid"}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, mock.Anything).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 0, sendQ.Len())
	})

	t.Run("skips project with SkipSyncLabel=true", func(t *testing.T) {
		proj := generateAppProject("proj1")
		proj.Labels = map[string]string{config.SkipSyncLabel: "true"}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, mock.Anything).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 0, sendQ.Len())
	})

	t.Run("does not skip project with SkipSyncLabel=false", func(t *testing.T) {
		proj := generateAppProject("proj1")
		proj.Labels = map[string]string{config.SkipSyncLabel: "false"}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, mock.Anything).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 1, sendQ.Len())
	})

	t.Run("sends repository for matching project and updates mappings", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repo := generateRepoSecret("repo1", "proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		// 1 project event + 1 repository event
		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 2, sendQ.Len())

		assert.True(t, s.projectToRepos.Get("proj1")["repo1"])
		assert.True(t, s.repoToAgents.Get("repo1")[agentName])
	})

	t.Run("sends repo-creds for matching project", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repoCreds := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "creds1",
				Namespace: ns,
				Labels: map[string]string{
					common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds,
				},
			},
			Data: map[string][]byte{
				"project": []byte("proj1"),
			},
		}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{repoCreds}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		// 1 project event + 1 repo-creds event
		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 2, sendQ.Len())

		assert.True(t, s.projectToRepos.Get("proj1")["creds1"])
		assert.True(t, s.repoToAgents.Get("creds1")[agentName])
	})

	t.Run("skips repository whose project was not sent to agent", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repo := generateRepoSecret("repo1", "unknown-project")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		// Only 1 project event, no repository event
		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 1, sendQ.Len())
	})

	t.Run("sends projects and repos for wildcard destination match", func(t *testing.T) {
		proj := v1alpha1.AppProject{
			ObjectMeta: metav1.ObjectMeta{Name: "proj1", Namespace: ns},
			Spec: v1alpha1.AppProjectSpec{
				Destinations: []v1alpha1.ApplicationDestination{
					{Name: "test-*", Namespace: "*", Server: "*"},
				},
				SourceNamespaces: []string{"test-*"},
			},
		}
		repo := generateRepoSecret("repo1", "proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 2, sendQ.Len())
	})

	t.Run("skips repository when it has SkipSyncLabel=true", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repo := generateRepoSecret("repo1", "proj1")
		repo.Labels = map[string]string{config.SkipSyncLabel: "true"}

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 1, sendQ.Len())
		assert.Empty(t, s.repoToAgents.Get("repo1"))
	})

	t.Run("skips repository when its project has SkipSyncLabel=true", func(t *testing.T) {
		proj := generateAppProject("proj1")
		proj.Labels = map[string]string{config.SkipSyncLabel: "true"}
		repo := generateRepoSecret("repo1", "proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 0, sendQ.Len())
		assert.Empty(t, s.projectToRepos.Get("proj1"))
		assert.Empty(t, s.repoToAgents.Get("repo1"))
	})

	t.Run("sends GPG key ConfigMap when it exists without skip-sync label", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repo := generateRepoSecret("repo1", "proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		mockGPGBackend := mocks.NewGPGKey(t)
		gpgCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.ArgoCDGPGKeysConfigMapName,
				Namespace: ns,
			},
			Data: map[string]string{"key1": "data1"},
		}
		mockGPGBackend.On("Get", mock.Anything, common.ArgoCDGPGKeysConfigMapName, ns).Return(gpgCM, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		s.gpgKeyManager = gpgkey.NewManager(mockGPGBackend, ns)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		// 1 project + 1 repo + 1 GPG key
		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 3, sendQ.Len())
	})

	t.Run("skips GPG key ConfigMap when it has SkipSyncLabel=true", func(t *testing.T) {
		proj := generateAppProject("proj1")
		repo := generateRepoSecret("repo1", "proj1")

		mockProjBackend := &mocks.AppProject{}
		mockRepoBackend := &mocks.Repository{}
		mockProjBackend.On("List", mock.Anything, mock.Anything).Return([]v1alpha1.AppProject{proj}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepository},
		}).Return([]corev1.Secret{repo}, nil)
		mockRepoBackend.On("List", mock.Anything, backend.RepositorySelector{
			Namespace: ns,
			Labels:    map[string]string{common.LabelKeySecretType: common.LabelValueSecretTypeRepoCreds},
		}).Return([]corev1.Secret{}, nil)

		mockGPGBackend := mocks.NewGPGKey(t)
		gpgCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      common.ArgoCDGPGKeysConfigMapName,
				Namespace: ns,
				Labels:    map[string]string{config.SkipSyncLabel: "true"},
			},
			Data: map[string]string{"key1": "data1"},
		}
		mockGPGBackend.On("Get", mock.Anything, common.ArgoCDGPGKeysConfigMapName, ns).Return(gpgCM, nil)

		s := newServer(t, mockProjBackend, mockRepoBackend)
		s.gpgKeyManager = gpgkey.NewManager(mockGPGBackend, ns)
		err := s.sendCurrentStateToAgent(agentName)
		require.NoError(t, err)

		// 1 project + 1 repo, GPG key skipped
		sendQ := s.queues.SendQ(agentName)
		assert.Equal(t, 2, sendQ.Len())
	})

}

func init() {
	logrus.SetLevel(logrus.TraceLevel)
}
