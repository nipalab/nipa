package server

import (
	"context"
	"strings"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultMergeRequestLimit = 50

func (n *nipaServer) CreateMergeRequest(ctx context.Context, req *pb.CreateMergeRequestRequest) (*pb.CreateMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Create(
		ctx, project.ID, req.GetTitle(), req.GetDescription(), req.GetSourceBranch(), req.GetTargetBranch(),
	)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CreateMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) UpdateMergeRequest(ctx context.Context, req *pb.UpdateMergeRequestRequest) (*pb.UpdateMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Update(ctx, project.ID, req.GetNumber(), req.GetTitle(), req.GetDescription())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.UpdateMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) ListMergeRequests(ctx context.Context, req *pb.ListMergeRequestsRequest) (*pb.ListMergeRequestsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	limit := int(req.GetLimit())
	if limit <= 0 {
		limit = defaultMergeRequestLimit
	}
	opts := domain.MergeRequestListOptions{
		Status:       req.GetStatus(),
		SourceBranch: req.GetSourceBranch(),
		TargetBranch: req.GetTargetBranch(),
		After:        req.GetAfterNumber(),
		Limit:        limit + 1,
	}
	if raw := req.GetAuthor(); raw != "" {
		author, err := snow.ParseBase36(raw)
		if err != nil {
			return nil, handleError(domain.NewErrorUser("invalid author id"))
		}
		opts.Author = &author
	}
	requests, err := n.uc.MergeRequest().List(ctx, project.ID, opts)
	if err != nil {
		return nil, handleError(err)
	}
	hasMore := len(requests) > limit
	if hasMore {
		requests = requests[:limit]
	}
	if review := n.uc.MergeRequestReview(); review != nil {
		if err := review.AttachSummaries(ctx, project.ID, requests); err != nil {
			return nil, handleError(err)
		}
	}
	resp := &pb.ListMergeRequestsResponse{MergeRequests: make([]*pb.MergeRequestDetail, 0, len(requests))}
	for _, request := range requests {
		resp.MergeRequests = append(resp.MergeRequests, domainMergeRequestToPB(request))
	}
	if hasMore {
		next := requests[len(requests)-1].Number
		resp.NextCursor = &next
	}
	return resp, nil
}

func (n *nipaServer) GetMergeRequest(ctx context.Context, req *pb.GetMergeRequestRequest) (*pb.GetMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Get(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	info, err := n.uc.MergeRequest().Check(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	if review := n.uc.MergeRequestReview(); review != nil {
		if err := review.AttachSummaries(ctx, project.ID, []*domain.MergeRequest{request}); err != nil {
			return nil, handleError(err)
		}
	}
	return &pb.GetMergeRequestResponse{
		MergeRequest: domainMergeRequestToPB(request),
		Mergeability: domainMergeabilityToPB(info),
	}, nil
}

func (n *nipaServer) MergeMergeRequest(ctx context.Context, req *pb.MergeMergeRequestRequest) (*pb.MergeMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, info, err := n.uc.MergeRequest().Merge(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.MergeMergeRequestResponse{
		MergeRequest: domainMergeRequestToPB(request),
		Mergeability: domainMergeabilityToPB(info),
	}, nil
}

func (n *nipaServer) CloseMergeRequest(ctx context.Context, req *pb.CloseMergeRequestRequest) (*pb.CloseMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Close(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CloseMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) ReopenMergeRequest(ctx context.Context, req *pb.ReopenMergeRequestRequest) (*pb.ReopenMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	request, err := n.uc.MergeRequest().Reopen(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.ReopenMergeRequestResponse{MergeRequest: domainMergeRequestToPB(request)}, nil
}

func (n *nipaServer) CheckMergeRequest(ctx context.Context, req *pb.CheckMergeRequestRequest) (*pb.CheckMergeRequestResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	info, err := n.uc.MergeRequest().Check(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.CheckMergeRequestResponse{Mergeability: domainMergeabilityToPB(info)}, nil
}

func (n *nipaServer) ListMergeRequestCommits(ctx context.Context, req *pb.ListMergeRequestCommitsRequest) (*pb.ListMergeRequestCommitsResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	commits, err := n.uc.MergeRequest().Commits(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ListMergeRequestCommitsResponse{Commits: make([]*pb.CommitLogEntry, 0, len(commits))}
	for _, commit := range commits {
		resp.Commits = append(resp.Commits, commitLogEntryToPB(commit))
	}
	return resp, nil
}

func (n *nipaServer) GetMergeRequestDiff(ctx context.Context, req *pb.GetMergeRequestDiffRequest) (*pb.GetMergeRequestDiffResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.Context.Org, req.Context.Project)
	if err != nil {
		return nil, handleError(err)
	}
	files, err := n.uc.MergeRequest().Diff(ctx, project.ID, req.GetNumber())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.GetMergeRequestDiffResponse{Files: make([]*pb.DiffFileDetail, 0, len(files))}
	for _, file := range files {
		resp.Files = append(resp.Files, diffFileToPB(file))
	}
	return resp, nil
}

func domainMergeRequestToPB(request *domain.MergeRequest) *pb.MergeRequestDetail {
	detail := &pb.MergeRequestDetail{
		Id:                snow.ID(request.ID).Base36(),
		Number:            request.Number,
		ProjectId:         request.ProjectID.Base36(),
		SourceBranch:      request.SourceBranch,
		TargetBranch:      request.TargetBranch,
		Title:             request.Title,
		Description:       request.Description,
		Status:            request.Status,
		MergeCommitId:     snowPtrToStringPtr(request.MergeCommitID),
		MergeBaseCommitId: snowPtrToStringPtr(request.MergeBaseCommitID),
		CreatedBy:         request.CreatedBy.Base36(),
		CreatedAt:         timestamppb.New(request.CreatedAt),
		UpdatedAt:         timestamppb.New(request.UpdatedAt),
	}
	if request.Review != nil {
		detail.Review = domainReviewStateToPB(request.Review)
	}
	return detail
}

func domainMergeabilityToPB(info *domain.Mergeability) *pb.MergeabilityDetail {
	if info == nil {
		return nil
	}
	return &pb.MergeabilityDetail{
		Status:            info.Status,
		SourceCommitId:    snowPtrToStringPtr(info.SourceCommitID),
		TargetCommitId:    snowPtrToStringPtr(info.TargetCommitID),
		MergeBaseCommitId: snowPtrToStringPtr(info.MergeBaseCommitID),
		BlockedBy:         info.BlockedBy,
	}
}

func diffFileToPB(file diff.FileDiff) *pb.DiffFileDetail {
	resp := &pb.DiffFileDetail{
		Path:   file.Change.Path,
		Status: file.Change.Status.String(),
		Binary: file.Change.New.IsBinary || file.Change.Old.IsBinary,
	}
	if file.Change.Status == diff.Renamed {
		resp.OldPath = file.Change.Old.Path
	}
	if !resp.Binary {
		options := diff.Options{Context: diff.DefaultContext}
		resp.Patch = diff.FilePatch(file, options)
		resp.Hunks = diffHunksToPB(diff.FileHunks(file, options))
		additions, deletions := diff.CountLines(file.Old, file.New)
		resp.Additions, resp.Deletions = int64(additions), int64(deletions)
	}
	return resp
}

func diffHunksToPB(hunks []diff.Hunk) []*pb.DiffHunkDetail {
	if len(hunks) == 0 {
		return nil
	}
	out := make([]*pb.DiffHunkDetail, 0, len(hunks))
	for _, hunk := range hunks {
		lines := make([]*pb.DiffLineDetail, 0, len(hunk.Lines))
		for _, line := range hunk.Lines {
			text, _ := strings.CutSuffix(line.Text, "\n")
			detail := &pb.DiffLineDetail{
				Kind:      diff.LineKind(line.Kind),
				Text:      text,
				NoNewline: line.NoNewline,
			}
			if line.Old > 0 {
				old := int64(line.Old)
				detail.OldLine = &old
			}
			if line.New > 0 {
				newLine := int64(line.New)
				detail.NewLine = &newLine
			}
			lines = append(lines, detail)
		}
		out = append(out, &pb.DiffHunkDetail{
			OldStart: int64(hunk.OldStart),
			OldLines: int64(hunk.OldLines),
			NewStart: int64(hunk.NewStart),
			NewLines: int64(hunk.NewLines),
			Lines:    lines,
		})
	}
	return out
}
