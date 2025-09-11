// Copyright 2025 The argocd-agent Authors
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

package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDefaultLabelSelector(t *testing.T) {
	selector := DefaultLabelSelector()

	// The selector should exclude resources with the skip sync label set to "true"
	expectedLabelSelector := fmt.Sprintf("%s!=true", SkipSyncLabel)

	assert.Equal(t, expectedLabelSelector, selector.LabelSelector,
		"DefaultLabelSelector should create a label selector that excludes resources with skip sync label set to true")

	// Verify it's a proper ListOptions
	assert.IsType(t, v1.ListOptions{}, selector,
		"DefaultLabelSelector should return a v1.ListOptions")
}

func TestDefaultLabelSelector_Integration(t *testing.T) {
	// This test verifies that the selector would work correctly with Kubernetes API
	selector := DefaultLabelSelector()

	// The selector should be non-empty
	assert.NotEmpty(t, selector.LabelSelector,
		"Label selector should not be empty")

	// The selector should not have any other fields set by default
	assert.Empty(t, selector.FieldSelector,
		"Field selector should be empty by default")
	assert.Empty(t, selector.ResourceVersion,
		"Resource version should be empty by default")
	assert.Nil(t, selector.TimeoutSeconds,
		"Timeout seconds should be nil by default")
}
