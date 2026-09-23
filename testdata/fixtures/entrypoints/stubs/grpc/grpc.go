// Package grpc is a stub of google.golang.org/grpc with the parts
// generated service code uses.
package grpc

type ServiceRegistrar interface {
	RegisterService(desc *ServiceDesc, impl any)
}

type ServiceDesc struct {
	ServiceName string
	HandlerType any
}

type Server struct{ services map[string]any }

func NewServer() *Server { return &Server{services: map[string]any{}} }

func (s *Server) RegisterService(desc *ServiceDesc, impl any) { s.services[desc.ServiceName] = impl }
