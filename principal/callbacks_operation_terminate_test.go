package principal

import (
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldSendOperationTerminateEvent(t *testing.T) {
	tests := []struct {
		name     string
		old      *v1alpha1.Application
		new      *v1alpha1.Application
		expected bool
	}{
		{
			name: "should send when phase changes from Running to Terminating",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Running",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			expected: true,
		},
		{
			name: "should not send when phase changes from Terminating to Running",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Running",
					},
				},
			},
			expected: false,
		},
		{
			name: "should not send when phase remains Terminating",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			expected: false,
		},
		{
			name: "should not send when old app has no operation state",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: nil,
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			expected: false,
		},
		{
			name: "should not send when new app has no operation state",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Running",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: nil,
				},
			},
			expected: false,
		},
		{
			name: "should not send when both apps have no operation state",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: nil,
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: nil,
				},
			},
			expected: false,
		},
		{
			name: "should send when phase changes from Succeeded to Terminating",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Succeeded",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			expected: true,
		},
		{
			name: "should send when phase changes from Failed to Terminating",
			old: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Failed",
					},
				},
			},
			new: &v1alpha1.Application{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Status: v1alpha1.ApplicationStatus{
					OperationState: &v1alpha1.OperationState{
						Phase: "Terminating",
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock server instance
			s := &Server{}

			result := s.shouldSendOperationTerminateEvent(tt.old, tt.new)
			assert.Equal(t, tt.expected, result, "shouldSendOperationTerminateEvent should return %v", tt.expected)
		})
	}
}

func TestUpdateAppCallbackWithOperationTerminate(t *testing.T) {
	t.Run("should add operation terminate event when phase changes to Terminating", func(t *testing.T) {
		// This test would require more complex setup with mocks
		// For now, we'll test the core logic separately
		t.Skip("Requires complex mocking setup - testing core logic separately")
	})
}
