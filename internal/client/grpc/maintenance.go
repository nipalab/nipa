package grpc

import (
	"errors"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/maintenance"
)

// maintenanceError maps a maintenance refusal to a 503 domain error that
// keeps the gRPC error as its cause.
func maintenanceError(err error) (*domain.Error, bool) {
	refused, ok := maintenance.FromGRPC(err)
	if !ok {
		return nil, false
	}
	info := refused.Info
	return &domain.Error{
		Code:    503,
		Message: refused.Error(),
		Hint:    maintenanceHint(info),
		Cause:   err,
		Maintenance: &domain.MaintenanceInfo{
			Scope:    string(info.Scope),
			Mode:     string(info.Mode),
			Message:  info.Message,
			StartsAt: info.StartsAt,
			EndsAt:   info.EndsAt,
			Org:      info.Org,
			Project:  info.Project,
		},
	}, true
}

func maintenanceHint(info maintenance.Info) string {
	switch info.Mode {
	case maintenance.ReadOnly:
		return "read-only commands still work"
	case maintenance.Frozen:
		return "sign-in is paused; commands that read work while your session lasts"
	default:
		if info.EndsAt != nil {
			return "try again after ~" + info.EndsAt.Local().Format(time.DateTime)
		}
		return "try again later"
	}
}

func isDomainError(err error) bool {
	var e *domain.Error
	return errors.As(err, &e)
}
