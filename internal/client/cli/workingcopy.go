package cli

import (
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/usecase"
)

func openWorkingCopy() (*usecase.WorkingCopy, func(), error) {
	root, err := localrepo.FindRepoRoot()
	if err != nil {
		return nil, nil, err
	}
	lr := localrepo.NewLocalRepo()
	wc, err := usecase.NewWorkingCopy(lr, root)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() { _ = lr.Close() }
	return wc, cleanup, nil
}
