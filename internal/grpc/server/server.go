package server

import (
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase"
)

type usecaseContainer interface {
	Auth() *usecase.Auth
	User() *usecase.User
	Branch() *usecase.Branch
	Tag() *usecase.Tag
	Common() *usecase.Common
	Push() *usecase.Push
	Import() *usecase.Import
	Chunk() *usecase.Chunk
	Permission() *usecase.Permission
	Group() *usecase.Group
	MergeRequest() *usecase.MergeRequest
	MergeRequestReview() *usecase.MergeRequestReview
	MergeRequestCheck() *usecase.MergeRequestCheck
	FileLock() *usecase.FileLock
	Org() *usecase.Org
	Project() *usecase.Project
	Maintenance() *usecase.Maintenance
	IssueTracker() *usecase.IssueTracker
}

type nipaServer struct {
	pb.UnimplementedNipaServiceServer
	uc usecaseContainer
}

func New(usecaseContainer usecaseContainer) *nipaServer {
	return &nipaServer{uc: usecaseContainer}
}
