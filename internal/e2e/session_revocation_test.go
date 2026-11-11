package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/http/model"
)

func testDeactivatedAdminLosesAccess(t *testing.T, base, grpcHost, superToken string) {
	created := auditREST(t, http.MethodPost, base+"/api/v1/users",
		`{"name":"frank","email":"frank@example.com","password":"password123"}`, superToken)
	require.Equal(t, http.StatusOK, created.StatusCode)
	var frank model.UserResponse
	require.NoError(t, json.NewDecoder(created.Body).Decode(&frank))
	_ = created.Body.Close()
	promote := auditREST(t, http.MethodPatch, base+"/api/v1/users/"+frank.ID+"/admin", `{"is_admin":true,"is_super_admin":false}`, superToken)
	require.Equal(t, http.StatusOK, promote.StatusCode)
	_ = promote.Body.Close()

	frankToken := auditLogin(t, base, "frank@example.com", "password123")

	conn, err := grpc.NewClient(grpcHost, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	listProjects := func() error {
		ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+frankToken)
		_, err := pb.NewNipaServiceClient(conn).ListMyProjects(ctx, &pb.ListMyProjectsRequest{})
		return err
	}
	createUser := func(name string) int {
		resp := auditREST(t, http.MethodPost, base+"/api/v1/users",
			`{"name":"`+name+`","email":"`+name+`@example.com","password":"password123"}`, frankToken)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	require.NoError(t, listProjects())
	require.Equal(t, http.StatusOK, createUser("grace"))

	deactivate := auditREST(t, http.MethodDelete, base+"/api/v1/users/"+frank.ID, "", superToken)
	require.Equal(t, http.StatusOK, deactivate.StatusCode)
	_ = deactivate.Body.Close()

	require.Equal(t, http.StatusUnauthorized, createUser("heidi"))
	require.Equal(t, codes.Unauthenticated, status.Code(listProjects()))
}
