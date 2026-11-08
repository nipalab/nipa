package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nipalab/nipa/internal/client/domain"
	serverDomain "github.com/nipalab/nipa/internal/domain"
)

func TestOperations_FailWhenRepoLockFails(t *testing.T) {
	lockErr := errors.New("lock failed")
	ctx := context.Background()
	cfg := func() *domain.Config {
		return &domain.Config{Url: "http://example.com/org/project", Branch: "main"}
	}
	local := func() *stubLocalRepo {
		return &stubLocalRepo{lockErr: lockErr, loadConfig: cfg(), snapshot: &domain.Snapshot{}}
	}
	cases := map[string]func(t *testing.T) error{
		"update": func(t *testing.T) error {
			return newTestUpdate(t, local(), &stubUpdateClient{}).Run(ctx, t.TempDir())
		},
		"switch": func(t *testing.T) error {
			return newTestUpdate(t, local(), &stubUpdateClient{}).Switch(ctx, t.TempDir(), "dev")
		},
		"switch tag": func(t *testing.T) error {
			return newTestUpdate(t, local(), &stubUpdateClient{}).SwitchTag(ctx, t.TempDir(), "v1")
		},
		"clone": func(t *testing.T) error {
			auth := NewAuth(nil, &stubSecureStorage{loadResult: &domain.LoginResult{AccessToken: signTestToken(t, "secret")}}, nil)
			repo := NewRepo(auth, &stubRepoInterface{
				defaultBranch: &serverDomain.Branch{Name: "main"},
				manifest:      &serverDomain.TreeNode{},
			}, local())
			return repo.Clone(ctx, "http://example.com/org/project", "example.com", "org", "project", "", nil, t.TempDir())
		},
		"push": func(t *testing.T) error {
			return newTestPush(t, local(), &stubPushClient{}).Run(ctx, t.TempDir(), "msg")
		},
		"push plan": func(t *testing.T) error {
			_, err := newTestPush(t, local(), &stubPushClient{}).Plan(ctx, t.TempDir())
			return err
		},
		"merge": func(t *testing.T) error {
			l := local()
			_, err := newTestMerge(t, &stubMergeClient{}, l, newTestPush(t, l, &stubPushClient{})).Run(ctx, t.TempDir(), "dev", MergeOptions{})
			return err
		},
		"revert": func(t *testing.T) error {
			r, _ := newTestRevert(t, &stubRevertClient{}, local())
			_, err := r.Run(ctx, t.TempDir(), "abc", RevertOptions{})
			return err
		},
		"restore": func(t *testing.T) error {
			return newWorkingCopy(t, local(), t.TempDir()).Restore(ctx, []string{"a"})
		},
		"diff": func(t *testing.T) error {
			repo := newDiffStub()
			repo.lockErr = lockErr
			_, err := NewDiff(nil, nil, repo).Run(ctx, t.TempDir(), nil, DiffOptions{})
			return err
		},
		"read blob": func(t *testing.T) error {
			repo := newDiffStub()
			repo.lockErr = lockErr
			_, err := readBlob(t, NewDiff(nil, nil, repo), t.TempDir(), BlobRequest{Path: "a"})
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, run(t), lockErr)
		})
	}
}
