package e2e

import (
	"testing"

	"github.com/argoproj-labs/argocd-agent/test/e2e/fixture"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApplicationTestSuite is a test suite for application management via the Argo CD API.
type ApplicationTestSuite struct {
	fixture.BaseSuite
	argoClient *fixture.ArgoRestClient
}

// SetupSuite runs before the tests in the suite are run.
func (s *ApplicationTestSuite) SetupSuite() {
	// First, run the base suite setup
	s.BaseSuite.SetupSuite()

	// Now, set up the Argo CD REST client
	requires := s.Require()
	endpoint, err := fixture.GetArgoCDServerEndpoint(s.PrincipalClient)
	requires.NoError(err)

	password, err := fixture.GetInitialAdminSecret(s.PrincipalClient)
	requires.NoError(err)

	s.argoClient = fixture.NewArgoClient(endpoint, "admin", password)
	err = s.argoClient.Login()
	requires.NoError(err)
}

// TestApplicationManagementAPI is the core test function for this suite.
func (s *ApplicationTestSuite) Test_ApplicationManagementAPI() {
	requires := s.Require()
	asserts := assert.New(s.T())

	// Define a new application
	appName := "guestbook-api-test"
	app := &v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName,
			Namespace: "argocd", // Argo CD API creates applications in the argocd namespace
		},
		Spec: v1alpha1.ApplicationSpec{
			Project: "default",
			Source: &v1alpha1.ApplicationSource{
				RepoURL:        "https://github.com/argoproj/argocd-example-apps.git",
				Path:           "guestbook",
				TargetRevision: "HEAD",
			},
			Destination: v1alpha1.ApplicationDestination{
				Server:    "https://kubernetes.default.svc",
				Namespace: "default",
			},
		},
	}

	// 1. Create Application
	createdApp, err := s.argoClient.CreateApplication(app)
	requires.NoError(err)
	asserts.Equal(appName, createdApp.Name)

	// 2. Get Application and verify
	gotApp, err := s.argoClient.GetApplication(appName)
	requires.NoError(err)
	asserts.Equal(appName, gotApp.Name)
	asserts.Equal("default", gotApp.Spec.Project)

	// 3. List Applications and verify
	appList, err := s.argoClient.ListApplications()
	requires.NoError(err)

	found := false
	for _, item := range appList.Items {
		if item.Name == appName {
			found = true
			break
		}
	}
	asserts.True(found, "application %s not found in list", appName)

	// 4. Delete Application
	err = s.argoClient.DeleteApplication(appName)
	requires.NoError(err)

}

// TestApplicationSuite runs the entire test suite.
func TestApplicationSuite(t *testing.T) {
	suite.Run(t, new(ApplicationTestSuite))
}
