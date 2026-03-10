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

// AppLabelSelector returns a ListOptions combining the default label selector
// with an additional user-provided selector. The two selectors are AND-ed
// together (comma-separated in Kubernetes label selector syntax).
func AppLabelSelector(extra string) v1.ListOptions {
	base := fmt.Sprintf("%s!=%s", SkipSyncLabel, "true")
	if extra != "" {
		base = base + "," + extra
	}

	return v1.ListOptions{
		LabelSelector: base,
	}
}
