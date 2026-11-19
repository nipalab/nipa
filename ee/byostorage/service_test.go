package byostorage

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	s3store "github.com/nipalab/nipa/ee/storage/s3"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

func strPtr(s string) *string { return &s }

func TestNewRejectsShortKey(t *testing.T) {
	_, err := New(newStubRepo(), &stubOwners{}, &stubTx{}, nil, Config{Key: []byte("short")})
	require.Error(t, err)
}

func TestCreate_ProbesSealsAndAudits(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)

	require.Equal(t, ownerID, server.OwnerUserID)
	require.Equal(t, "https://acct.r2.cloudflarestorage.com", server.Endpoint)
	require.Equal(t, "nipa", server.Prefix)
	require.NotContains(t, string(server.SecretSealed), "secret")

	require.Len(t, f.opener.opened, 1)
	probed := f.opener.opened[0]
	require.True(t, probed.closed)
	require.Equal(t, "secret", probed.cfg.SecretAccessKey)
	require.True(t, probed.cfg.SkipProbe)
	require.NotNil(t, probed.cfg.Transport)
	require.Equal(t, "assets", probed.cfg.Bucket)

	secret, err := f.svc.openSecret(server)
	require.NoError(t, err)
	require.Equal(t, "secret", secret)
	other := *server
	other.ID = server.ID + 1
	_, err = f.svc.openSecret(&other)
	require.Equal(t, 500, errorCode(err), "the secret is bound to its server id")

	require.Equal(t, []string{domain.AuditStorageServerCreated}, f.audit.actions())
	entry := f.audit.entries[0]
	require.Equal(t, domain.AuditTargetStorageServer, entry.TargetType)
	require.Equal(t, server.ID.Base36(), entry.TargetID)
	for _, value := range entry.Metadata {
		require.NotEqual(t, "secret", value)
	}
	require.Equal(t, 1, f.repo.calls["LockOwner"])
}

func TestCreate_Validation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ServerInput)
		want   string
	}{
		{"name", func(in *ServerInput) { in.Name = " " }, "name is required"},
		{"endpoint", func(in *ServerInput) { in.Endpoint = "http://s3.example.com" }, "https"},
		{"bucket", func(in *ServerInput) { in.Bucket = "Bad_Bucket" }, "bucket"},
		{"prefix", func(in *ServerInput) { in.Prefix = "a/../b" }, "prefix"},
		{"access key", func(in *ServerInput) { in.AccessKeyID = "" }, "access_key_id"},
		{"secret", func(in *ServerInput) { in.SecretAccessKey = " " }, "secret_access_key"},
		{"region", func(in *ServerInput) { in.Region = string(make([]byte, 65)) }, "region"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			in := validInput()
			tc.mutate(&in)
			_, err := f.svc.Create(as(ownerID), in)
			require.Equal(t, 400, errorCode(err))
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, f.opener.opened, "nothing is probed before validation")
		})
	}
}

func TestCreate_ProbeFailure(t *testing.T) {
	f := newFixture(t)
	f.opener.probeErr = &s3store.ProbeError{Check: s3store.ProbeCreateOnly, Err: errors.New("answered 200")}
	_, err := f.svc.Create(as(ownerID), validInput())
	var failure *ProbeFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, s3store.ProbeCreateOnly, failure.Check)
	require.ErrorContains(t, err, "conditional_create")
	require.Empty(t, f.repo.servers)

	f.opener.probeErr = errBoom
	_, err = f.svc.Create(as(ownerID), validInput())
	require.ErrorAs(t, err, &failure)
	require.Equal(t, ProbeOpen, failure.Check)
	require.ErrorIs(t, err, errBoom)

	f.opener.probeErr = nil
	f.opener.openErr = errBoom
	_, err = f.svc.Create(as(ownerID), validInput())
	require.ErrorAs(t, err, &failure)
	require.Equal(t, ProbeOpen, failure.Check)
}

func TestCreate_LimitsAndConflicts(t *testing.T) {
	f := newFixture(t)
	f.create(t)
	_, err := f.svc.Create(as(ownerID), validInput())
	require.Equal(t, 409, errorCode(err))
	require.ErrorContains(t, err, "name already exists")

	for i := len(f.repo.servers); i < MaxServersPerOwner; i++ {
		in := validInput()
		in.Name = fmt.Sprintf("server-%d", i)
		_, err := f.svc.Create(as(ownerID), in)
		require.NoError(t, err)
	}
	in := validInput()
	in.Name = "one too many"
	_, err = f.svc.Create(as(ownerID), in)
	require.Equal(t, 400, errorCode(err))
	require.ErrorContains(t, err, "at most 20")

	_, err = f.svc.Create(as(otherID), in)
	require.NoError(t, err, "the limit is per owner")
}

func TestCreate_Errors(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Create(context.Background(), validInput())
	require.Equal(t, 401, errorCode(err))

	f.svc.WithEntitlement(stubEntitlement{active: false})
	_, err = f.svc.Create(as(ownerID), validInput())
	require.Equal(t, 402, errorCode(err))

	f.svc.WithEntitlement(stubEntitlement{err: errBoom})
	_, err = f.svc.Create(as(ownerID), validInput())
	require.ErrorIs(t, err, errBoom)

	f.svc.WithEntitlement(stubEntitlement{active: true})
	for _, method := range []string{"LockOwner", "CountServers", "CreateServer"} {
		f.repo.fail = map[string]error{method: errBoom}
		_, err = f.svc.Create(as(ownerID), validInput())
		require.ErrorIs(t, err, errBoom, method)
	}
	f.repo.fail = map[string]error{}
	f.audit.err = errBoom
	_, err = f.svc.Create(as(ownerID), validInput())
	require.ErrorIs(t, err, errBoom)
}

func TestListAndGet(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)

	list, err := f.svc.List(as(ownerID))
	require.NoError(t, err)
	require.Len(t, list, 1)
	list, err = f.svc.List(as(otherID))
	require.NoError(t, err)
	require.Empty(t, list)
	_, err = f.svc.List(context.Background())
	require.Equal(t, 401, errorCode(err))

	got, err := f.svc.Get(as(ownerID), server.ID)
	require.NoError(t, err)
	require.Equal(t, server.Name, got.Name)
	_, err = f.svc.Get(as(otherID), server.ID)
	require.Equal(t, 404, errorCode(err), "another user's server is not revealed")
	_, err = f.svc.Get(as(ownerID), 999)
	require.Equal(t, 404, errorCode(err))
	_, err = f.svc.Get(context.Background(), server.ID)
	require.Equal(t, 401, errorCode(err))
	f.repo.fail["Server"] = errBoom
	_, err = f.svc.Get(as(ownerID), server.ID)
	require.ErrorIs(t, err, errBoom)
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)

	renamed, err := f.svc.Update(as(ownerID), server.ID, ServerPatch{Name: strPtr("Renamed")})
	require.NoError(t, err)
	require.Equal(t, "Renamed", renamed.Name)
	require.Len(t, f.opener.opened, 1, "a rename does not probe")

	same, err := f.svc.Update(as(ownerID), server.ID, ServerPatch{Name: strPtr("Renamed")})
	require.NoError(t, err)
	require.Equal(t, renamed.UpdatedAt, same.UpdatedAt, "an unchanged patch writes nothing")

	moved, err := f.svc.Update(as(ownerID), server.ID, ServerPatch{Endpoint: strPtr("s3.eu.example.com")})
	require.NoError(t, err)
	require.Equal(t, "https://s3.eu.example.com", moved.Endpoint)
	require.Len(t, f.opener.opened, 2)
	require.Equal(t, "secret", f.opener.opened[1].cfg.SecretAccessKey, "the stored secret is probed")

	rotated, err := f.svc.Update(as(ownerID), server.ID, ServerPatch{AccessKeyID: strPtr("AKID2"), SecretAccessKey: strPtr("new-secret")})
	require.NoError(t, err)
	require.Equal(t, "new-secret", f.opener.opened[2].cfg.SecretAccessKey)
	secret, err := f.svc.openSecret(rotated)
	require.NoError(t, err)
	require.Equal(t, "new-secret", secret)

	require.Equal(t, []string{domain.AuditStorageServerCreated, domain.AuditStorageServerUpdated, domain.AuditStorageServerUpdated, domain.AuditStorageServerUpdated}, f.audit.actions())
	require.Equal(t, []string{"access_key_id", "secret_access_key"}, f.audit.entries[3].Metadata["changed"])
}

func TestUpdate_Errors(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	ctx := as(ownerID)

	_, err := f.svc.Update(context.Background(), server.ID, ServerPatch{})
	require.Equal(t, 401, errorCode(err))
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Bucket: strPtr("assets")})
	require.Equal(t, 400, errorCode(err))
	require.ErrorContains(t, err, "cannot be changed")
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Prefix: strPtr("x")})
	require.Equal(t, 400, errorCode(err))
	_, err = f.svc.Update(as(otherID), server.ID, ServerPatch{Name: strPtr("x")})
	require.Equal(t, 404, errorCode(err))
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Name: strPtr("")})
	require.Equal(t, 400, errorCode(err))
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{SecretAccessKey: strPtr(" ")})
	require.Equal(t, 400, errorCode(err))

	f.opener.probeErr = &s3store.ProbeError{Check: s3store.ProbeBucket, Err: errBoom}
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Region: strPtr("us-east-1")})
	var failure *ProbeFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, s3store.ProbeBucket, failure.Check)
	f.opener.probeErr = nil

	f.svc.WithEntitlement(stubEntitlement{active: false})
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Name: strPtr("x")})
	require.Equal(t, 402, errorCode(err))
	f.svc.WithEntitlement(nil)

	in := validInput()
	in.Name = "taken"
	_, err = f.svc.Create(ctx, in)
	require.NoError(t, err)
	f.repo.fail["UpdateServer"] = domain.NewErrorConflict("record already exists")
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Name: strPtr("taken")})
	require.ErrorContains(t, err, "name already exists")

	f.repo.fail = map[string]error{}
	f.repo.servers[server.ID].SecretSealed = []byte("garbage")
	_, err = f.svc.Update(ctx, server.ID, ServerPatch{Region: strPtr("eu")})
	require.Equal(t, 500, errorCode(err))
}

func TestDelete(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	ctx := as(ownerID)

	require.Equal(t, 401, errorCode(f.svc.Delete(context.Background(), server.ID)))
	require.Equal(t, 404, errorCode(f.svc.Delete(as(otherID), server.ID)))

	f.repo.inUse[server.ID] = true
	require.Equal(t, 409, errorCode(f.svc.Delete(ctx, server.ID)))
	f.repo.inUse[server.ID] = false

	f.repo.fail["ServerInUse"] = errBoom
	require.ErrorIs(t, f.svc.Delete(ctx, server.ID), errBoom)
	f.repo.fail = map[string]error{}

	f.svc.WithEntitlement(stubEntitlement{active: false})
	require.NoError(t, f.svc.Delete(ctx, server.ID), "a lapsed add-on can still clean up")
	require.Empty(t, f.repo.servers)
	require.Equal(t, domain.AuditStorageServerDeleted, f.audit.actions()[len(f.audit.entries)-1])
}

func TestTest(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	ctx := as(ownerID)

	require.NoError(t, f.svc.Test(ctx, server.ID))
	require.Equal(t, "secret", f.opener.opened[1].cfg.SecretAccessKey)

	f.opener.probeErr = &s3store.ProbeError{Check: s3store.ProbeDownload, Err: errBoom}
	var failure *ProbeFailure
	require.ErrorAs(t, f.svc.Test(ctx, server.ID), &failure)
	require.Equal(t, s3store.ProbeDownload, failure.Check)

	require.Equal(t, 401, errorCode(f.svc.Test(context.Background(), server.ID)))
	require.Equal(t, 404, errorCode(f.svc.Test(as(otherID), server.ID)))
	f.repo.servers[server.ID].SecretSealed = nil
	require.Equal(t, 500, errorCode(f.svc.Test(ctx, server.ID)))
}

func TestSetOrgDefault(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	ctx := as(ownerID)

	require.NoError(t, f.svc.SetOrgDefault(ctx, orgID, &server.ID))
	require.Equal(t, server.ID, f.repo.defaults[orgID])
	require.NoError(t, f.svc.SetOrgDefault(ctx, orgID, &server.ID))

	def, err := f.svc.OrgDefault(ctx, orgID)
	require.NoError(t, err)
	require.True(t, def.CanManage)
	require.Equal(t, server.ID, def.Server.ID)

	require.NoError(t, f.svc.SetOrgDefault(ctx, orgID, nil))
	require.NoError(t, f.svc.SetOrgDefault(ctx, orgID, nil))
	require.Empty(t, f.repo.defaults)

	require.Equal(t, []string{
		domain.AuditStorageServerCreated,
		domain.AuditOrgStorageDefaultSet,
		domain.AuditOrgStorageDefaultCleared,
	}, f.audit.actions(), "unchanged sets and empty clears are not audited")
	require.Equal(t, server.ID.Base36(), f.audit.entries[1].Metadata["storage_server_id"])
	require.Nil(t, f.audit.entries[1].Metadata["previous_server_id"])

	def, err = f.svc.OrgDefault(ctx, orgID)
	require.NoError(t, err)
	require.Nil(t, def.Server)
}

func TestSetOrgDefault_Refusals(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	ctx := as(ownerID)

	require.Equal(t, 401, errorCode(f.svc.SetOrgDefault(context.Background(), orgID, &server.ID)))

	f.svc.WithOrgs(stubOrgs{member: true})
	require.Equal(t, 403, errorCode(f.svc.SetOrgDefault(ctx, orgID, &server.ID)), "org owners only")
	require.Equal(t, 403, errorCode(f.svc.SetOrgDefault(ctx, orgID, nil)))
	f.svc.WithOrgs(stubOrgs{err: errBoom})
	require.ErrorIs(t, f.svc.SetOrgDefault(ctx, orgID, &server.ID), errBoom)
	f.svc.WithOrgs(nil)
	require.Equal(t, 403, errorCode(f.svc.SetOrgDefault(ctx, orgID, &server.ID)))
	f.svc.WithOrgs(stubOrgs{owner: true, member: true})

	require.Equal(t, 404, errorCode(f.svc.SetOrgDefault(as(otherID), orgID, &server.ID)), "only the caller's own server")

	f.owners.billing[orgID] = otherID
	require.Equal(t, 403, errorCode(f.svc.SetOrgDefault(ctx, orgID, &server.ID)), "the caller must be the billing owner")
	f.owners.billing[orgID] = ownerID

	f.owners.err = errBoom
	require.ErrorIs(t, f.svc.SetOrgDefault(ctx, orgID, &server.ID), errBoom)
	f.owners.err = nil

	f.svc.WithEntitlement(stubEntitlement{active: false})
	require.Equal(t, 402, errorCode(f.svc.SetOrgDefault(ctx, orgID, &server.ID)))
	f.svc.WithEntitlement(nil)

	f.repo.fail["OrgDefault"] = errBoom
	require.ErrorIs(t, f.svc.SetOrgDefault(ctx, orgID, &server.ID), errBoom)
	f.repo.fail = map[string]error{"ClearOrgDefault": errBoom}
	require.ErrorIs(t, f.svc.SetOrgDefault(ctx, orgID, nil), errBoom)
	require.Empty(t, f.repo.defaults)
}

func TestOrgDefault_Reads(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)
	f.repo.defaults[orgID] = server.ID

	_, err := f.svc.OrgDefault(context.Background(), orgID)
	require.Equal(t, 401, errorCode(err))

	f.svc.WithOrgs(stubOrgs{member: true})
	def, err := f.svc.OrgDefault(as(otherID), orgID)
	require.NoError(t, err)
	require.False(t, def.CanManage)
	require.Equal(t, server.Name, def.Server.Name)

	f.svc.WithOrgs(stubOrgs{owner: true, member: true})
	def, err = f.svc.OrgDefault(as(otherID), orgID)
	require.NoError(t, err)
	require.False(t, def.CanManage, "an org owner who is not the billing owner cannot manage it")
	require.Zero(t, f.owners.seeded, "reads never seed the billing owner")

	f.svc.WithOrgs(stubOrgs{})
	_, err = f.svc.OrgDefault(as(otherID), orgID)
	require.Equal(t, 403, errorCode(err))

	f.svc.WithOrgs(stubOrgs{owner: true, member: true})
	f.owners.err = errBoom
	_, err = f.svc.OrgDefault(as(ownerID), orgID)
	require.ErrorIs(t, err, errBoom)
	f.owners.err = nil

	f.repo.fail["OrgDefault"] = errBoom
	_, err = f.svc.OrgDefault(as(ownerID), orgID)
	require.ErrorIs(t, err, errBoom)
	f.repo.fail = map[string]error{"Server": errBoom}
	_, err = f.svc.OrgDefault(as(ownerID), orgID)
	require.ErrorIs(t, err, errBoom)
}

func TestAssign_Requested(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)

	require.NoError(t, f.svc.Assign(as(ownerID), orgID, project1, &server.ID))
	require.Equal(t, server.ID, f.repo.projects[project1])
	entry := f.audit.entries[len(f.audit.entries)-1]
	require.Equal(t, domain.AuditProjectStorageServerSet, entry.Action)
	require.Equal(t, "requested", entry.Metadata["source"])
	require.Equal(t, project1, *entry.ProjectID)

	got, err := f.svc.ProjectServer(context.Background(), project1)
	require.NoError(t, err)
	require.Equal(t, server.ID, got.ID)
}

func TestAssign_RequestedRefusals(t *testing.T) {
	f := newFixture(t)
	server := f.create(t)

	require.Equal(t, 401, errorCode(f.svc.Assign(context.Background(), orgID, project1, &server.ID)))
	require.Equal(t, 404, errorCode(f.svc.Assign(as(otherID), orgID, project1, &server.ID)), "another user's server")
	missing := snow.ID(999)
	require.Equal(t, 404, errorCode(f.svc.Assign(as(ownerID), orgID, project1, &missing)))

	f.owners.billing[orgID] = otherID
	require.Equal(t, 403, errorCode(f.svc.Assign(as(ownerID), orgID, project1, &server.ID)), "no cross-owner assignment")
	f.owners.billing[orgID] = ownerID

	f.owners.err = errBoom
	require.ErrorIs(t, f.svc.Assign(as(ownerID), orgID, project1, &server.ID), errBoom)
	f.owners.err = nil

	f.svc.WithEntitlement(stubEntitlement{active: false})
	require.Equal(t, 402, errorCode(f.svc.Assign(as(ownerID), orgID, project1, &server.ID)))
	f.svc.WithEntitlement(stubEntitlement{err: errBoom})
	require.ErrorIs(t, f.svc.Assign(as(ownerID), orgID, project1, &server.ID), errBoom)
	f.svc.WithEntitlement(nil)

	f.repo.fail["AssignProject"] = errBoom
	require.ErrorIs(t, f.svc.Assign(as(ownerID), orgID, project1, &server.ID), errBoom)
	f.repo.fail = map[string]error{}
	f.audit.err = errBoom
	require.ErrorIs(t, f.svc.Assign(as(ownerID), orgID, project1, &server.ID), errBoom)
	require.NoError(t, f.svc.WithAudit(nil).Assign(as(ownerID), orgID, snow.ID(31), &server.ID))
}

func TestAssign_OrgDefault(t *testing.T) {
	f := newFixture(t)

	require.NoError(t, f.svc.Assign(as(ownerID), orgID, project1, nil))
	require.Empty(t, f.repo.projects, "no default keeps the server's own storage")

	server := f.create(t)
	f.repo.defaults[orgID] = server.ID
	require.NoError(t, f.svc.Assign(as(otherID), orgID, project1, nil), "any org admin's project follows the default")
	require.Equal(t, server.ID, f.repo.projects[project1])
	require.Equal(t, "org_default", f.audit.entries[len(f.audit.entries)-1].Metadata["source"])

	f.owners.billing[orgID] = otherID
	require.NoError(t, f.svc.Assign(as(otherID), orgID, snow.ID(31), nil))
	require.NotContains(t, f.repo.projects, snow.ID(31), "a default of a former billing owner is ignored")
	f.owners.billing[orgID] = ownerID

	f.svc.WithEntitlement(stubEntitlement{active: false})
	require.Equal(t, 402, errorCode(f.svc.Assign(as(otherID), orgID, snow.ID(32), nil)))
	f.svc.WithEntitlement(nil)

	for _, method := range []string{"OrgDefault", "Server"} {
		f.repo.fail = map[string]error{method: errBoom}
		require.ErrorIs(t, f.svc.Assign(as(otherID), orgID, snow.ID(33), nil), errBoom, method)
	}
	f.repo.fail = map[string]error{}
	f.owners.err = errBoom
	require.ErrorIs(t, f.svc.Assign(as(otherID), orgID, snow.ID(33), nil), errBoom)
}

func TestProbeFailureError(t *testing.T) {
	failure := &ProbeFailure{Check: "stat", Err: errBoom}
	require.Equal(t, "storage server check stat failed: boom", failure.Error())
	require.ErrorIs(t, failure, errBoom)
}
