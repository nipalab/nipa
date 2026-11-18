package server

import (
	"context"

	"google.golang.org/grpc"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
)

// Maintenance refuses calls the effective maintenance window does not allow.
// Chain it after the JWT interceptor and before the rate limiter.
type Maintenance struct {
	svc     *maintenance.Service
	metrics *obs.Metrics
}

func NewMaintenance(svc *maintenance.Service, metrics *obs.Metrics) *Maintenance {
	return &Maintenance{svc: svc, metrics: metrics}
}

type projectContextRequest interface {
	GetContext() *pb.ProjectContext
}

type orgRequest interface {
	GetOrg() string
}

// maintenanceTarget resolves the org and project a request names: its
// ProjectContext, else its org field, else none (server scope only).
func maintenanceTarget(req any) (org, project string) {
	switch r := req.(type) {
	case *pb.PushStreamRequest:
		c := r.GetHeader().GetContext()
		return c.GetOrg(), c.GetProject()
	case *pb.ImportStreamRequest:
		c := r.GetHeader().GetContext()
		return c.GetOrg(), c.GetProject()
	case projectContextRequest:
		c := r.GetContext()
		return c.GetOrg(), c.GetProject()
	case orgRequest:
		return r.GetOrg(), ""
	}
	return "", ""
}

func (m *Maintenance) check(method string, req any) error {
	class := classOf(method)
	org, project := maintenanceTarget(req)
	refused := m.svc.Check(class, "", org, project)
	if refused == nil {
		return nil
	}
	m.metrics.ObserveMaintenanceRejected("grpc", string(class), string(refused.Info.Scope))
	return refused.GRPCError(m.svc.Now())
}

func (m *Maintenance) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := m.check(info.FullMethod, req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (m *Maintenance) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := m.check(info.FullMethod, nil); err != nil {
			return err
		}
		return handler(srv, &maintenanceStream{ServerStream: ss, m: m, method: info.FullMethod})
	}
}

// maintenanceStream checks the first client message of a stream, which names
// its org and project.
type maintenanceStream struct {
	grpc.ServerStream
	m       *Maintenance
	method  string
	checked bool
}

func (s *maintenanceStream) RecvMsg(msg any) error {
	if err := s.ServerStream.RecvMsg(msg); err != nil {
		return err
	}
	if s.checked {
		return nil
	}
	s.checked = true
	return s.m.check(s.method, msg)
}
