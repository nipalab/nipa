package grpc

import (
	"context"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (c *Client) LockFile(ctx context.Context, org, project, path, branch string) (*clientDomain.FileLock, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.LockFile(ctx, &pb.LockFileRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Path:    path,
		Branch:  branch,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toClientFileLock(res.GetLock()), nil
}

func (c *Client) UnlockFile(ctx context.Context, org, project, path, branch string) error {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return err
	}
	_, err = client.UnlockFile(ctx, &pb.UnlockFileRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Path:    path,
		Branch:  branch,
	})
	return toDomainError(err)
}

func (c *Client) ListFileLocks(ctx context.Context, org, project string) ([]*clientDomain.FileLock, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.ListFileLocks(ctx, &pb.ListFileLocksRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	locks := make([]*clientDomain.FileLock, 0, len(res.GetLocks()))
	for _, lock := range res.GetLocks() {
		if converted := toClientFileLock(lock); converted != nil {
			locks = append(locks, converted)
		}
	}
	return locks, nil
}

func toClientFileLock(lock *pb.FileLockDetail) *clientDomain.FileLock {
	if lock == nil {
		return nil
	}
	return &clientDomain.FileLock{
		ID:                 lock.GetId(),
		Path:               lock.GetPath(),
		Branch:             lock.GetBranch(),
		Global:             lock.GetGlobal(),
		HeldBy:             lock.GetHeldBy(),
		HeldByName:         lock.GetHeldByName(),
		MergeRequestNumber: lock.MergeRequestNumber,
		AcquiredAt:         lock.GetAcquiredAt().AsTime(),
	}
}
