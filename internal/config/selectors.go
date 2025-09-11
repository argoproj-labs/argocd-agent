package config

import (
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DefaultLabelSelector returns a ListOptions with the default label selector
func DefaultLabelSelector() v1.ListOptions {
	return v1.ListOptions{
		LabelSelector: fmt.Sprintf("%s!=%s", SkipSyncLabel, "true"),
	}
}
