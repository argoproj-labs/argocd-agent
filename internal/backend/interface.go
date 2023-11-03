/*
Package backend provides the interface for implementing Application providers.
*/
package backend

import (
	"context"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

type ApplicationSelector struct {
	Labels     map[string]string
	Names      []string
	Namespaces []string
	Projects   []string
}

type Application interface {
	List(ctx context.Context, selector ApplicationSelector) ([]v1alpha1.Application, error)
	Create(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error)
	Get(ctx context.Context, name string, namespace string) (*v1alpha1.Application, error)
	Delete(ctx context.Context, name string, namespace string) error
	Update(ctx context.Context, app *v1alpha1.Application) (*v1alpha1.Application, error)
	Patch(ctx context.Context, name string, namespace string, patch []byte) (*v1alpha1.Application, error)
	SupportsPatch() bool
	StartInformer(ctx context.Context)
}
