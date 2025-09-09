package config

import (
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SkipSyncSelector returns a ListOptions that excludes resources with the skip sync label
func SkipSyncSelector() v1.ListOptions {
	return v1.ListOptions{
		LabelSelector: fmt.Sprintf("%s!=%s", SkipSyncLabel, "true"),
	}
}
