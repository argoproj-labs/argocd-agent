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

package client

import (
	"context"
	"crypto/x509"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	"github.com/argoproj-labs/argocd-agent/pkg/types"
	"github.com/argoproj-labs/argocd-agent/principal"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/argoproj-labs/argocd-agent/test/fake/testcerts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Connect(t *testing.T) {
	tempDir := t.TempDir()
	basePath := path.Join(tempDir, "certs")
	testcerts.WriteSelfSignedCert(t, "rsa", basePath, x509.Certificate{SerialNumber: big.NewInt(1)})

	s, err := principal.NewServer(context.TODO(), kube.NewKubernetesFakeClientWithApps("default"), "default",
		principal.WithGRPC(true),
		principal.WithListenerPort(0),
		principal.WithTLSKeyPairFromPath(basePath+".crt", basePath+".key"),
		principal.WithGeneratedTokenSigningKey(),
	)

	am := userpass.NewUserPassAuthentication("")
	am.UpsertUser("default", "password")
	s.AuthMethodsForE2EOnly().RegisterMethod("userpass", am)
	require.NoError(t, err)
	errch := make(chan error)
	err = s.Start(context.Background(), errch)
	require.NoError(t, err)

	t.Cleanup(func() {
		if s != nil {
			require.NoError(t, s.Shutdown())
		}
	})

	t.Run("Connect to a server", func(t *testing.T) {
		r, err := NewRemote("127.0.0.1", s.ListenerForE2EOnly().Port(),
			WithInsecureSkipTLSVerify(),
			WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "default", userpass.ClientSecretField: "password"}),
			WithClientMode(types.AgentModeManaged),
		)
		require.NoError(t, err)
		require.NotNil(t, r)
		ctx, cancelFn := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelFn()
		err = r.Connect(ctx, false)
		assert.NoError(t, err)
		assert.NotNil(t, r.conn)
		require.NotNil(t, r.accessToken)
		require.NotNil(t, r.accessToken.Claims)
		sub, err := r.accessToken.Claims.GetSubject()
		assert.NoError(t, err)
		assert.NotEmpty(t, sub)
		authSub, err := auth.ParseAuthSubject(sub)
		require.NoError(t, err)
		assert.Equal(t, "default", authSub.ClientID)
	})

	t.Run("Invalid auth and context deadline reached", func(t *testing.T) {
		r, err := NewRemote("127.0.0.1", s.ListenerForE2EOnly().Port(),
			WithInsecureSkipTLSVerify(),
			WithAuth("userpass", auth.Credentials{userpass.ClientIDField: "default", userpass.ClientSecretField: "passwor"}),
		)
		require.NoError(t, err)
		require.NotNil(t, r)
		ctx, cancelFn := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFn()
		err = r.Connect(ctx, false)
		assert.Error(t, err)
		assert.Nil(t, r.conn)
	})

}
