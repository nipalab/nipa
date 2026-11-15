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
		return handler(srv, ss)
	}
}
