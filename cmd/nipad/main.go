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

	passwordHasher := hasher.NewHasher(cfg.HasherWorkers)

	snowUser, err := snow.NewNode(cfg.SnowflakeNodeID)
	if err != nil {
		panic(err)
	}
	authUsecase := usecase.NewAuth(cfg.JWTKey, passwordHasher, userRepo, authRepo)
	chunkStore, err := storage.NewLocalStore(cfg.ChunkStorageDir)
	if err != nil {
		panic(fmt.Errorf("create chunk store: %w", err))
	}
	defer chunkStore.Close()
	reg := &Registry{
		authUsecase:   authUsecase,
		userUsecase:   usecase.NewUser(snowUser),
		commonUsecase: usecase.NewCommon(orgRepo, projectRepo),
		branchUsecase: usecase.NewBranch(authUsecase, branchRepository, snowUser),
		pushUsecase:   usecase.NewPush(authUsecase, branchRepository, pushRepository, snowUser),
		chunkUsecase:  usecase.NewChunk(pushRepository, chunkStore),
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

	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("incoming connection", "content", r.Header.Get("Content-Type"), "method", r.Method, "url", r.URL.String(), "ProtoMajor", r.ProtoMajor)
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			slog.Info("grpc connection is coming")
			grpcRegistrar.ServeHTTP(w, r)
		} else {
			container.ServeHTTP(w, r)
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
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	return db, nil
}
