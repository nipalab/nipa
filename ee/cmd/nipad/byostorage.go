package main

import (
	"database/sql"
	"errors"
	"log/slog"

	"github.com/emicklei/go-restful/v3"

	"github.com/nipalab/nipa/ee/byostorage"
	"github.com/nipalab/nipa/ee/repository/postgres"
	"github.com/nipalab/nipa/ee/storageusage"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/repository/dbtx"
	"github.com/nipalab/nipa/internal/serverapp"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
)

type byoStorageSetup struct {
	service  *byostorage.Service
	resolver *byostorage.Resolver
}

// buildBYOStorage validates the bring-your-own storage settings and builds
// the service and the per-project store resolver over the server's own
// store. Without BYO_STORAGE_ENCRYPTION_KEY it returns nil.
func buildBYOStorage(cfg *config.Config, dbConn *sql.DB, node snow.Node, defaultStore storage.ChunkStore, ledger *storageusage.Ledger) (*byoStorageSetup, error) {
	if err := cfg.ValidateBYOStorage(); err != nil {
		return nil, err
	}
	key, _ := cfg.BYOStorageKey()
	if key == nil {
		return nil, nil
	}
	if ledger == nil {
		return nil, errors.New("bring-your-own storage requires the storage ledger")
	}
	service, err := byostorage.New(postgres.NewBYOStorageRepository(dbConn), ledger, dbtx.NewTransactor(dbConn), node, byostorage.Config{
		Key:          key,
		AllowPrivate: cfg.BYOStorageAllowPrivate,
	})
	if err != nil {
		return nil, err
	}
	if cfg.BYOStorageAllowPrivate {
		slog.Warn("BYO_STORAGE_ALLOW_PRIVATE lets storage servers reach private and loopback addresses; use it for tests only")
	}
	slog.Info("bring-your-own storage enabled")
	return &byoStorageSetup{service: service, resolver: service.Resolver(defaultStore)}, nil
}

// entitle gates writes to own storage on the billing Own storage add-on.
// Without billing the add-on is never checked, so BYO storage stays open.
func (b *byoStorageSetup) entitle(billingSetup *billingSetup) {
	if b == nil {
		return
	}
	if e := ownStorageEntitlement(billingSetup); e != nil {
		b.service.WithEntitlement(e)
	}
}

func ownStorageEntitlement(billingSetup *billingSetup) byostorage.Entitlement {
	if billingSetup == nil {
		return nil
	}
	if !billingSetup.ownStorage {
		slog.Warn("billing is on but the Own storage add-on has no prices; storage servers stay read-only for everyone")
	}
	return billingSetup.service
}

// wire makes the resolver pick each project's store and the service assign
// new projects.
func (b *byoStorageSetup) wire(deps *serverapp.UsecaseDeps) {
	if b == nil {
		return
	}
	deps.ChunkStores = b.resolver
	deps.ProjectStorage = b.service
}

// runOptions attaches the usecases built after wiring and registers the
// storage server routes.
func (b *byoStorageSetup) runOptions(uc serverapp.Usecases) []serverapp.Option {
	if b == nil {
		return nil
	}
	b.service.WithAudit(uc.Audit).WithOrgs(uc.Org)
	routes := byostorage.RoutesConfig{Service: b.service, Slugs: uc.Common, Access: uc.Permission}
	return []serverapp.Option{serverapp.WithRoutes(func(ws *restful.WebService) {
		byostorage.RegisterRoutes(ws, routes)
	})}
}

func (b *byoStorageSetup) enabled() bool {
	return b != nil
}
