package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

type mrClient interface {
	Connect(ctx context.Context, host string) error
	CreateMergeRequest(ctx context.Context, org, project, title, description, sourceBranch, targetBranch string, draft bool) (*domain.MergeRequest, error)
	UpdateMergeRequest(ctx context.Context, org, project string, number int64, title, description string, draft *bool) (*domain.MergeRequest, error)
	SetMergeRequestAssignees(ctx context.Context, org, project string, number int64, userIDs []string) (*domain.MergeRequest, error)
	ReportMergeRequestCheck(ctx context.Context, org, project string, number int64, name, state, detailsURL string) (*domain.MergeRequestCheck, error)
	ListMergeRequestChecks(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestCheck, error)
	ListMergeRequests(ctx context.Context, org, project string, opts domain.ListMergeRequestOptions) ([]*domain.MergeRequest, int64, error)
	GetMergeRequest(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, *domain.Mergeability, error)
	MergeMergeRequest(ctx context.Context, org, project string, number int64, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error)
	MarkMergeRequestMerged(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, error)
	CloseMergeRequest(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, error)
	ReopenMergeRequest(ctx context.Context, org, project string, number int64) (*domain.MergeRequest, error)
	CheckMergeRequest(ctx context.Context, org, project string, number int64) (*domain.Mergeability, error)
	ListMergeRequestCommits(ctx context.Context, org, project string, number int64) ([]*serverDomain.CommitLogEntry, error)
	GetMergeRequestDiff(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestDiffFile, error)
	ListMergeRequestReviews(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestReview, error)
	SubmitMergeRequestReview(ctx context.Context, org, project string, number int64, state, body string) (*domain.MergeRequestReview, error)
	ListMergeRequestThreads(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestThread, error)
	AddMergeRequestComment(ctx context.Context, org, project string, number int64, filePath string, oldLine, newLine *int64, body string) (*domain.MergeRequestThread, error)
	ReplyMergeRequestThread(ctx context.Context, org, project string, number int64, threadID, body string) (*domain.MergeRequestComment, error)
	ResolveMergeRequestThread(ctx context.Context, org, project string, number int64, threadID string, resolved bool) (*domain.MergeRequestThread, error)
	ListMergeRequestTimeline(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestTimelineItem, error)
	ListMergeRequestReviewRequests(ctx context.Context, org, project string, number int64) ([]*domain.MergeRequestReviewRequest, error)
	RequestMergeRequestReview(ctx context.Context, org, project string, number int64, userID string) (*domain.MergeRequestReviewRequest, error)
	RemoveMergeRequestReviewRequest(ctx context.Context, org, project string, number int64, userID string) error
	GetDefaultBranch(ctx context.Context, org, project string) (*serverDomain.Branch, error)
}

type mrLocalRepo interface {
	Init(target string) error
	LoadConfig() (*domain.Config, error)
}

type CreateMergeRequestOptions struct {
	Title       string
	Description string
	Source      string
	Target      string
	Draft       bool
}

// ListMergeRequestOptions filters and paginates a merge request listing.
type ListMergeRequestOptions = domain.ListMergeRequestOptions

type MergeRequest struct {
	auth      *Auth
	client    mrClient
	localRepo mrLocalRepo
}

func NewMergeRequest(auth *Auth, client mrClient, localRepo mrLocalRepo) *MergeRequest {
	return &MergeRequest{
		auth:      auth,
		client:    client,
		localRepo: localRepo,
	}
}

func (m *MergeRequest) Create(ctx context.Context, root string, opts CreateMergeRequestOptions) (*domain.MergeRequest, error) {
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		return nil, domain.NewUserError("a title is required")
	}
	url, cfg, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		source = cfg.Branch
	}
	target := strings.TrimSpace(opts.Target)
	if target == "" {
		branch, err := m.client.GetDefaultBranch(ctx, url.Org, url.Project)
		if err != nil {
			return nil, err
		}
		target = branch.Name
	}
	if source == target {
		return nil, domain.NewUserError(fmt.Sprintf("source and target branches must differ (both are %q); pass --target", source))
	}
	return m.client.CreateMergeRequest(ctx, url.Org, url.Project, title, opts.Description, source, target, opts.Draft)
}

// Update patches the title and description and/or toggles the draft state.
func (m *MergeRequest) Update(ctx context.Context, root, id, title, description string, draft *bool) (*domain.MergeRequest, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(title) == "" && strings.TrimSpace(description) == "" && draft == nil {
		return nil, domain.NewUserError("pass --title, --description or --draft to update a merge request")
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return m.client.UpdateMergeRequest(ctx, url.Org, url.Project, number, title, description, draft)
}

// SetDraft toggles the draft state of a merge request.
func (m *MergeRequest) SetDraft(ctx context.Context, root, id string, draft bool) (*domain.MergeRequest, error) {
	return m.Update(ctx, root, id, "", "", &draft)
}

// SetAssignees replaces the assignees of a merge request.
func (m *MergeRequest) SetAssignees(ctx context.Context, root, id string, userIDs []string) (*domain.MergeRequest, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	cleaned := make([]string, 0, len(userIDs))
	for _, userID := range userIDs {
		if trimmed := strings.TrimSpace(userID); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return m.client.SetMergeRequestAssignees(ctx, url.Org, url.Project, number, cleaned)
}

// Checks lists the status checks reported for the request's current head.
func (m *MergeRequest) Checks(ctx context.Context, root, id string) ([]*domain.MergeRequestCheck, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestChecks(ctx, url.Org, url.Project, number)
}

// ReportCheck records a status check result for the request's current head.
func (m *MergeRequest) ReportCheck(ctx context.Context, root, id, name, state, detailsURL string) (*domain.MergeRequestCheck, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, domain.NewUserError("a check name is required")
	}
	state = strings.TrimSpace(state)
	if !domain.IsValidMergeRequestCheckState(state) {
		return nil, domain.NewUserError("state must be one of pending, success, failed")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ReportMergeRequestCheck(ctx, url.Org, url.Project, number, name, state, detailsURL)
}

// List returns one page of merge requests and the cursor for the next page
// (0 when the page is the last one).
func (m *MergeRequest) List(ctx context.Context, root string, opts domain.ListMergeRequestOptions) ([]*domain.MergeRequest, int64, error) {
	opts.Status = strings.TrimSpace(opts.Status)
	if opts.Status != "" && !domain.IsValidMergeRequestStatus(opts.Status) {
		return nil, 0, domain.NewUserError("status must be one of open, merged, closed")
	}
	opts.Author = strings.TrimSpace(opts.Author)
	opts.SourceBranch = strings.TrimSpace(opts.SourceBranch)
	opts.TargetBranch = strings.TrimSpace(opts.TargetBranch)
	opts.Search = strings.TrimSpace(opts.Search)
	opts.Assignee = strings.TrimSpace(opts.Assignee)
	if opts.After < 0 {
		return nil, 0, domain.NewUserError("the after cursor cannot be negative")
	}
	if opts.Limit <= 0 {
		opts.Limit = 50
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, 0, err
	}
	return m.client.ListMergeRequests(ctx, url.Org, url.Project, opts)
}

func (m *MergeRequest) Close(ctx context.Context, root, id string) (*domain.MergeRequest, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, err
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, err
	}
	return m.client.CloseMergeRequest(ctx, url.Org, url.Project, number)
}

// MarkMerged marks a request as merged without moving the target branch.
func (m *MergeRequest) MarkMerged(ctx context.Context, root, id string) (*domain.MergeRequest, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.MarkMergeRequestMerged(ctx, url.Org, url.Project, number)
}

// Merge lands a merge request with the given strategy (empty = ff) and can
// delete the source branch after a successful merge.
func (m *MergeRequest) Merge(ctx context.Context, root, id, strategy string, deleteSource bool) (*domain.MergeRequest, *domain.Mergeability, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, nil, err
	}
	strategy = strings.TrimSpace(strategy)
	if strategy != "" && !serverDomain.IsValidMergeStrategy(strategy) {
		return nil, nil, domain.NewUserError("strategy must be one of ff, merge, squash, rebase")
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, nil, err
	}
	return m.client.MergeMergeRequest(ctx, url.Org, url.Project, number, strategy, deleteSource)
}

// View returns one merge request with its live mergeability and review summary.
func (m *MergeRequest) View(ctx context.Context, root, id string) (*domain.MergeRequest, *domain.Mergeability, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, nil, err
	}
	return m.client.GetMergeRequest(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Reopen(ctx context.Context, root, id string) (*domain.MergeRequest, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ReopenMergeRequest(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Check(ctx context.Context, root, id string) (*domain.Mergeability, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.CheckMergeRequest(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Diff(ctx context.Context, root, id string) ([]*domain.MergeRequestDiffFile, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.GetMergeRequestDiff(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Commits(ctx context.Context, root, id string) ([]*serverDomain.CommitLogEntry, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestCommits(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) Reviews(ctx context.Context, root, id string) ([]*domain.MergeRequestReview, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestReviews(ctx, url.Org, url.Project, number)
}

// Review submits one decision (approved, changes_requested or commented) with
// its message. The server requires a body when there are no inline comments.
func (m *MergeRequest) Review(ctx context.Context, root, id, state, body string) (*domain.MergeRequestReview, error) {
	state = strings.TrimSpace(state)
	if !domain.IsValidMergeRequestReviewState(state) {
		return nil, domain.NewUserError("state must be one of approved, changes_requested, commented")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, domain.NewUserError("a review needs a message; pass -m")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.SubmitMergeRequestReview(ctx, url.Org, url.Project, number, state, body)
}

func (m *MergeRequest) Threads(ctx context.Context, root, id string) ([]*domain.MergeRequestThread, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestThreads(ctx, url.Org, url.Project, number)
}

// Comment starts a conversation thread; an empty filePath creates a top-level
// thread, otherwise the comment anchors to the given diff line.
func (m *MergeRequest) Comment(ctx context.Context, root, id, filePath string, oldLine, newLine *int64, body string) (*domain.MergeRequestThread, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, domain.NewUserError("a comment needs a message; pass -m")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.AddMergeRequestComment(ctx, url.Org, url.Project, number, strings.TrimSpace(filePath), oldLine, newLine, body)
}

func (m *MergeRequest) Reply(ctx context.Context, root, id, threadID, body string) (*domain.MergeRequestComment, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, domain.NewUserError("a thread id is required")
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, domain.NewUserError("a reply needs a message; pass -m")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ReplyMergeRequestThread(ctx, url.Org, url.Project, number, threadID, body)
}

func (m *MergeRequest) Resolve(ctx context.Context, root, id, threadID string, resolved bool) (*domain.MergeRequestThread, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, domain.NewUserError("a thread id is required")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ResolveMergeRequestThread(ctx, url.Org, url.Project, number, threadID, resolved)
}

func (m *MergeRequest) Timeline(ctx context.Context, root, id string) ([]*domain.MergeRequestTimelineItem, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestTimeline(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) ReviewRequests(ctx context.Context, root, id string) ([]*domain.MergeRequestReviewRequest, error) {
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.ListMergeRequestReviewRequests(ctx, url.Org, url.Project, number)
}

func (m *MergeRequest) RequestReview(ctx context.Context, root, id, userID string) (*domain.MergeRequestReviewRequest, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, domain.NewUserError("a reviewer user id is required")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return nil, err
	}
	return m.client.RequestMergeRequestReview(ctx, url.Org, url.Project, number, userID)
}

func (m *MergeRequest) RemoveReviewRequest(ctx context.Context, root, id, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return domain.NewUserError("a reviewer user id is required")
	}
	url, number, err := m.target(ctx, root, id)
	if err != nil {
		return err
	}
	return m.client.RemoveMergeRequestReviewRequest(ctx, url.Org, url.Project, number, userID)
}

// target resolves the project of a working copy and parses the request number.
func (m *MergeRequest) target(ctx context.Context, root, id string) (*domain.NipaUrl, int64, error) {
	number, err := mergeRequestNumber(id)
	if err != nil {
		return nil, 0, err
	}
	url, _, err := m.connect(ctx, root)
	if err != nil {
		return nil, 0, err
	}
	return url, number, nil
}

func (m *MergeRequest) connect(ctx context.Context, root string) (*domain.NipaUrl, *domain.Config, error) {
	if err := m.localRepo.Init(root); err != nil {
		return nil, nil, err
	}
	cfg, err := m.localRepo.LoadConfig()
	if err != nil {
		return nil, nil, err
	}
	url, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return nil, nil, err
	}
	if err := m.client.Connect(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	if err := m.auth.MakeSureLoggedIn(ctx, url.Host); err != nil {
		return nil, nil, err
	}
	return url, cfg, nil
}

func mergeRequestNumber(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, domain.NewUserError("a merge request number is required")
	}
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number <= 0 {
		return 0, domain.NewUserError(fmt.Sprintf("invalid merge request number %q", raw))
	}
	return number, nil
}
