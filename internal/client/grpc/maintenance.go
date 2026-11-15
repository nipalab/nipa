package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
)

const maintenanceHeader = "X-Nipa-Maintenance"

// maintenanceError maps a maintenance refusal to a 503 domain error that
// keeps the gRPC error as its cause.
func maintenanceError(err error) (*domain.Error, bool) {
	refused, ok := maintenance.FromGRPC(err)
	if !ok {
		return nil, false
	}
	return domain.NewMaintenanceError(refused.Info, err), true
}

// httpMaintenanceError reads the REST maintenance refusal: a 503 carrying the
// X-Nipa-Maintenance header and a JSON body with the window.
func httpMaintenanceError(res *http.Response) (*domain.Error, bool) {
	if res.StatusCode != http.StatusServiceUnavailable || res.Header.Get(maintenanceHeader) == "" {
		return nil, false
	}
	var body struct {
		Error       string           `json:"error"`
		Maintenance maintenance.Info `json:"maintenance"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body); err != nil || !body.Maintenance.Mode.Valid() {
		body.Maintenance = headerInfo(res.Header.Get(maintenanceHeader))
	}
	return domain.NewMaintenanceError(body.Maintenance, nil), true
}

func headerInfo(value string) maintenance.Info {
	scope, mode, _ := strings.Cut(value, ";")
	return maintenance.Info{Scope: maintenance.Scope(scope), Mode: maintenance.Mode(mode)}
}

func isDomainError(err error) bool {
	var e *domain.Error
	return errors.As(err, &e)
}

// GetMaintenanceStatus asks the server for its maintenance windows. org and
// project are optional; their windows come back only for a signed-in caller
// with access.
func (t *Transport) GetMaintenanceStatus(ctx context.Context, org, project string) (*domain.MaintenanceStatus, error) {
	client, err := t.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	req := &pb.GetMaintenanceStatusRequest{}
	if org != "" {
		req.Context = &pb.ProjectContext{Org: org, Project: project}
	}
	res, err := client.GetMaintenanceStatus(ctx, req)
	if err != nil {
		return nil, toDomainError(err)
	}
	return &domain.MaintenanceStatus{
		Server:  MaintenanceWindowFromPB(res.GetServer()),
		Org:     MaintenanceWindowFromPB(res.GetOrg()),
		Project: MaintenanceWindowFromPB(res.GetProject()),
		Source:  res.GetSource(),
	}, nil
}

func (c *Client) GetMaintenanceStatus(ctx context.Context, org, project string) (*domain.MaintenanceStatus, error) {
	return c.transport.GetMaintenanceStatus(ctx, org, project)
}

func MaintenanceWindowFromPB(w *pb.MaintenanceWindow) *domain.MaintenanceWindow {
	if w == nil || w.GetMode() == "" {
		return nil
	}
	return &domain.MaintenanceWindow{
		Mode:     w.GetMode(),
		Active:   w.GetActive(),
		Message:  w.GetMessage(),
		StartsAt: timeOf(w.GetStartsAt()),
		EndsAt:   timePtrOf(w.GetEndsAt()),
		Org:      w.GetOrg(),
		Project:  w.GetProject(),
	}
}

func timeOf(ts *timestamppb.Timestamp) (t time.Time) {
	if ts == nil {
		return t
	}
	return ts.AsTime()
}

func timePtrOf(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil {
		return nil
	}
	t := ts.AsTime()
	return &t
}
