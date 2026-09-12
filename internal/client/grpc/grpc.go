package grpc

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"

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

type Client struct {
	transport *Transport
	session   tokenSession
}

func NewClient(transport *Transport, session tokenSession) *Client {
	return &Client{
		transport: transport,
		session:   session,
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

func (c *Client) GetTreeNodeManifest(ctx context.Context, org, project, branch, path string) (*serverDomain.TreeNode, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	res, err := client.GetTreeManifest(ctx, &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: org, Project: project},
		Branch:    branch,
		Path:      path,
		Recursive: true,
	})
	if err != nil {
		return nil, toDomainError(err)
	}
	return toServerTreeNode(res.GetRootTree()), nil
}

func (c *Client) Push(ctx context.Context, org, project, branch, baseTreeHash, message string, files []*serverDomain.PushFile, removed []string) (*serverDomain.PushResult, error) {
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
	for _, f := range files {
		pf := &pb.PushFile{
			Path:      f.Path,
			Mode:      intToPBFileMode(f.Mode),
			SizeBytes: f.SizeBytes,
			IsBinary:  f.IsBinary,
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

func (c *Client) UploadChunks(ctx context.Context, chunks []*serverDomain.ChunkData, onChunk ...func(ch *serverDomain.ChunkData)) (int, int, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return 0, 0, err
	}
	authedCtx, err := c.authedContext(ctx)
	if err != nil {
		return 0, 0, err
	}
	stream, err := client.UploadChunks(authedCtx)
	if err != nil {
		return 0, 0, toDomainError(err)
	}
	for _, ch := range chunks {
		if err := stream.Send(&pb.ChunkUploadRequest{Hash: ch.Hash.String(), Data: ch.Data}); err != nil {
			return 0, 0, err
		}
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](ch)
		}
	}
	if err := stream.CloseSend(); err != nil {
		return 0, 0, err
	}
	res, err := stream.CloseAndRecv()
	if err != nil {
		return 0, 0, toDomainError(err)
	}
	return int(res.GetUploaded()), int(res.GetSkipped()), nil
}

func (c *Client) DownloadChunks(ctx context.Context, hashes []serverDomain.Hash, onChunk ...func(h serverDomain.Hash, data []byte)) (map[serverDomain.Hash][]byte, error) {
	client, err := c.transport.NipaServiceClient()
	if err != nil {
		return nil, err
	}
	authedCtx, err := c.authedContext(ctx)
	if err != nil {
		return nil, err
	}
	stream, err := client.DownloadChunks(authedCtx)
	if err != nil {
		return nil, toDomainError(err)
	}
	for _, h := range hashes {
		if err := stream.Send(&pb.DownloadChunksRequest{Hash: h.String()}); err != nil {
			return nil, err
		}
	}
	if err := stream.CloseSend(); err != nil {
		return nil, err
	}
	out := make(map[serverDomain.Hash][]byte, len(hashes))
	for {
		recv, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, toDomainError(err)
		}
		hash, err := decodeHash(recv.GetHash())
		if err != nil {
			return nil, err
		}
		out[hash] = recv.GetData()
		if len(onChunk) > 0 && onChunk[0] != nil {
			onChunk[0](hash, recv.GetData())
		}
	}
	return out, nil
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
	}
	for _, h := range node.GetChunkHashes() {
		if hash, err := decodeHash(h); err == nil {
			file.Chunks = append(file.Chunks, serverDomain.Chunk{Hash: hash})
		}
	}
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
