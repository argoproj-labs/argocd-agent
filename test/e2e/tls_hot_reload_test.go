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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/argocd-agent/internal/tlsutil"
	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
)

// constants for filenames for each of the TLS components
const (
	symlinkDir                  = "..data"
	hotReloadCACertFileName     = "ca.crt"
	hotReloadCAKeyFileName      = "ca.key"
	hotReloadServerCertFileName = "tls.crt"
	hotReloadServerKeyFileName  = "tls.key"
	hotReloadTLSSecretName      = "hot-reload-test-tls"
	hotReloadCASecretName       = "hot-reload-test-ca"
)

var fileProviderExpectedSuccessLogs = []string{
	"Change detected in TLS data, updating internal store",
	"Reloading TLS data",
	"Successfully reloaded TLS data",
}

var fileProviderExpectedFailLogs = []string{
	"Change detected in TLS data, updating internal store",
	"Reloading TLS data",
	"TLS hot reload watch failed",
}

var secretProviderExpectedSuccessLogs = []string{
	"Change detected in TLS data, updating internal store",
	"Successfully updated TLS configuration to have new data",
}

type TLSHotReloadTestSuite struct {
	fixture.BaseSuite
}

func TestTLSHotReloadTestSuite(t *testing.T) {
	suite.Run(t, new(TLSHotReloadTestSuite))
}

func (suite *TLSHotReloadTestSuite) SetupSuite() {
	suite.BaseSuite.SetupSuite()

	// Skip if running in cluster because we need to be able to read/write to file paths fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
}

func (suite *TLSHotReloadTestSuite) TearDownTest() {
	fixture.ClearEnvVarsFile()

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	fixture.RestartAgent(suite.T(), fixture.AgentManagedName)
	fixture.CheckReadiness(suite.T(), fixture.AgentManagedName)

	fixture.RestartAgent(suite.T(), fixture.AgentAutonomousName)
	fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)

	suite.BaseSuite.TearDownTest()
}

// generateAndWriteInitalCerts is a helper function that writes the inital certs to a symlinked ..data directory to
// mock the way Kubernetes mounts configmaps and secrets as a volume
func generateAndWriteInitalCerts(t *testing.T) (string, tls.Certificate, string, string, string, string) {
	dir := t.TempDir()

	initialDir := filepath.Join(dir, "initial")

	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)

	serverPEM, serverKeyPEM, err := tlsutil.GenerateServerCertificate("principal", ca.Leaf, ca.PrivateKey, nil, []string{"localhost", "127.0.0.1"}, tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	writeCertsToDir(t, initialDir, caPEM, caKeyPEM, serverPEM, serverKeyPEM)
	swapSymlink(t, dir, initialDir)

	require.NoError(t, os.Symlink(filepath.Join(symlinkDir, hotReloadCACertFileName), filepath.Join(dir, hotReloadCACertFileName)))
	require.NoError(t, os.Symlink(filepath.Join(symlinkDir, hotReloadServerCertFileName), filepath.Join(dir, hotReloadServerCertFileName)))
	require.NoError(t, os.Symlink(filepath.Join(symlinkDir, hotReloadServerKeyFileName), filepath.Join(dir, hotReloadServerKeyFileName)))

	return dir, ca, caPEM, caKeyPEM, serverPEM, serverKeyPEM
}

// createNewServerCert creates a new server cert and then symlinks it to the ..data directory
func createNewServerCert(t *testing.T, ca tls.Certificate, caPEM, caKeyPEM, dir string) {
	newDir := filepath.Join(dir, "new-certs")

	serverPEM, serverKeyPEM, err := tlsutil.GenerateServerCertificate("principal-v2", ca.Leaf, ca.PrivateKey, nil, []string{"localhost", "127.0.0.1"}, tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	writeCertsToDir(t, newDir, caPEM, caKeyPEM, serverPEM, serverKeyPEM)
	swapSymlink(t, dir, newDir)
}

// createNewCACert creates a new CA cert and then symlinks it to the ..data directory
func createNewCACert(t *testing.T, serverPEM, serverKeyPEM, dir string) {
	newDir := filepath.Join(dir, "new-ca")

	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	writeCertsToDir(t, newDir, caPEM, caKeyPEM, serverPEM, serverKeyPEM)
	swapSymlink(t, dir, newDir)
}

// writeCertsToDir writes the passed certificates to a directory
func writeCertsToDir(t *testing.T, dir, caPEM, caKeyPEM, serverPEM, serverKeyPEM string) {
	err := os.MkdirAll(dir, 0o755)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, hotReloadCACertFileName), []byte(caPEM), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hotReloadCAKeyFileName), []byte(caKeyPEM), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hotReloadServerCertFileName), []byte(serverPEM), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hotReloadServerKeyFileName), []byte(serverKeyPEM), 0o644))
}

// swapSymlink creates a temp symlink and then renames it to be the expected directory
func swapSymlink(t *testing.T, baseDir, targetDir string) {
	tempSymlink := filepath.Join(baseDir, "..data.tmp")
	os.Remove(tempSymlink)
	require.NoError(t, os.Symlink(targetDir, tempSymlink))
	require.NoError(t, os.Rename(tempSymlink, filepath.Join(baseDir, symlinkDir)))
}

// createTestSecrets is a helper function that creates TLS secrets to be used during the tests
func createTestSecrets(t *testing.T, ctx context.Context, kubeClient fixture.KubeClient) (*corev1.Secret, *corev1.Secret, tls.Certificate) {
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)
	ca, err := tls.X509KeyPair([]byte(caPEM), []byte(caKeyPEM))
	require.NoError(t, err)

	serverPEM, serverKeyPEM, err := tlsutil.GenerateServerCertificate("principal", ca.Leaf, ca.PrivateKey, nil, []string{"localhost", "127.0.0.1"}, tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: fixture.PrincipalNamespace,
			Name:      hotReloadCASecretName,
		},
		Data: map[string][]byte{
			"tls.crt": []byte(caPEM),
		},
	}

	serverSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: fixture.PrincipalNamespace,
			Name:      hotReloadTLSSecretName,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": []byte(serverPEM),
			"tls.key": []byte(serverKeyPEM),
		},
	}

	require.NoError(t, kubeClient.Create(ctx, caSecret, metav1.CreateOptions{}))
	require.NoError(t, kubeClient.Create(ctx, serverSecret, metav1.CreateOptions{}))

	return caSecret, serverSecret, ca
}

// Test_ReloadServerCertByFilePath tests to make sure that a server cert and key can be reloaded successfully by file path
func (suite *TLSHotReloadTestSuite) Test_ReloadServerCertByFilePath() {
	dir, ca, caPEM, caKeyPEM, _, _ := generateAndWriteInitalCerts(suite.T())

	err := fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH":    filepath.Join(dir, hotReloadServerCertFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH":     filepath.Join(dir, hotReloadServerKeyFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH": filepath.Join(dir, hotReloadCACertFileName),
	})
	require.NoError(suite.T(), err)

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	createNewServerCert(suite.T(), ca, caPEM, caKeyPEM, dir)

	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, fileProviderExpectedSuccessLogs)
}

// Test_ReloadCAByFilePath tests to make sure that a server ca can be reloaded successfully by file path
func (suite *TLSHotReloadTestSuite) Test_ReloadCAByFilePath() {
	dir, _, _, _, serverPEM, serverKeyPEM := generateAndWriteInitalCerts(suite.T())

	err := fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH":    filepath.Join(dir, hotReloadServerCertFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH":     filepath.Join(dir, hotReloadServerKeyFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH": filepath.Join(dir, hotReloadCACertFileName),
	})
	require.NoError(suite.T(), err)

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	createNewCACert(suite.T(), serverPEM, serverKeyPEM, dir)

	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, fileProviderExpectedSuccessLogs)
}

// Test_ReloadServerCertBySecret tests to make sure that a server cert can be reloaded successfully be secret
func (suite *TLSHotReloadTestSuite) Test_ReloadServerCertBySecret() {
	caSecret, serverSecret, ca := createTestSecrets(suite.T(), suite.Ctx, suite.PrincipalClient)

	err := fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_TLS_SECRET_NAME":     hotReloadTLSSecretName,
		"ARGOCD_PRINCIPAL_ROOT_CA_SECRET_NAME": hotReloadCASecretName,
	})
	require.NoError(suite.T(), err)

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	newServerCertPEM, newServerCertKeyPEM, err := tlsutil.GenerateServerCertificate("new-server", ca.Leaf, ca.PrivateKey, nil, []string{"localhost", "127.0.0.1"},
		tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	serverSecret.Data["tls.crt"] = []byte(newServerCertPEM)
	serverSecret.Data["tls.key"] = []byte(newServerCertKeyPEM)
	suite.PrincipalClient.Update(suite.Ctx, serverSecret, metav1.UpdateOptions{})

	// Cleanup Secrets created
	suite.PrincipalClient.Delete(suite.Ctx, caSecret, metav1.DeleteOptions{})
	suite.PrincipalClient.Delete(suite.Ctx, serverSecret, metav1.DeleteOptions{})
}

// Test_ReloadCABySecret tests to make sure that a ca cert can be reloaded successfully by secret
func (suite *TLSHotReloadTestSuite) Test_ReloadCABySecret() {
	caSecret, serverSecret, _ := createTestSecrets(suite.T(), suite.Ctx, suite.PrincipalClient)

	err := fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_TLS_SECRET_NAME":     hotReloadTLSSecretName,
		"ARGOCD_PRINCIPAL_ROOT_CA_SECRET_NAME": hotReloadCASecretName,
	})
	require.NoError(suite.T(), err)

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	newCAPEM, _, err := tlsutil.GenerateCaCertificate("new-test-ca", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(suite.T(), err)
	caSecret.Data["tls.crt"] = []byte(newCAPEM)
	suite.PrincipalClient.Update(suite.Ctx, caSecret, metav1.UpdateOptions{})

	// Cleanup Secrets created
	suite.PrincipalClient.Delete(suite.Ctx, caSecret, metav1.DeleteOptions{})
	suite.PrincipalClient.Delete(suite.Ctx, serverSecret, metav1.DeleteOptions{})
}

// Test_PrincipalFailsOnBadReload reloads the cert data with a bad cert and ensures that the principal terminates on a bad reload
func (suite *TLSHotReloadTestSuite) Test_PrincipalFailsOnBadReload() {
	dir, _, caPEM, caKeyPEM, _, _ := generateAndWriteInitalCerts(suite.T())

	err := fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_PRINCIPAL_TLS_SERVER_CERT_PATH":    filepath.Join(dir, hotReloadServerCertFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_KEY_PATH":     filepath.Join(dir, hotReloadServerKeyFileName),
		"ARGOCD_PRINCIPAL_TLS_SERVER_ROOT_CA_PATH": filepath.Join(dir, hotReloadCACertFileName),
	})
	require.NoError(suite.T(), err)

	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)

	// Make invalid by writing CA to server cert file
	newDir := filepath.Join(dir, "invalid")
	writeCertsToDir(suite.T(), newDir, caPEM, caKeyPEM, caPEM, caKeyPEM)
	swapSymlink(suite.T(), dir, newDir)

	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, fileProviderExpectedFailLogs)
}
