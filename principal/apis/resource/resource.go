package resource

import "github.com/argoproj-labs/argocd-agent/pkg/api/grpc/resourceapi"

type Server struct {
	resourceapi.UnimplementedResourceServer
}

func NewServer() *Server {
	return &Server{}
}

func (s *Server) Resource(r *resourceapi.Resource_RequestServer) error {
	return nil
}
