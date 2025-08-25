package principal

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResourceRequestRegexp(t *testing.T) {
	tests := []struct {
		name               string
		path               string
		shouldMatch        bool
		expectedGroup      string
		expectedVer        string
		expectedNs         string
		expectedRes        string
		expectedName       string
		expectedSubresource string
	}{
		// Current patterns that should work
		{
			name:        "API version endpoint",
			path:        "/api/v1",
			shouldMatch: true,
			expectedVer: "v1",
		},
		{
			name:          "Namespaced resource list",
			path:          "/api/v1/namespaces/default/pods",
			shouldMatch:   true,
			expectedVer:   "v1",
			expectedNs:    "default",
			expectedRes:   "pods",
		},
		{
			name:          "Specific namespaced resource",
			path:          "/api/v1/namespaces/default/pods/my-pod",
			shouldMatch:   true,
			expectedVer:   "v1",
			expectedNs:    "default",
			expectedRes:   "pods",
			expectedName:  "my-pod",
		},
		{
			name:          "Group API resource",
			path:          "/apis/apps/v1/namespaces/default/deployments/my-deployment",
			shouldMatch:   true,
			expectedGroup: "apps",
			expectedVer:   "v1",
			expectedNs:    "default",
			expectedRes:   "deployments",
			expectedName:  "my-deployment",
		},
		{
			name:          "Cluster-scoped resource",
			path:          "/api/v1/nodes/my-node",
			shouldMatch:   true,
			expectedVer:   "v1",
			expectedRes:   "nodes",
			expectedName:  "my-node",
		},
		// Subresource patterns now work with the updated regex
		{
			name:               "Pod status subresource",
			path:               "/api/v1/namespaces/default/pods/my-pod/status",
			shouldMatch:        true, // Now matches with updated regex
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "pods",
			expectedName:       "my-pod",
			expectedSubresource: "status",
		},
		{
			name:               "Pod log subresource",
			path:               "/api/v1/namespaces/default/pods/my-pod/log",
			shouldMatch:        true, // Now matches with updated regex
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "pods",
			expectedName:       "my-pod",
			expectedSubresource: "log",
		},
		{
			name:               "Deployment scale subresource",
			path:               "/apis/apps/v1/namespaces/default/deployments/my-deployment/scale",
			shouldMatch:        true, // Now matches with updated regex
			expectedGroup:      "apps",
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "deployments",
			expectedName:       "my-deployment",
			expectedSubresource: "scale",
		},
		{
			name:               "Service proxy subresource with port",
			path:               "/api/v1/namespaces/default/services/my-service:80/proxy",
			shouldMatch:        true, // Now matches with updated regex
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "services",
			expectedName:       "my-service:80",
			expectedSubresource: "proxy",
		},
		{
			name:               "Node proxy subresource",
			path:               "/api/v1/nodes/my-node/proxy",
			shouldMatch:        true, // Now matches with updated regex
			expectedVer:        "v1",
			expectedRes:        "nodes",
			expectedName:       "my-node",
			expectedSubresource: "proxy",
		},
		{
			name:               "Pod exec subresource",
			path:               "/api/v1/namespaces/default/pods/my-pod/exec",
			shouldMatch:        true,
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "pods",
			expectedName:       "my-pod",
			expectedSubresource: "exec",
		},
		{
			name:               "Pod portforward subresource",
			path:               "/api/v1/namespaces/default/pods/my-pod/portforward",
			shouldMatch:        true,
			expectedVer:        "v1",
			expectedNs:         "default",
			expectedRes:        "pods",
			expectedName:       "my-pod",
			expectedSubresource: "portforward",
		},
		// Negative test cases - patterns that should NOT match
		{
			name:        "Invalid path - no leading slash",
			path:        "api/v1/pods",
			shouldMatch: false,
		},
		{
			name:        "Invalid path - too many subresources",
			path:        "/api/v1/namespaces/default/pods/my-pod/status/extra",
			shouldMatch: false,
		},
		{
			name:        "Invalid path - random string",
			path:        "/random/path/here",
			shouldMatch: false,
		},
		{
			name:        "Empty path",
			path:        "",
			shouldMatch: false,
		},
	}

	re, err := regexp.Compile(resourceRequestRegexp)
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := re.FindStringSubmatch(tt.path)
			if tt.shouldMatch {
				assert.NotNil(t, matches, "Expected path %s to match", tt.path)
				if matches != nil {
					// Extract named groups
					names := re.SubexpNames()
					params := make(map[string]string)
					for i, name := range names {
						if i != 0 && name != "" && i < len(matches) {
							params[name] = matches[i]
						}
					}
					if tt.expectedGroup != "" {
						assert.Equal(t, tt.expectedGroup, params["group"])
					}
					if tt.expectedVer != "" {
						assert.Equal(t, tt.expectedVer, params["version"])
					}
					if tt.expectedNs != "" {
						assert.Equal(t, tt.expectedNs, params["namespace"])
					}
					if tt.expectedRes != "" {
						assert.Equal(t, tt.expectedRes, params["resource"])
					}
					if tt.expectedName != "" {
						assert.Equal(t, tt.expectedName, params["name"])
					}
					if tt.expectedSubresource != "" {
						assert.Equal(t, tt.expectedSubresource, params["subresource"])
					}
				}
			} else {
				assert.Nil(t, matches, "Expected path %s to not match", tt.path)
			}
		})
	}
}

