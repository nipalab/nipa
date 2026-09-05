package grpc

import (
	"context"
	"encoding/hex"
	"errors"
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

type tokenProvider interface {
	LoginWithRefreshToken(ctx context.Context, host, refreshToken string) error
	GetToken(ctx context.Context, host string) (string, error)
}

type Client struct {
	url           string
	clientConn    *grpc.ClientConn
	tokenProvider tokenProvider
}

func NewClient(tokenProvider tokenProvider) *Client {
	return &Client{
		tokenProvider: tokenProvider,
	}
}

func (c *Client) Close() error {
	if c.clientConn != nil {
		return c.clientConn.Close()
	}
	return nil
}

func (c *Client) Connect(url string) error {
	if c.clientConn != nil {
		if url != c.url {
			err := c.clientConn.Close()
			if err != nil {
				slog.Error("unable to close grpc connection", "url", url, "error", err)
			}
			c.clientConn = nil
			c.url = ""
		} else {
			return nil
		}
	}
	conn, err := grpc.NewClient(url, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(c.unaryAuthInterceptor()))
	if err != nil {
		return err
	}
	c.url = url
	c.clientConn = conn
	return nil
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

		accToken, err := c.tokenProvider.GetToken(ctx, c.url)
		if err != nil {
			return status.Error(codes.Unauthenticated, "unable to get access token")
		}
		authCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+accToken)

		err = invoker(authCtx, method, req, reply, cc, opts...)
		if status.Code(err) != codes.Unauthenticated {
			return err
		}

		err = c.tokenProvider.LoginWithRefreshToken(ctx, c.url, "")
		if err != nil {
			return err
		}

		accToken, err = c.tokenProvider.GetToken(ctx, c.url)
		if err != nil {
			return status.Error(codes.Unauthenticated, "unable to get access token after refresh")
		}
		retryCtx := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+accToken)
		return invoker(retryCtx, method, req, reply, cc, opts...)
	}
}

func (c *Client) LoginWithUsernamePassword(ctx context.Context, host, username, password string) (*domain.LoginResult, error) {
	if err := c.Connect(host); err != nil {
		return nil, err
	}
	client := pb.NewNipaServiceClient(c.clientConn)
	res, err := client.LoginWithUsernamePassword(ctx, &pb.LoginUsernamePasswordRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, err
	}
	return &domain.LoginResult{
		AccessToken:  res.GetAccessToken(),
		RefreshToken: res.GetRefreshToken(),
		ExpiresIn:    int(res.GetExpiresIn()),
		Host:         host,
	}, nil
}

func (c *Client) LoginWithRefreshToken(ctx context.Context, host, refreshToken string) (*domain.LoginResult, error) {
	if err := c.Connect(host); err != nil {
		return nil, err
	}
	client := pb.NewNipaServiceClient(c.clientConn)
	res, err := client.LoginWithRefreshToken(ctx, &pb.LoginWithRefreshRequest{
		RefreshToken: refreshToken,
	})
	if err != nil {
		return nil, err
	}
	return &domain.LoginResult{
		AccessToken:  res.GetAccessToken(),
		RefreshToken: res.GetRefreshToken(),
		ExpiresIn:    int(res.GetExpiresIn()),
		Host:         host,
	}, nil
}

func (c *Client) GetDefaultBranch(ctx context.Context, org, project string) (*serverDomain.Branch, error) {
	if err := c.requireConnection(); err != nil {
		return nil, err
	}
	client := pb.NewNipaServiceClient(c.clientConn)
	res, err := client.GetDefaultBranch(ctx, &pb.GetDefaultBranchRequest{
		Context: &pb.ProjectContext{Org: org, Project: project},
	})
	if err != nil {
		return nil, err
	}
	return toServerBranch(res.GetBranch()), nil
}

func (c *Client) GetTreeNodeManifest(ctx context.Context, org, project, branch string) (*serverDomain.TreeNode, error) {
	if err := c.requireConnection(); err != nil {
		return nil, err
	}
	client := pb.NewNipaServiceClient(c.clientConn)
	res, err := client.GetTreeManifest(ctx, &pb.GetTreeManifestRequest{
		Context:   &pb.ProjectContext{Org: org, Project: project},
		Branch:    branch,
		Recursive: true,
	})
	if err != nil {
		return nil, err
	}
	return toServerTreeNode(res.GetRootTree()), nil
}

func (c *Client) requireConnection() error {
	if c.clientConn == nil {
		return errors.New("not connected to a nipa server")
	}
	return nil
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
