package server

import (
	"context"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/nipalab/nipa/internal/grpc/pb"
	"github.com/nipalab/nipa/internal/maintenance"
	"github.com/nipalab/nipa/internal/obs"
	"github.com/nipalab/nipa/internal/snow"
)

var maintNow = time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

func describedMethods() []string {
	var out []string
	for _, desc := range []grpc.ServiceDesc{pb.NipaService_ServiceDesc, healthpb.Health_ServiceDesc} {
		for _, m := range desc.Methods {
			out = append(out, "/"+desc.ServiceName+"/"+m.MethodName)
		}
		for _, s := range desc.Streams {
			out = append(out, "/"+desc.ServiceName+"/"+s.StreamName)
		}
	}
	return out
}

func TestMethodClassesCoverServiceDesc(t *testing.T) {
	described := map[string]bool{}
	for _, method := range describedMethods() {
		described[method] = true
		_, ok := methodClass[method]
		require.True(t, ok, "%s has no maintenance class", method)
	}
	for method := range methodClass {
		require.True(t, described[method], "%s is classified but not served", method)
	}
	for method := range serverScopeOnly {
		require.Contains(t, []maintenance.Class{read, write}, methodClass[method], method)
	}
	require.Equal(t, maintenance.ClassWrite, classOf("/greet.NipaService/Unknown"))
}

func TestPublicAndAuthMethodsSkipJWT(t *testing.T) {
	for method, class := range methodClass {
		if class == public && method != "/grpc.health.v1.Health/List" {
			require.True(t, isPublicMethod(method), method)
		}
		if class == auth {
			require.True(t, isPublicMethod(method), method)
		}
	}
	require.True(t, exemptFromRateLimit(nipaSvc+"GetMaintenanceStatus"))
}

func TestMaintenanceTargetCoversEveryScopedMethod(t *testing.T) {
	svc := pb.File_internal_grpc_proto_server_proto.Services().ByName("NipaService")
	methods := svc.Methods()
	for i := 0; i < methods.Len(); i++ {
		md := methods.Get(i)
		full := nipaSvc + string(md.Name())
		class := methodClass[full]
		if class != read && class != write {
			continue
		}
		mt, err := protoregistry.GlobalTypes.FindMessageByName(md.Input().FullName())
		require.NoError(t, err)
		msg := mt.New()
		fields := msg.Descriptor().Fields()
		switch {
		case fields.ByName("context") != nil:
			fd := fields.ByName("context")
			pc := msg.Mutable(fd).Message()
			pc.Set(pc.Descriptor().Fields().ByName("org"), protoreflect.ValueOfString("acme"))
			pc.Set(pc.Descriptor().Fields().ByName("project"), protoreflect.ValueOfString("game"))
			org, project := maintenanceTarget(msg.Interface())
			require.Equal(t, "acme", org, full)
			require.Equal(t, "game", project, full)
		case fields.ByName("org") != nil:
			msg.Set(fields.ByName("org"), protoreflect.ValueOfString("acme"))
			org, project := maintenanceTarget(msg.Interface())
			require.Equal(t, "acme", org, full)
			require.Empty(t, project, full)
		default:
			require.True(t, serverScopeOnly[full], "%s names no org or project and is not listed in serverScopeOnly", full)
			org, project := maintenanceTarget(msg.Interface())
			require.Empty(t, org+project, full)
		}
	}
	org, project := maintenanceTarget(nil)
	require.Empty(t, org+project)
	org, project = maintenanceTarget(&pb.PushRequest{})
	require.Empty(t, org+project, "a missing context is server scope")
}

func maintSvc(t *testing.T, windows ...maintenance.Window) *maintenance.Service {
	t.Helper()
	svc := maintenance.NewService().WithClock(func() time.Time { return maintNow })
	require.NoError(t, svc.Apply(context.Background(), maintenance.NewSnapshot(windows, maintNow)))
	return svc
}

func serverWin(mode maintenance.Mode) maintenance.Window {
	return maintenance.Window{ID: 1, Mode: mode, Message: "upgrade", StartsAt: maintNow.Add(-time.Minute)}
}

func projectWin(org, project string, mode maintenance.Mode) maintenance.Window {
	id := snow.ID(10)
	return maintenance.Window{ID: 2, ProjectID: &id, Org: org, Project: project, Mode: mode, StartsAt: maintNow.Add(-time.Minute)}
}

func orgWin(org string, mode maintenance.Mode) maintenance.Window {
	id := snow.ID(20)
	return maintenance.Window{ID: 3, OrgID: &id, Org: org, Mode: mode, StartsAt: maintNow.Add(-time.Minute)}
}

func callUnary(m *Maintenance, method string, req any) error {
	_, err := m.Unary()(context.Background(), req, &grpc.UnaryServerInfo{FullMethod: nipaSvc + method}, okHandler)
	return err
}

func pctx(org, project string) *pb.ProjectContext {
	return &pb.ProjectContext{Org: org, Project: project}
}

func TestMaintenanceInterceptorRules(t *testing.T) {
	type call struct {
		method  string
		req     any
		allowed bool
	}
	cases := []struct {
		name    string
		windows []maintenance.Window
		calls   []call
	}{
		{name: "off", calls: []call{
			{"Push", &pb.PushRequest{Context: pctx("acme", "game")}, true},
		}},
		{name: "scheduled server window", windows: []maintenance.Window{{ID: 1, Mode: maintenance.Full, StartsAt: maintNow.Add(time.Hour)}}, calls: []call{
			{"Push", &pb.PushRequest{Context: pctx("acme", "game")}, true},
		}},
		{name: "server read_only", windows: []maintenance.Window{serverWin(maintenance.ReadOnly)}, calls: []call{
			{"GetCommit", &pb.GetCommitRequest{Context: pctx("acme", "game")}, true},
			{"ListMyProjects", &pb.ListMyProjectsRequest{}, true},
			{"Push", &pb.PushRequest{Context: pctx("acme", "game")}, false},
			{"LockFile", &pb.LockFileRequest{Context: pctx("acme", "game")}, false},
			{"AddMergeRequestComment", &pb.AddMergeRequestCommentRequest{Context: pctx("acme", "game")}, false},
			{"LoginWithUsernamePassword", &pb.LoginUsernamePasswordRequest{}, true},
			{"GetServerVersion", &pb.GetServerVersionRequest{}, true},
		}},
		{name: "server full", windows: []maintenance.Window{serverWin(maintenance.Full)}, calls: []call{
			{"GetCommit", &pb.GetCommitRequest{Context: pctx("acme", "game")}, false},
			{"ListMyProjects", &pb.ListMyProjectsRequest{}, false},
			{"LoginWithRefreshToken", &pb.LoginWithRefreshRequest{}, true},
			{"GetMaintenanceStatus", &pb.GetMaintenanceStatusRequest{}, true},
		}},
		{name: "server frozen is enforced as full", windows: []maintenance.Window{serverWin(maintenance.Frozen)}, calls: []call{
			{"GetCommit", &pb.GetCommitRequest{Context: pctx("acme", "game")}, false},
			{"CompleteLoginMFA", &pb.CompleteLoginMFARequest{}, true},
		}},
		{name: "project read_only", windows: []maintenance.Window{projectWin("acme", "game", maintenance.ReadOnly)}, calls: []call{
			{"Push", &pb.PushRequest{Context: pctx("acme", "game")}, false},
			{"Push", &pb.PushRequest{Context: pctx("acme", "other")}, true},
			{"GetTreeManifest", &pb.GetTreeManifestRequest{Context: pctx("acme", "game")}, true},
			{"CreateGroup", &pb.CreateGroupRequest{Org: "acme"}, true},
		}},
		{name: "project full", windows: []maintenance.Window{projectWin("acme", "game", maintenance.Full)}, calls: []call{
			{"GetTreeManifest", &pb.GetTreeManifestRequest{Context: pctx("acme", "game")}, false},
			{"ListMyProjects", &pb.ListMyProjectsRequest{}, true},
		}},
		{name: "org full", windows: []maintenance.Window{orgWin("beta", maintenance.Full)}, calls: []call{
			{"ListGroups", &pb.ListGroupsRequest{Org: "beta"}, false},
			{"CreateGroup", &pb.CreateGroupRequest{Org: "beta"}, false},
			{"GetCommit", &pb.GetCommitRequest{Context: pctx("beta", "game")}, false},
			{"ListGroups", &pb.ListGroupsRequest{Org: "acme"}, true},
			{"ListMyProjects", &pb.ListMyProjectsRequest{}, true},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMaintenance(maintSvc(t, tc.windows...), nil)
			for _, c := range tc.calls {
				err := callUnary(m, c.method, c.req)
				if c.allowed {
					require.NoError(t, err, c.method)
					continue
				}
				require.Equal(t, codes.Unavailable, status.Code(err), c.method)
				_, ok := maintenance.FromGRPC(err)
				require.True(t, ok, c.method)
			}
		})
	}
}

func TestMaintenanceInterceptorRejection(t *testing.T) {
	end := maintNow.Add(20 * time.Minute)
	w := projectWin("acme", "game", maintenance.ReadOnly)
	w.Message, w.EndsAt = "lfs repair", &end
	metrics := obs.NewMetrics()
	m := NewMaintenance(maintSvc(t, w), metrics)

	err := callUnary(m, "Push", &pb.PushRequest{Context: pctx("acme", "game")})
	st := status.Convert(err)
	require.Equal(t, codes.Unavailable, st.Code())
	require.Equal(t, "project acme/game is in maintenance (read-only): lfs repair", st.Message())
	info := st.Details()[0].(*errdetails.ErrorInfo)
	require.Equal(t, maintenance.Reason, info.GetReason())
	require.Equal(t, "nipa", info.GetDomain())
	require.Equal(t, map[string]string{
		"scope": "project", "mode": "read_only", "message": "lfs repair", "org": "acme", "project": "acme/game",
		"starts_at": "2026-11-15T09:59:00Z", "ends_at": "2026-11-15T10:20:00Z",
	}, info.GetMetadata())
	require.Equal(t, 20*time.Minute, st.Details()[1].(*errdetails.RetryInfo).GetRetryDelay().AsDuration())

	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	require.Contains(t, string(body), `nipa_maintenance_rejected_total{class="write",scope="project",transport="grpc"} 1`)
}

func TestMaintenanceInterceptorStream(t *testing.T) {
	m := NewMaintenance(maintSvc(t, serverWin(maintenance.Full)), nil)
	called := false
	handler := func(any, grpc.ServerStream) error { called = true; return nil }
	require.NoError(t, m.Stream()(nil, nil, &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"}, handler))
	require.True(t, called)
	err := m.Stream()(nil, nil, &grpc.StreamServerInfo{FullMethod: nipaSvc + "Unknown"}, handler)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestMaintenanceInterceptorNilService(t *testing.T) {
	m := NewMaintenance(nil, nil)
	require.NoError(t, callUnary(m, "Push", &pb.PushRequest{}))
}
