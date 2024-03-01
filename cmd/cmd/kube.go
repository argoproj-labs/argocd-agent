package cmd

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jannfis/argocd-agent/internal/kube"
)

func GetKubeConfig(ctx context.Context, namespace string, kubeConfig string, kubecontext string) (*kube.KubernetesClient, error) {
	var fullKubeConfigPath string
	var kubeClient *kube.KubernetesClient
	var err error

	if kubeConfig != "" {
		fullKubeConfigPath, err = filepath.Abs(kubeConfig)
		if err != nil {
			return nil, fmt.Errorf("cannot expand path %s: %v", kubeConfig, err)
		}
	}

	kubeClient, err = kube.NewKubernetesClientFromConfig(ctx, namespace, fullKubeConfigPath, kubecontext)
	if err != nil {
		return nil, err
	}

	return kubeClient, nil
}
