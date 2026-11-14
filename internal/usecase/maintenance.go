package usecase

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

type maintenanceRepository interface {
	List(ctx context.Context) ([]domain.MaintenanceWindow, error)
	GetServer(ctx context.Context) (*domain.MaintenanceWindow, error)
	GetOrg(ctx context.Context, orgID snow.ID) (*domain.MaintenanceWindow, error)
	GetProject(ctx context.Context, projectID snow.ID) (*domain.MaintenanceWindow, error)
	UpsertServer(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	UpsertOrg(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	UpsertProject(ctx context.Context, w domain.MaintenanceWindow) (*domain.MaintenanceWindow, error)
	Delete(ctx context.Context, id snow.ID) (bool, error)
	RecipientsServer(ctx context.Context) ([]domain.MaintenanceRecipient, error)
	RecipientsOrg(ctx context.Context, orgID snow.ID) ([]domain.MaintenanceRecipient, error)
	RecipientsProject(ctx context.Context, projectID snow.ID) ([]domain.MaintenanceRecipient, error)
}
