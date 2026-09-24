package grpc

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nipalab/nipa/internal/chunker"
	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
	pb "github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/snow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type Transport struct {
	url        string
	clientConn *grpc.ClientConn
}

func NewTransport() *Transport {
	return &Transport{}
}

func (t *Transport) Connect(url string, opts ...grpc.DialOption) error {
	if t.clientConn != nil && url != t.url {
		err := t.clientConn.Close()
		if err != nil {
			slog.Error("unable to close grpc connection", "url", url, "error", err)
		}
		t.clientConn = nil
		t.url = ""
	}
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(url, opts...)
	if err != nil {
		return err
	}
	t.url = url
	t.clientConn = conn
	return nil
}

func (t *Transport) Close() error {
	if t.clientConn != nil {
		return t.clientConn.Close()
	}
	return nil
}

func (t *Transport) NipaServiceClient() (pb.NipaServiceClient, error) {
	if t.clientConn == nil {
		return nil, errors.New("not connected to a nipa server")
	}
	return pb.NewNipaServiceClient(t.clientConn), nil
}

func (t *Transport) LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error) {
	client, err := t.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.LoginWithUsernamePassword(ctx, &pb.LoginUsernamePasswordRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return &domain.LoginResult{
		AccessToken:  res.GetAccessToken(),
		RefreshToken: res.GetRefreshToken(),
		ExpiresIn:    int(res.GetExpiresIn()),
		Host:         host,
	}, nil
}

func (t *Transport) LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error) {
	client, err := t.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.LoginWithRefreshToken(ctx, &pb.LoginWithRefreshRequest{
		RefreshToken: refreshToken,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return &domain.LoginResult{
		AccessToken:  res.GetAccessToken(),
		RefreshToken: res.GetRefreshToken(),
		ExpiresIn:    int(res.GetExpiresIn()),
		Host:         host,
	}, nil
}

type tokenSession interface {
	AccessToken(ctx context.Context, host string) (string, error)
	Refresh(ctx context.Context, host string) (string, error)
}

// ClientOption customizes a Client at construction time.
type ClientOption func(*Client)

// WithUploadWorkers pins the number of concurrent chunk uploads, disabling
// automatic tuning.
func WithUploadWorkers(workers int) ClientOption {
	return func(c *Client) {
		c.uploadWorkers = clampUploadWorkers(workers)
		c.tuner = nil
		c.limiter.setLimit(c.uploadWorkers)
	}
}

// WithHTTPClient replaces the HTTP client used for signed chunk transfers.
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		if httpClient != nil {
			c.http = httpClient
		}
	}
}

type Client struct {
	transport     *Transport
	session       tokenSession
	http          *http.Client
	limiter       *transferLimiter
	tuner         *concurrencyTuner
	uploadWorkers int
	confirmMu     sync.Mutex
}

func NewClient(transport *Transport, session tokenSession, opts ...ClientOption) *Client {
	c := &Client{
		transport:     transport,
		session:       session,
		http:          defaultHTTPClient(),
		limiter:       newTransferLimiter(defaultUploadWorkers),
		tuner:         newConcurrencyTuner(minUploadWorkers, maxUploadWorkers, defaultUploadWorkers),
		uploadWorkers: defaultUploadWorkers,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SetUploadWorkers adjusts the concurrent chunk transfer limit at runtime and
// disables automatic tuning.
func (c *Client) SetUploadWorkers(workers int) {
	c.uploadWorkers = clampUploadWorkers(workers)
	c.tuner = nil
	c.limiter.setLimit(c.uploadWorkers)
}

func (c *Client) recordTransfer(bytes int64, transferErr error) {
	if c.tuner == nil {
		return
	}
	limit := c.limiter.currentLimit()
	next := c.tuner.observe(limit, bytes, time.Now(), transferErr)
	if next != limit {
		c.limiter.setLimit(next)
	}
}

func (c *Client) Connect(ctx context.Context, host string) error {
	return c.transport.Connect(host, grpc.WithUnaryInterceptor(c.unaryAuthInterceptor()))
}

func (c *Client) Close() error {
	return c.transport.Close()
}

func (c *Client) LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error) {
	return c.transport.LoginWithUsernamePassword(ctx, host, username, password)
}

func (c *Client) LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error) {
	return c.transport.LoginWithRefreshToken(ctx, host, refreshToken)
}

func (c *Client) unaryAuthInterceptor() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		if method == "/greet.NipaService/LoginWithUsernamePassword" || method == "/greet.NipaService/LoginWithRefreshToken" {
			return invoker(ctx, method, req, reply, cc, opts...)
		}

		accToken, err := c.session.AccessToken(ctx, c.transport.url)
		if err != nil {
			return status.Error(codes.Unauthenticated, "unable to get access token")
		}
		authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+accToken)

		err = invoker(authCtx, method, req, reply, cc, opts...)
		if status.Code(err) != codes.Unauthenticated {
			return err
		}

		accToken, err = c.session.Refresh(ctx, c.transport.url)
		if err != nil {
			return err
		}
		retryCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+accToken)
		return invoker(retryCtx, method, req, reply, cc, opts...)
	}
}

func (c *Client) GetDefaultBranch(ctx context.Context, org, project string) (*serverDomain.Branch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetDefaultBranch(ctx, &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerBranch(res.GetBranch()), nil
}

func (c *Client) GetBranchByName(ctx context.Context, org, project, name string) (*serverDomain.Branch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetBranchByName(ctx, &pb.GetBranchByNameRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Name:    name,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerBranch(res.GetBranch()), nil
}

func (c *Client) ListBranches(ctx context.Context, org, project string) ([]*serverDomain.Branch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetListBranch(ctx, &pb.GetListBranchRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
		Limit:   100,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	branches := make([]*serverDomain.Branch, 0, len(res.GetBranches()))
	for _, b := range res.GetBranches() {
		if sb := toServerBranch(b); sb != nil {
			branches = append(branches, sb)
		}
	}
	return branches, nil
}

func (c *Client) GetCommitLog(ctx context.Context, org, project, branch string, startCommitID *snow.ID, limit int) ([]*serverDomain.CommitLogEntry, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	var startID *string
	if startCommitID != nil {
		s := startCommitID.Base36()
		startID = &s
	}
	res, err := client.GetCommitLog(ctx, &pb.GetCommitLogRequest{
		Context:       &pb.ProjectContext{Org: org, Project: project},
		Branch:        branch,
		StartCommitId: startID,
		Limit:         int32(limit),
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	entries := make([]*serverDomain.CommitLogEntry, 0, len(res.GetCommits()))
	for _, e := range res.GetCommits() {
		if se := toServerCommitLogEntry(e); se != nil {
			entries = append(entries, se)
		}
	}
	return entries, nil
}

func (c *Client) CreateBranch(ctx context.Context, org, project, name, fromBranch, fromCommitID, fromCommitHash string) (*serverDomain.Branch, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.CreateBranch(ctx, &pb.CreateBranchRequest{
		Context:        &pb.ProjectContext{Org: org, Project: project},
		Name:           name,
		FromBranch:     fromBranch,
		FromCommitId:   fromCommitID,
		FromCommitHash: fromCommitHash,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerBranch(res.GetBranch()), nil
}

func (c *Client) GetTreeNodeManifest(ctx context.Context, org, project, branch string, paths []string) (*serverDomain.TreeNode, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetTreeManifest(ctx, &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: org, Project: project},
		Branch:    branch,
		Paths:     paths,
		Recursive: true,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerTreeNode(res.GetRootTree()), nil
}

func (c *Client) Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string, parent2CommitHash, baseCommitID string) (*serverDomain.PushResult, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	req := &pb.PushRequest{
		Context:      &pb.ProjectContext{Org: org, Project: project},
		Branch:       branch,
		BaseTreeHash: baseTreeHash,
		Message:      message,
		RemovedFiles: removed,
	}
	if parent2CommitHash != "" {
		req.Parent_2CommitHash = &parent2CommitHash
	}
	if baseCommitID != "" {
		req.BaseCommitId = &baseCommitID
	}
	for _, f := range files {
		pf := &pb.PushFile{
			Path:      f.Path,
			Mode:      intToPBFileMode(f.Mode),
			SizeBytes: f.SizeBytes,
			IsBinary:  f.IsBinary,
			Encoding:  f.Encoding,
			FileHash:  f.FileHash.String(),
		}
		for _, h := range f.ChunkHashes {
			pf.ChunkHashes = append(pf.ChunkHashes, h.String())
		}
		req.Files = append(req.Files, pf)
	}

	res, err := client.Push(ctx, req)
	if err != nil {
		return nil, toDomainError(err)
	}
	commitID, _ := snow.ParseBase36(res.GetCommitId())
	commitHash, err := decodeHash(res.GetCommitHash())
	if err != nil {
		return nil, err
	}
	treeHash, err := decodeHash(res.GetTreeHash())
	if err != nil {
		return nil, err
	}
	return &serverDomain.PushResult{
		CommitID:   commitID,
		CommitHash: commitHash,
		TreeHash:   treeHash,
	}, nil
}

func (c *Client) authedContext(ctx context.Context) (context.Context, error) {
	accToken, err := c.session.AccessToken(ctx, c.transport.url)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "unable to get access token")
	}
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+accToken), nil
}

func intToPBFileMode(mode int) pb.FileMode {
	switch mode {
	case 0o444, 444:
		return pb.FileMode_FILE_MODE_READ_ONLY
	case 0o755, 755:
		return pb.FileMode_FILE_MODE_EXECUTABLE
	default:
		return pb.FileMode_FILE_MODE_READ_WRITE
	}
}

func toServerBranch(pbBranch *pb.Branch) *serverDomain.Branch {
	if pbBranch == nil {
		return nil
	}
	id, _ := snow.ParseBase36(pbBranch.GetId())
	var commitID *snow.ID
	if cid, err := snow.ParseBase36(pbBranch.GetCommitId()); err == nil && pbBranch.GetCommitId() != "" {
		commitID = &cid
	}
	return &serverDomain.Branch{
		ID:          id,
		Name:        pbBranch.GetName(),
		IsProtected: pbBranch.GetIsProtected(),
		IsDefault:   pbBranch.GetIsDefault(),
		CommitID:    commitID,
		UpdatedAt:   pbBranch.GetUpdatedAt().AsTime(),
		CreatedAt:   pbBranch.GetCreatedAt().AsTime(),
	}
}

func toServerCommitLogEntry(e *pb.CommitLogEntry) *serverDomain.CommitLogEntry {
	if e == nil {
		return nil
	}
	id, _ := snow.ParseBase36(e.GetCommitId())
	hash, _ := decodeHash(e.GetCommitHash())
	var parent1ID, parent2ID *snow.ID
	if p, err := snow.ParseBase36(e.GetParent_1Id()); err == nil && e.GetParent_1Id() != "" {
		parent1ID = &p
	}
	if p, err := snow.ParseBase36(e.GetParent_2Id()); err == nil && e.GetParent_2Id() != "" {
		parent2ID = &p
	}
	return &serverDomain.CommitLogEntry{
		Commit: serverDomain.Commit{
			ID:        id,
			Hash:      hash,
			Parent1ID: parent1ID,
			Parent2ID: parent2ID,
			Message:   e.GetMessage(),
			CreatedAt: e.GetCreatedAt().AsTime(),
		},
		AuthorName:  e.GetAuthorName(),
		AuthorEmail: e.GetAuthorEmail(),
	}
}

func toServerTreeNode(manifest *pb.TreeManifest) *serverDomain.TreeNode {
	if manifest == nil {
		return nil
	}
	node := &serverDomain.TreeNode{
		Name:         manifest.GetPath(),
		TreeChildren: make([]*serverDomain.TreeNode, 0, len(manifest.GetSubTrees())),
		FileChildren: make([]*serverDomain.File, 0, len(manifest.GetFiles())),
	}
	if hash, err := decodeHash(manifest.GetTreeHash()); err == nil {
		node.Hash = hash
	}
	for _, sub := range manifest.GetSubTrees() {
		node.TreeChildren = append(node.TreeChildren, toServerTreeNode(sub))
	}
	for _, f := range manifest.GetFiles() {
		node.FileChildren = append(node.FileChildren, toServerFile(f))
	}
	return node
}

func toServerFile(node *pb.FileNode) *serverDomain.File {
	if node == nil {
		return nil
	}
	file := &serverDomain.File{
		Name:      node.GetPath(),
		Mode:      int(node.GetMode()),
		SizeBytes: node.GetSizeBytes(),
		IsBinary:  node.GetIsBinary(),
		Encoding:  node.GetEncoding(),
	}
	var hashes []serverDomain.Hash
	for _, h := range node.GetChunkHashes() {
		if hash, err := decodeHash(h); err == nil {
			hashes = append(hashes, hash)
			file.Chunks = append(file.Chunks, serverDomain.Chunk{Hash: hash})
		}
	}
	file.Hash = chunker.FileHash(hashes)
	return file
}

func decodeHash(s string) (serverDomain.Hash, error) {
	var h serverDomain.Hash
	if len(s) != hex.EncodedLen(len(h)) {
		return h, errors.New("invalid hash length")
	}
	_, err := hex.Decode(h[:], []byte(s))
	return h, err
}

func toDomainError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return &domain.Error{Code: 404, Message: st.Message()}
	case codes.InvalidArgument:
		return &domain.Error{Code: 400, Message: st.Message()}
	case codes.Unauthenticated:
		return &domain.Error{Code: 401, Message: st.Message()}
	case codes.PermissionDenied:
		return &domain.Error{Code: 403, Message: st.Message()}
	case codes.FailedPrecondition:
		return &domain.Error{Code: 409, Message: st.Message()}
	default:
		return err
	}
}
