package server

import (
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	User() *usecase.User
	Branch() *usecase.Branch
	Common() *usecase.Common
	Push() *usecase.Push
	Chunk() *usecase.Chunk
	Permission() *usecase.Permission
	Group() *usecase.Group
	MergeRequest() *usecase.MergeRequest
	FileLock() *usecase.FileLock
}

type nipaServer struct {
	pb.UnimplementedNipaServiceServer
	uc usecaseContainer
}

func New(usecaseContainer usecaseContainer) *nipaServer {
	return &nipaServer{uc: usecaseContainer}
}
