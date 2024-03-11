package informer

import "context"

type Informer interface {
	Start(ctx context.Context)
}
