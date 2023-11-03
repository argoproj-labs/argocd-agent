package session

import (
	"context"
	"fmt"

	"github.com/jannfis/argocd-agent/pkg/types"
)

/*
Package session contains various functions to access and manipulate session
data.
*/

func ClientIdFromContext(ctx context.Context) (string, error) {
	val := ctx.Value(types.ContextAgentIdentifier)
	if clientId, ok := val.(string); !ok {
		return "", fmt.Errorf("no client identifier found in context")
	} else {
		return clientId, nil
	}
}
