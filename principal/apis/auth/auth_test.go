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

package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/argoproj-labs/argocd-agent/internal/auth"
	authmock "github.com/argoproj-labs/argocd-agent/internal/auth/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/auth/userpass"
	issuermock "github.com/argoproj-labs/argocd-agent/internal/issuer/mocks"
	"github.com/argoproj-labs/argocd-agent/internal/queue"
	"github.com/argoproj-labs/argocd-agent/internal/version"
	"github.com/argoproj-labs/argocd-agent/pkg/api/grpc/authapi"
	"github.com/argoproj-labs/argocd-agent/principal/registration"
	"github.com/argoproj-labs/argocd-agent/test/fake/kube"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_Authenticate(t *testing.T) {
	encodedSubject := `{"clientID":"user1","mode":"managed"}`
	queues := queue.NewSendRecvQueues()
	testVersion := version.New("argocd-agent").Version()

	t.Run("Authentication method unsupported", func(t *testing.T) {
		auths, err := NewServer(queues, nil, nil)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion,
		})
		assert.ErrorContains(t, err, authFailedMessage)
	})

	t.Run("Missing version", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		// Mock authentication to succeed so we reach version validation
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		auths, err := NewServer(queues, ams, nil)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     "",
		})
		assert.ErrorContains(t, err, "agent version is required")
	})

	t.Run("Version mismatch", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		// Mock authentication to succeed so we reach version validation
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		auths, err := NewServer(queues, ams, nil)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion + "-mismatch",
		})
		assert.ErrorContains(t, err, "version mismatch")
	})

	t.Run("Authentication successful", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		iss := issuermock.NewIssuer(t)
		iss.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		iss.On("IssueRefreshToken", encodedSubject, mock.Anything).Return("refresh", nil)

		auths, err := NewServer(queues, ams, iss)
		require.NoError(t, err)
		r, err := auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.NoError(t, err)
		require.NotNil(t, r)
		assert.Equal(t, "access", r.AccessToken)
		assert.Equal(t, "refresh", r.RefreshToken)
		assert.Equal(t, testVersion, r.Version)
	})

	t.Run("Wrong credentials", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("", errAuthenticationFailed)
		ams.RegisterMethod("userpass", am)
		auths, err := NewServer(queues, ams, nil)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "wordpass"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.ErrorContains(t, err, "authentication failed")
	})

	t.Run("Error issuing an access token", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)
		iss := issuermock.NewIssuer(t)
		iss.On("IssueAccessToken", encodedSubject, mock.Anything).Return("", fmt.Errorf("oops"))
		auths, err := NewServer(queues, ams, iss)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "wordpass"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.ErrorContains(t, err, "authentication failed")
	})

	t.Run("Error issuing an refresh token", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)
		iss := issuermock.NewIssuer(t)
		iss.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		iss.On("IssueRefreshToken", encodedSubject, mock.Anything).Return("", fmt.Errorf("oops"))
		auths, err := NewServer(queues, ams, iss)
		require.NoError(t, err)
		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "wordpass"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.ErrorContains(t, err, "authentication failed")
	})

	t.Run("Continue authentication even when self cluster registration is disabled", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		iss := issuermock.NewIssuer(t)
		iss.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		iss.On("IssueRefreshToken", encodedSubject, mock.Anything).Return("refresh", nil)

		// Create manager with agent registration disabled
		kubeclient := kube.NewFakeKubeClient("argocd")
		mgr := registration.NewAgentRegistrationManager(false, "argocd", "resource-proxy:8443", "", kubeclient, iss)

		auths, err := NewServer(queues, ams, iss, WithAgentRegistrationManager(mgr))
		require.NoError(t, err)
		r, err := auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.NoError(t, err)
		require.NotNil(t, r)
		assert.Equal(t, "access", r.AccessToken)
		assert.Equal(t, "refresh", r.RefreshToken)
	})

	t.Run("Authentication fails when agent registration fails", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		// Create manager with agent registration enabled but no CA cert path to make it fail
		kubeclient := kube.NewFakeClientsetWithResources()
		mockIss := issuermock.NewIssuer(t)
		mockIss.On("IssueResourceProxyToken", "user1").Return("test-token", nil)
		mgr := registration.NewAgentRegistrationManager(true, "argocd", "resource-proxy:8443", "", kubeclient, mockIss)

		auths, err := NewServer(queues, ams, nil, WithAgentRegistrationManager(mgr))
		require.NoError(t, err)

		_, err = auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.ErrorContains(t, err, "authentication failed")
	})

	t.Run("Authentication successful with nil cluster registration manager", func(t *testing.T) {
		ams := auth.NewMethods()
		am := authmock.NewMethod(t)
		am.On("Authenticate", mock.Anything, mock.Anything).Return("user1", nil)
		ams.RegisterMethod("userpass", am)

		iss := issuermock.NewIssuer(t)
		iss.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		iss.On("IssueRefreshToken", encodedSubject, mock.Anything).Return("refresh", nil)

		// No cluster registration manager provided
		auths, err := NewServer(queues, ams, iss)
		require.NoError(t, err)
		r, err := auths.Authenticate(context.TODO(), &authapi.AuthRequest{
			Method:      "userpass",
			Credentials: map[string]string{userpass.ClientIDField: "user1", userpass.ClientSecretField: "password"},
			Mode:        "managed",
			Version:     testVersion,
		})
		require.NoError(t, err)
		require.NotNil(t, r)
		assert.Equal(t, "access", r.AccessToken)
		assert.Equal(t, "refresh", r.RefreshToken)
	})

}

func Test_RefreshToken(t *testing.T) {
	encodedSubject := `{"clientID":"user1","mode":"managed"}`
	queues := queue.NewSendRecvQueues()
	t.Run("Get a new access token from refresh token", func(t *testing.T) {
		methods := auth.NewMethods()

		claims := issuermock.NewClaims(t)
		claims.On("GetSubject").Return(encodedSubject, nil)
		claims.On("GetExpirationTime").Return(jwt.NewNumericDate(time.Now().Add(1*time.Hour)), nil)
		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(claims, nil)
		issuer.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		// issuer.On("IssueRefreshToken", "user1", mock.Anything).Return("refresh", nil)

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.NoError(t, err)
		require.NotNil(t, nr)
		assert.Equal(t, "access", nr.AccessToken)
		assert.Equal(t, "", nr.RefreshToken)
	})

	t.Run("Get a new access token and refresh token from refresh token", func(t *testing.T) {
		methods := auth.NewMethods()
		claims := issuermock.NewClaims(t)
		claims.On("GetSubject").Return(encodedSubject, nil)
		claims.On("GetExpirationTime").Return(jwt.NewNumericDate(time.Now().Add(refreshTokenAutoRefresh-1*time.Minute)), nil)
		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(claims, nil)
		issuer.On("IssueAccessToken", encodedSubject, mock.Anything).Return("access", nil)
		issuer.On("IssueRefreshToken", encodedSubject, mock.Anything).Return("refresh", nil)

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.NoError(t, err)
		require.NotNil(t, nr)
		assert.Equal(t, "access", nr.AccessToken)
		assert.Equal(t, "refresh", nr.RefreshToken)
	})

	t.Run("No refresh token supplied", func(t *testing.T) {
		methods := auth.NewMethods()

		issuer := issuermock.NewIssuer(t)
		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{})
		require.Error(t, err)
		require.Nil(t, nr)
	})

	t.Run("Verification of refresh token fails", func(t *testing.T) {
		methods := auth.NewMethods()

		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(nil, fmt.Errorf("oops"))

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.Error(t, err)
		require.Nil(t, nr)
	})

	t.Run("No subject in refresh token", func(t *testing.T) {
		methods := auth.NewMethods()

		claims := issuermock.NewClaims(t)
		claims.On("GetSubject").Return("", fmt.Errorf("oops"))

		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(claims, nil)

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.Error(t, err)
		require.Nil(t, nr)
	})

	t.Run("No expiration date in refresh token", func(t *testing.T) {
		methods := auth.NewMethods()

		claims := issuermock.NewClaims(t)
		claims.On("GetSubject").Return(encodedSubject, nil)
		claims.On("GetExpirationTime").Return(nil, fmt.Errorf("oops"))

		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(claims, nil)

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.Error(t, err)
		require.Nil(t, nr)
	})

	t.Run("Error issuing token", func(t *testing.T) {
		methods := auth.NewMethods()

		claims := issuermock.NewClaims(t)
		claims.On("GetSubject").Return(encodedSubject, nil)
		claims.On("GetExpirationTime").Return(jwt.NewNumericDate(time.Now().Add(1*time.Hour)), nil)

		issuer := issuermock.NewIssuer(t)
		issuer.On("ValidateRefreshToken", "refresh").Return(claims, nil)
		issuer.On("IssueAccessToken", encodedSubject, mock.Anything).Return("", fmt.Errorf("ooops"))

		auths, err := NewServer(queues, methods, issuer)
		require.NoError(t, err)
		nr, err := auths.RefreshToken(context.TODO(), &authapi.RefreshTokenRequest{RefreshToken: "refresh"})
		require.Error(t, err)
		require.Nil(t, nr)
	})

}
