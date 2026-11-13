package server

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/usecase/dto"
)

const mfaUpgradeHint = "this account uses two-factor authentication; upgrade nipa or log in with an SSH key"

func (n *nipaServer) LoginWithUsernamePassword(ctx context.Context, req *pb.LoginUsernamePasswordRequest) (*pb.LoginResponse, error) {
	result, err := n.uc.Auth().LoginWithEmailPassword(ctx, req.Username, req.Password)
	if err != nil {
		return nil, handleError(err)
	}
	if result.MFAChallenge != nil && !req.GetSupportsMfa() {
		return nil, status.Error(codes.FailedPrecondition, mfaUpgradeHint)
	}
	return loginResponse(result), nil
}

func (n *nipaServer) CompleteLoginMFA(ctx context.Context, req *pb.CompleteLoginMFARequest) (*pb.LoginResponse, error) {
	result, err := n.uc.Auth().CompleteLoginMFA(ctx, req.GetChallenge(), req.GetCode())
	if err != nil {
		return nil, handleError(err)
	}
	return loginResponse(result), nil
}

func loginResponse(result *dto.LoginResult) *pb.LoginResponse {
	if result.MFAChallenge != nil {
		return &pb.LoginResponse{
			MfaChallenge: result.MFAChallenge.Token,
			ExpiresIn:    int32(result.MFAChallenge.ExpiresIn),
		}
	}
	return &pb.LoginResponse{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    int32(result.ExpiresIn),
	}
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
		Audience:  challenge.Audience,
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
