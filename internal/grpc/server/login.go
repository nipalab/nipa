package server

import (
	"context"

	"github.com/nipalab/nipa/internal/grpc/pb"
)

func (n *nipaServer) LoginWithUsernamePassword(ctx context.Context, req *pb.LoginUsernamePasswordRequest) (*pb.LoginResponse, error) {
	result, err := n.uc.Auth().LoginWithEmailPassword(ctx, req.Username, req.Password)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    int32(result.ExpiresIn),
	}, nil
}

func (n *nipaServer) LoginWithRefreshToken(ctx context.Context, req *pb.LoginWithRefreshRequest) (*pb.LoginResponse, error) {
	result, err := n.uc.Auth().LoginWithRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    int32(result.ExpiresIn),
	}, nil
}

func (n *nipaServer) BeginLoginSSHKey(ctx context.Context, req *pb.BeginLoginSSHKeyRequest) (*pb.BeginLoginSSHKeyResponse, error) {
	challenge, err := n.uc.Auth().BeginSSHKeyLogin(ctx, req.GetEmail())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.BeginLoginSSHKeyResponse{
		Nonce:     challenge.Nonce,
		ExpiresIn: int32(challenge.ExpiresIn),
	}, nil
}

func (n *nipaServer) CompleteLoginSSHKey(ctx context.Context, req *pb.CompleteLoginSSHKeyRequest) (*pb.LoginResponse, error) {
	result, err := n.uc.Auth().CompleteSSHKeyLogin(ctx, req.GetEmail(), req.GetNonce(), req.GetPublicKey(), req.GetSignature())
	if err != nil {
		return nil, handleError(err)
	}
	return &pb.LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    int32(result.ExpiresIn),
	}, nil
}
