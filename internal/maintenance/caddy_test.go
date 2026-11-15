package maintenance_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/nipalab/nipa/internal/maintenance"
)

type caddyErrorPage struct {
	statuses    []string
	marker      string
	grpcStatus  string
	grpcMessage string
	grpcDetails string
	jsonBody    string
}

func readCaddyErrorPage(t *testing.T) caddyErrorPage {
	t.Helper()
	raw, err := os.ReadFile("../../deploy/caddy/Caddyfile")
	require.NoError(t, err)
	var page caddyErrorPage
	inErrors := false
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "handle_errors ") {
			inErrors = true
			page.statuses = strings.Fields(strings.TrimSuffix(strings.TrimPrefix(trimmed, "handle_errors "), "{"))
			continue
		}
		if !inErrors {
			continue
		}
		name, value, _ := strings.Cut(trimmed, " ")
		switch name {
		case "header":
			key, v, _ := strings.Cut(value, " ")
			v = strings.Trim(v, `"`)
			switch key {
			case "X-Nipa-Maintenance":
				page.marker = v
			case "grpc-status":
				page.grpcStatus = v
			case "grpc-message":
				page.grpcMessage = v
			case "grpc-status-details-bin":
				page.grpcDetails = v
			}
		case "respond":
			if body, ok := strings.CutPrefix(value, "`"); ok {
				page.jsonBody = body[:strings.LastIndex(body, "`")]
			}
		}
	}
	require.True(t, inErrors, "deploy/caddy/Caddyfile has no handle_errors block")
	return page
}

func TestCaddyErrorPageMatchesServerRefusal(t *testing.T) {
	page := readCaddyErrorPage(t)
	require.Equal(t, []string{"502", "503", "504"}, page.statuses)
	require.Equal(t, "server;full", page.marker)
	require.Equal(t, strconv.Itoa(int(codes.Unavailable)), page.grpcStatus)

	bin, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(page.grpcDetails, "="))
	require.NoError(t, err)
	var pb spb.Status
	require.NoError(t, proto.Unmarshal(bin, &pb))
	refused, ok := maintenance.FromGRPC(status.FromProto(&pb).Err())
	require.True(t, ok, "grpc-status-details-bin must carry the maintenance ErrorInfo")
	require.Equal(t, maintenance.ScopeServer, refused.Info.Scope)
	require.Equal(t, maintenance.Full, refused.Info.Mode)
	require.NotEmpty(t, refused.Info.Message)
	require.Equal(t, refused.Error(), pb.GetMessage())
	require.Equal(t, refused.Error(), page.grpcMessage)

	var body struct {
		Error       string           `json:"error"`
		Maintenance maintenance.Info `json:"maintenance"`
	}
	require.NoError(t, json.Unmarshal([]byte(page.jsonBody), &body))
	require.Equal(t, refused.Error(), body.Error)
	require.Equal(t, refused.Info, body.Maintenance)
}

func TestCaddyMaintenancePageIsSelfContained(t *testing.T) {
	raw, err := os.ReadFile("../../deploy/caddy/maintenance/index.html")
	require.NoError(t, err)
	html := strings.ToLower(string(raw))
	require.Contains(t, html, "<title>")
	for _, external := range []string{"src=", "<link", "@import", "url("} {
		require.NotContains(t, html, external)
	}
}
