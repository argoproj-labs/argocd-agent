package cluster

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

/*
The ClusterToSecret function has been copied from Argo CD util/db/cluster.go
and was slightly modified. It is a package-private function in Argo CD, so
we are not able to just use it from the package.

TODO(jannfis): Submit PR to Argo CD to make this function public, so we can
just use it directly from github.com/argoproj/argo-cd/v3 package.
*/

// ClusterToSecret converts a cluster object to string data for serialization to a secret
func ClusterToSecret(c *v1alpha1.Cluster, secret *v1.Secret) error {
	data := make(map[string][]byte)
	data["server"] = []byte(strings.TrimRight(c.Server, "/"))
	if c.Name == "" {
		data["name"] = []byte(c.Server)
	} else {
		data["name"] = []byte(c.Name)
	}
	if len(c.Namespaces) != 0 {
		data["namespaces"] = []byte(strings.Join(c.Namespaces, ","))
	}
	configBytes, err := json.Marshal(c.Config)
	if err != nil {
		return err
	}
	data["config"] = configBytes
	if c.Shard != nil {
		data["shard"] = []byte(strconv.Itoa(int(*c.Shard)))
	}
	if c.ClusterResources {
		data["clusterResources"] = []byte("true")
	}
	if c.Project != "" {
		data["project"] = []byte(c.Project)
	}
	secret.Data = data

	secret.Labels = c.Labels
	if c.Annotations != nil && c.Annotations[v1.LastAppliedConfigAnnotation] != "" {
		return fmt.Errorf("annotation %s cannot be set", v1.LastAppliedConfigAnnotation)
	}
	secret.Annotations = c.Annotations

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	if c.RefreshRequestedAt != nil {
		secret.Annotations[v1alpha1.AnnotationKeyRefresh] = c.RefreshRequestedAt.Format(time.RFC3339)
	} else {
		delete(secret.Annotations, v1alpha1.AnnotationKeyRefresh)
	}

	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[common.AnnotationKeyManagedBy] = LabelValueManagerName

	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[common.LabelKeySecretType] = common.LabelValueSecretTypeCluster

	return nil
}
