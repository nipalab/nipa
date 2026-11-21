package server

import (
	"context"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"github.com/nipalab/nipa/internal/usecase"
)

// ResolveIssues looks up title, status and type of issue keys through the
// project's tracker; a tracker that is down answers key and URL only.
func (n *nipaServer) ResolveIssues(ctx context.Context, req *pb.ResolveIssuesRequest) (*pb.ResolveIssuesResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	tracker, err := n.issueTracker()
	if err != nil {
		return nil, handleError(err)
	}
	issues, err := tracker.ResolveIssues(ctx, project.ID, req.GetKeys())
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.ResolveIssuesResponse{Issues: make([]*pb.IssueDetail, 0, len(issues))}
	for _, issue := range issues {
		resp.Issues = append(resp.Issues, resolvedIssueToPB(issue))
	}
	return resp, nil
}

// SearchIssues backs issue pickers when the project's tracker can search.
func (n *nipaServer) SearchIssues(ctx context.Context, req *pb.SearchIssuesRequest) (*pb.SearchIssuesResponse, error) {
	_, project, err := n.uc.Common().ResolveBySlug(ctx, req.GetContext().GetOrg(), req.GetContext().GetProject())
	if err != nil {
		return nil, handleError(err)
	}
	tracker, err := n.issueTracker()
	if err != nil {
		return nil, handleError(err)
	}
	res, err := tracker.SearchIssues(ctx, project.ID, req.GetQuery(), int(req.GetLimit()))
	if err != nil {
		return nil, handleError(err)
	}
	resp := &pb.SearchIssuesResponse{Issues: make([]*pb.IssueDetail, 0, len(res.Issues)), Supported: res.Supported, Unavailable: res.Unavailable}
	for _, issue := range res.Issues {
		resp.Issues = append(resp.Issues, resolvedIssueToPB(issue))
	}
	return resp, nil
}

func (n *nipaServer) issueTracker() (*usecase.IssueTracker, error) {
	tracker := n.uc.IssueTracker()
	if tracker == nil || !tracker.Available() {
		return nil, domain.NewErrorServiceUnavailable("the issue tracker integration is not configured on this server")
	}
	return tracker, nil
}

// issueKeys looks up the issue keys of subjects the caller has already read;
// a project without an issue tracker answers nil.
func (n *nipaServer) issueKeys(ctx context.Context, projectID snow.ID, kind string, ids []snow.ID) map[snow.ID][]*pb.IssueKey {
	refs := n.uc.IssueTracker().SubjectIssueKeys(ctx, projectID, kind, ids)
	if len(refs) == 0 {
		return nil
	}
	out := make(map[snow.ID][]*pb.IssueKey, len(refs))
	for id, keys := range refs {
		for _, key := range keys {
			out[id] = append(out[id], &pb.IssueKey{Key: key.Key, Url: key.URL})
		}
	}
	return out
}

func (n *nipaServer) commitLogToPB(ctx context.Context, projectID snow.ID, entries []*domain.CommitLogEntry) []*pb.CommitLogEntry {
	ids := make([]snow.ID, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	keys := n.issueKeys(ctx, projectID, domain.IssueSubjectCommit, ids)
	out := make([]*pb.CommitLogEntry, 0, len(entries))
	for _, entry := range entries {
		commit := commitLogEntryToPB(entry)
		commit.IssueKeys = keys[entry.ID]
		out = append(out, commit)
	}
	return out
}

func (n *nipaServer) mergeRequestsToPB(ctx context.Context, projectID snow.ID, requests []*domain.MergeRequest) []*pb.MergeRequestDetail {
	ids := make([]snow.ID, len(requests))
	for i, request := range requests {
		ids[i] = snow.ID(request.ID)
	}
	keys := n.issueKeys(ctx, projectID, domain.IssueSubjectMergeRequest, ids)
	out := make([]*pb.MergeRequestDetail, 0, len(requests))
	for _, request := range requests {
		detail := domainMergeRequestToPB(request)
		detail.IssueKeys = keys[snow.ID(request.ID)]
		out = append(out, detail)
	}
	return out
}

func (n *nipaServer) branchesToPB(ctx context.Context, projectID snow.ID, branches []*domain.Branch) []*pb.Branch {
	ids := make([]snow.ID, len(branches))
	for i, branch := range branches {
		ids[i] = branch.ID
	}
	keys := n.issueKeys(ctx, projectID, domain.IssueSubjectBranch, ids)
	out := make([]*pb.Branch, 0, len(branches))
	for _, branch := range branches {
		detail := domainBranchToPB(branch)
		detail.IssueKeys = keys[branch.ID]
		out = append(out, detail)
	}
	return out
}

func resolvedIssueToPB(issue usecase.ResolvedIssue) *pb.IssueDetail {
	out := &pb.IssueDetail{Key: issue.Key, Url: issue.URL, State: issue.State}
	if issue.Issue != nil {
		out.Title = issue.Issue.Title
		out.Type = issue.Issue.Type
		out.Status = issue.Issue.Status
		out.StatusCategory = issue.Issue.StatusCategory
		out.Assignee = issue.Issue.Assignee
	}
	return out
}
