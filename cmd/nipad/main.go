package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nipalab/nipa/db"
	"github.com/nipalab/nipa/internal/config"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/grpc/server"
	"github.com/nipalab/nipa/internal/hasher"
	"github.com/nipalab/nipa/internal/http/api"
	"github.com/nipalab/nipa/internal/repository/sqlite"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/storage"
	"github.com/nipalab/nipa/internal/usecase"
	webui "github.com/nipalab/nipa/web/server"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"
)

func main() {
	cfg, err := config.LoadConfig()
	if err != nil {
		panic(err)
	}

	dbConn, err := createDatabaseConnection(cfg.DatabaseDSN)
	if err != nil {
		panic(err)
	}

	slog.Info("migrating database...")
	err = db.MigrateUp(dbConn, "sqlite3")
	if err != nil {
		panic(err)
	}
	slog.Info("database migration completed")

	orgRepo := sqlite.NewOrgRepository(dbConn)
	projectRepo := sqlite.NewProjectRepository(dbConn)
	authRepo := sqlite.NewAuthRepository(dbConn)
	userRepo := sqlite.NewUserRepository(dbConn)
	branchRepository := sqlite.NewBranchRepository(dbConn)
	pushRepository := sqlite.NewPushRepository(dbConn)
	pbacRepository := sqlite.NewPBACRepository(dbConn)
	groupRepository := sqlite.NewGroupRepository(dbConn)

	passwordHasher := hasher.NewHasher(cfg.HasherWorkers)

	snowUser, err := snow.NewNode(cfg.SnowflakeNodeID)
	if err != nil {
		panic(err)
	}
	authUsecase := usecase.NewAuth(cfg.JWTKey, passwordHasher, userRepo, authRepo)
	orgUsecase := usecase.NewOrg(orgRepo)
	permissionUsecase := usecase.NewPermission(pbacRepository, userRepo, groupRepository, orgUsecase)
	projectUsecase := usecase.NewProject(projectRepo, snowUser, permissionUsecase, orgUsecase)
	chunkStore, err := storage.NewLocalStore(cfg.ChunkStorageDir)
	if err != nil {
		panic(fmt.Errorf("create chunk store: %w", err))
	}
	defer chunkStore.Close()
	if cfg.ChunkURLSigningKey == "" {
		panic("CHUNK_URL_SIGNING_KEY must be set")
	}
	branchUsecase := usecase.NewBranchWithChunks(permissionUsecase, branchRepository, snowUser, chunkStore)
	fileLockUsecase := usecase.NewFileLock(sqlite.NewFileLockRepository(dbConn), branchRepository, permissionUsecase, snowUser)
	branchUsecase = branchUsecase.WithFileLocks(fileLockUsecase)
	mergeRequestUsecase := usecase.NewMergeRequest(
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepository,
		permissionUsecase,
		branchUsecase,
		snowUser,
	).WithFileLocks(fileLockUsecase)
	pushUsecase := usecase.NewPush(permissionUsecase, branchRepository, pushRepository, snowUser).WithFileLocks(fileLockUsecase)
	mergeRequestReviewUsecase := usecase.NewMergeRequestReview(
		sqlite.NewMergeRequestReviewRepository(dbConn),
		sqlite.NewMergeRequestRepository(dbConn),
		branchRepository,
		branchUsecase,
		permissionUsecase,
		userRepo,
		snowUser,
	)
	pushUsecase = pushUsecase.WithReviews(mergeRequestReviewUsecase)
	chunkUsecase := usecase.NewChunk(pushRepository, chunkStore, usecase.ChunkTransferConfig{
		SigningKey:  cfg.ChunkURLSigningKey,
		PresignTTL:  time.Duration(cfg.ChunkPresignTTLSeconds) * time.Second,
		MaxPageSize: cfg.ChunkMaxPageSize,
	})
	reg := &Registry{
		authUsecase:               authUsecase,
		userUsecase:               usecase.NewUser(snowUser, userRepo, passwordHasher),
		commonUsecase:             usecase.NewCommon(orgRepo, projectRepo),
		branchUsecase:             branchUsecase,
		pushUsecase:               pushUsecase,
		chunkUsecase:              chunkUsecase,
		permissionUsecase:         permissionUsecase,
		groupUsecase:              usecase.NewGroup(groupRepository, snowUser, permissionUsecase, orgUsecase),
		orgUsecase:                orgUsecase,
		projectUsecase:            projectUsecase,
		mergeRequestUsecase:       mergeRequestUsecase,
		mergeRequestReviewUsecase: mergeRequestReviewUsecase,
		fileLockUsecase:           fileLockUsecase,
	}

	apiApp := api.NewAPI(reg)
	container := apiApp.SetupRoute()

	address := fmt.Sprintf("%s:%d", cfg.ServerAddress, cfg.ServerPort)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	grpcInterceptor := server.NewInterceptor(reg.Auth())

	grpcRegistrar := grpc.NewServer(
		grpc.UnaryInterceptor(grpcInterceptor.JWTUnary()),
		grpc.StreamInterceptor(grpcInterceptor.JWTStream()),
	)
	grpcServer := server.New(reg)
	pb.RegisterNipaServiceServer(grpcRegistrar, grpcServer)

	webUI := webui.Handler()

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("incoming connection", "content", r.Header.Get("Content-Type"), "method", r.Method, "url", r.URL.String(), "ProtoMajor", r.ProtoMajor)
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			slog.Info("grpc connection is coming")
			grpcRegistrar.ServeHTTP(w, r)
		} else if isAPIPath(r.URL.Path) {
			container.ServeHTTP(w, r)
		} else {
			webUI.ServeHTTP(w, r)
		}
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	httpServer := &http.Server{Addr: address, Handler: mainHandler, Protocols: protocols}
	ln, err := net.Listen("tcp", address)
	if err != nil {
		panic(err)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		<-c
		slog.Info("shutting down server")
		if err := httpServer.Shutdown(ctx); err != nil {
			slog.Error("error shutting down server", "error", err)
		}
		os.Exit(0)
	}()

	slog.Info("server is running", "address", address)
	if err := httpServer.Serve(ln); err != http.ErrServerClosed {
		panic(err)
	}
}

func createDatabaseConnection(dsn string) (*sql.DB, error) {
	return db.OpenSQLite(dsn)
}

func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/auth") ||
		strings.HasPrefix(p, "/docs") ||
		strings.HasPrefix(p, "/api")
}
