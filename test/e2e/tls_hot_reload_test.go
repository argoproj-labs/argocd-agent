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
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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

	swapCertFiles(t, dir, initialDir, principalCertFiles(caPEM, caKeyPEM, serverPEM, serverKeyPEM))
	linkCertFiles(t, dir, hotReloadCACertFileName, hotReloadServerCertFileName, hotReloadServerKeyFileName)

	return dir, ca, caPEM, caKeyPEM, serverPEM, serverKeyPEM
}

// createNewServerCert creates a new server cert and then symlinks it to the ..data directory
func createNewServerCert(t *testing.T, ca tls.Certificate, caPEM, caKeyPEM, dir string) {
	serverPEM, serverKeyPEM, err := tlsutil.GenerateServerCertificate("principal-v2", ca.Leaf, ca.PrivateKey, nil, []string{"localhost", "127.0.0.1"}, tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	swapCertFiles(t, dir, filepath.Join(dir, "new-certs"), principalCertFiles(caPEM, caKeyPEM, serverPEM, serverKeyPEM))
}

// createNewCACert creates a new CA cert and then symlinks it to the ..data directory
func createNewCACert(t *testing.T, serverPEM, serverKeyPEM, dir string) {
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("test-ca", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	require.NoError(t, err)

	swapCertFiles(t, dir, filepath.Join(dir, "new-ca"), principalCertFiles(caPEM, caKeyPEM, serverPEM, serverKeyPEM))
}

// principalCertFiles returns the principal's CA and server certificate files, keyed by file name
func principalCertFiles(caPEM, caKeyPEM, serverPEM, serverKeyPEM string) map[string]string {
	return map[string]string{
		hotReloadCACertFileName:     caPEM,
		hotReloadCAKeyFileName:      caKeyPEM,
		hotReloadServerCertFileName: serverPEM,
		hotReloadServerKeyFileName:  serverKeyPEM,
	}
}

// writeCertsToDir writes each file, keyed by file name, to a directory
func writeCertsToDir(t *testing.T, dir string, files map[string]string) {
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, data := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
	}
}

// swapCertFiles writes the files to targetDir and then points the ..data symlink in baseDir to it
func swapCertFiles(t *testing.T, baseDir, targetDir string, files map[string]string) {
	writeCertsToDir(t, targetDir, files)
	swapSymlink(t, baseDir, targetDir)
}

// linkCertFiles symlinks each file name in baseDir to the same name in the ..data directory
func linkCertFiles(t *testing.T, baseDir string, names ...string) {
	for _, name := range names {
		require.NoError(t, os.Symlink(filepath.Join(symlinkDir, name), filepath.Join(baseDir, name)))
	}
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
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())

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
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())

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

const (
	agentClientTLSSecretName = "argocd-agent-client-tls"
	agentCASecretName        = "argocd-agent-ca"
)

// Test_AgentUsesRenewedClientCertificateWithoutRestart updates the agent client
// certificate Secret, checks that the running agent loads it, and checks that
// the agent authenticates with it after a principal restart.
func (suite *TLSHotReloadTestSuite) Test_AgentUsesRenewedClientCertificateWithoutRestart() {
	// The agent process logs and status are only available when the agent runs locally.
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
	requires := suite.Require()
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: agentClientTLSSecretName, Namespace: fixture.AutonomousAgentNamespace}
	requires.NoError(suite.AutonomousAgentClient.Get(suite.Ctx, key, secret, metav1.GetOptions{}))
	current := parsePEMCertificate(suite.T(), secret.Data["tls.crt"])

	// The agent logs the fingerprint of each client certificate it loads.
	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, []string{"certFingerprint=" + tlsutil.CertificateFingerprint(current)})

	newCert, newKey := suite.newAgentClientCertificate()
	renewed := parsePEMCertificate(suite.T(), []byte(newCert))
	restore := suite.updateAgentSecret(agentClientTLSSecretName, map[string][]byte{"tls.crt": []byte(newCert), "tls.key": []byte(newKey)})
	defer restore()

	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, []string{"certFingerprint=" + tlsutil.CertificateFingerprint(renewed)})
	requires.True(fixture.IsProcessRunning(fixture.AgentAutonomousName, suite.T()))

	// The principal logs the fingerprint of the client certificate that authenticated.
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, []string{"fingerprint=" + tlsutil.CertificateFingerprint(renewed)})
	requires.True(fixture.IsProcessRunning(fixture.AgentAutonomousName, suite.T()))
}

// Test_AgentExitsOnInvalidClientCertificate writes a CA certificate to the
// agent client certificate Secret and checks that the agent exits.
func (suite *TLSHotReloadTestSuite) Test_AgentExitsOnInvalidClientCertificate() {
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("not-a-client", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	suite.Require().NoError(err)

	restore := suite.updateAgentSecret(agentClientTLSSecretName, map[string][]byte{"tls.crt": []byte(caPEM), "tls.key": []byte(caKeyPEM)})
	defer restore()

	suite.requireAgentExit("Failed to change certificates", "certificate provided is a CA")
}

// Test_AgentExitsOnInvalidRootCA writes invalid data to the agent CA Secret and
// checks that the agent exits.
func (suite *TLSHotReloadTestSuite) Test_AgentExitsOnInvalidRootCA() {
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())

	restore := suite.updateAgentSecret(agentCASecretName, map[string][]byte{"ca.crt": []byte("not a certificate")})
	defer restore()

	suite.requireAgentExit("Failed to change certificates", "no certs were found under correct keys")
}

// Test_AgentUsesSwappedClientCertificateFileWithoutRestart swaps the agent's
// file-based client certificate, checks that the running agent loads it, and
// checks that the agent authenticates with it after a principal restart.
func (suite *TLSHotReloadTestSuite) Test_AgentUsesSwappedClientCertificateFileWithoutRestart() {
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
	dir, files := suite.startAgentWithCertFiles()

	newCert, newKey := suite.newAgentClientCertificate()
	renewed := parsePEMCertificate(suite.T(), []byte(newCert))
	files[hotReloadServerCertFileName] = newCert
	files[hotReloadServerKeyFileName] = newKey
	swapCertFiles(suite.T(), dir, filepath.Join(dir, "renewed"), files)

	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, []string{"certFingerprint=" + tlsutil.CertificateFingerprint(renewed)})
	suite.Require().True(fixture.IsProcessRunning(fixture.AgentAutonomousName, suite.T()))

	// The principal logs the fingerprint of the client certificate that authenticated.
	fixture.RestartAgent(suite.T(), fixture.PrincipalName)
	fixture.CheckReadiness(suite.T(), fixture.PrincipalName)
	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, []string{"fingerprint=" + tlsutil.CertificateFingerprint(renewed)})
	suite.Require().True(fixture.IsProcessRunning(fixture.AgentAutonomousName, suite.T()))
}

// Test_AgentExitsOnInvalidClientCertificateFile swaps a CA certificate into the
// agent's client certificate file and checks that the agent exits.
func (suite *TLSHotReloadTestSuite) Test_AgentExitsOnInvalidClientCertificateFile() {
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
	dir, files := suite.startAgentWithCertFiles()

	caPEM, caKeyPEM, err := tlsutil.GenerateCaCertificate("not-a-client", tlsutil.DefaultCACertValidityDays, tlsutil.KeyGenOptions{})
	suite.Require().NoError(err)
	files[hotReloadServerCertFileName] = caPEM
	files[hotReloadServerKeyFileName] = caKeyPEM
	swapCertFiles(suite.T(), dir, filepath.Join(dir, "invalid"), files)

	suite.requireAgentExit("TLS hot reload watch failed", "failed to validate client cert")
}

// Test_AgentExitsOnInvalidRootCAFile swaps invalid data into the agent's root CA
// file and checks that the agent exits.
func (suite *TLSHotReloadTestSuite) Test_AgentExitsOnInvalidRootCAFile() {
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())
	dir, files := suite.startAgentWithCertFiles()

	files[hotReloadCACertFileName] = "not a certificate"
	swapCertFiles(suite.T(), dir, filepath.Join(dir, "invalid"), files)

	suite.requireAgentExit("TLS hot reload watch failed", "validation failed on reloading ca pool")
}

// startAgentWithCertFiles writes a new client certificate and the agent's root
// CA to a Kubernetes style mounted directory, restarts the autonomous agent to
// load them from files, and returns the directory and the files it holds.
// TearDownTest clears the file paths and restarts the agent with its Secrets.
func (suite *TLSHotReloadTestSuite) startAgentWithCertFiles() (string, map[string]string) {
	requires := suite.Require()
	caSecret := &corev1.Secret{}
	caKey := types.NamespacedName{Name: agentCASecretName, Namespace: fixture.AutonomousAgentNamespace}
	requires.NoError(suite.AutonomousAgentClient.Get(suite.Ctx, caKey, caSecret, metav1.GetOptions{}))
	certPEM, keyPEM := suite.newAgentClientCertificate()

	dir := suite.T().TempDir()
	files := map[string]string{
		hotReloadCACertFileName:     string(caSecret.Data["ca.crt"]),
		hotReloadServerCertFileName: certPEM,
		hotReloadServerKeyFileName:  keyPEM,
	}
	swapCertFiles(suite.T(), dir, filepath.Join(dir, "initial"), files)
	linkCertFiles(suite.T(), dir, hotReloadCACertFileName, hotReloadServerCertFileName, hotReloadServerKeyFileName)

	certPath := filepath.Join(dir, hotReloadServerCertFileName)
	requires.NoError(fixture.WriteEnvVarsToFile(map[string]string{
		"ARGOCD_AUTONOMOUS_AGENT_TLS_CLIENT_CERT_PATH": certPath,
		"ARGOCD_AUTONOMOUS_AGENT_TLS_CLIENT_KEY_PATH":  filepath.Join(dir, hotReloadServerKeyFileName),
		"ARGOCD_AUTONOMOUS_AGENT_TLS_ROOT_CA_PATH":     filepath.Join(dir, hotReloadCACertFileName),
	}))
	fixture.RestartAgent(suite.T(), fixture.AgentAutonomousName)
	fixture.CheckReadiness(suite.T(), fixture.AgentAutonomousName)

	cert := parsePEMCertificate(suite.T(), []byte(certPEM))
	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, []string{
		"Loading client TLS configuration from files cert=" + certPath,
		"certFingerprint=" + tlsutil.CertificateFingerprint(cert),
	})

	return dir, files
}

// newAgentClientCertificate returns a new client certificate for the
// autonomous agent's identity, signed by the principal CA.
func (suite *TLSHotReloadTestSuite) newAgentClientCertificate() (string, string) {
	requires := suite.Require()
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: agentClientTLSSecretName, Namespace: fixture.AutonomousAgentNamespace}
	requires.NoError(suite.AutonomousAgentClient.Get(suite.Ctx, key, secret, metav1.GetOptions{}))
	current := parsePEMCertificate(suite.T(), secret.Data["tls.crt"])

	caSecret := &corev1.Secret{}
	caKey := types.NamespacedName{Name: agentCASecretName, Namespace: fixture.PrincipalNamespace}
	requires.NoError(suite.PrincipalClient.Get(suite.Ctx, caKey, caSecret, metav1.GetOptions{}))
	ca, err := tls.X509KeyPair(caSecret.Data["tls.crt"], caSecret.Data["tls.key"])
	requires.NoError(err)
	ca.Leaf, err = x509.ParseCertificate(ca.Certificate[0])
	requires.NoError(err)

	certPEM, keyPEM, err := tlsutil.GenerateClientCertificate(current.Subject.CommonName, ca.Leaf, ca.PrivateKey, tlsutil.DefaultLeafCertValidityDays, tlsutil.KeyGenOptions{})
	requires.NoError(err)
	return certPEM, keyPEM
}

// updateAgentSecret sets data keys in a Secret of the autonomous agent and
// returns a function that restores the original data. Call it before the
// test ends: TearDownTest restarts the agent, which needs valid material.
func (suite *TLSHotReloadTestSuite) updateAgentSecret(name string, data map[string][]byte) func() {
	key := types.NamespacedName{Name: name, Namespace: fixture.AutonomousAgentNamespace}
	secret := &corev1.Secret{}
	suite.Require().NoError(suite.AutonomousAgentClient.Get(suite.Ctx, key, secret, metav1.GetOptions{}))
	original := secret.DeepCopy().Data
	for k, v := range data {
		secret.Data[k] = v
	}
	suite.Require().NoError(suite.AutonomousAgentClient.Update(suite.Ctx, secret, metav1.UpdateOptions{}))

	return func() {
		current := &corev1.Secret{}
		if err := suite.AutonomousAgentClient.Get(suite.Ctx, key, current, metav1.GetOptions{}); err != nil {
			suite.T().Logf("Could not restore Secret %s: %v", key, err)
			return
		}
		current.Data = original
		if err := suite.AutonomousAgentClient.Update(suite.Ctx, current, metav1.UpdateOptions{}); err != nil {
			suite.T().Logf("Could not restore Secret %s: %v", key, err)
		}
	}
}

// requireAgentExit checks that the autonomous agent logged the fatal reload
// message with the given reason and that its process stopped.
func (suite *TLSHotReloadTestSuite) requireAgentExit(message, reason string) {
	fixture.VerifyProcessLogs(suite.T(), fixture.AgentAutonomousName, []string{message, reason})
	suite.Require().Eventually(func() bool {
		return !fixture.IsProcessRunning(fixture.AgentAutonomousName, suite.T())
	}, 60*time.Second, 2*time.Second, "the agent should exit on invalid TLS material")
}

func parsePEMCertificate(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert
}

// Test_PrincipalFailsOnBadReload reloads the cert data with a bad cert and ensures that the principal terminates on a bad reload
func (suite *TLSHotReloadTestSuite) Test_PrincipalFailsOnBadReload() {
	// Skip if running in cluster because we need to be able to read/write to file paths
	fixture.SkipIfAgentInClusterEnvVarIsSet(suite.T())

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
	swapCertFiles(suite.T(), dir, filepath.Join(dir, "invalid"), principalCertFiles(caPEM, caKeyPEM, caPEM, caKeyPEM))

	fixture.VerifyProcessLogs(suite.T(), fixture.PrincipalName, fileProviderExpectedFailLogs)
}
