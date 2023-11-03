package version

import (
	"context"

	"github.com/jannfis/argocd-agent/internal/version"
	"github.com/jannfis/argocd-agent/pkg/api/grpc/versionapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var unauthenticatedMethods map[string]bool = map[string]bool{
	"/versionapi.Version/Version": true,
}

type server struct {
	versionapi.UnimplementedVersionServer
	authfunc func(context.Context) (context.Context, error)
}

func NewServer(authfunc func(context.Context) (context.Context, error)) *server {
	return &server{authfunc: authfunc}
}

func (s *server) Version(ctx context.Context, r *versionapi.VersionRequest) (*versionapi.VersionResponse, error) {
	return &versionapi.VersionResponse{Version: version.QualifiedVersion()}, nil
}

func (s *server) AuthFuncOverride(ctx context.Context, fullMethodName string) (context.Context, error) {
	_, ok := unauthenticatedMethods[fullMethodName]
	if ok {
		return ctx, nil
	}
	if s.authfunc != nil {
		return s.authfunc(ctx)
	}
	return ctx, status.Error(codes.Unauthenticated, "no session")
}
