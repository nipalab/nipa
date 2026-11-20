package usecase

import (
	"fmt"

	clientDomain "github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/domain"
)

// validateSyncTree checks every directory and file name of a server tree
// before anything is written, so one unsafe name (a "..", a separator, the
// .nipa metadata directory, a Windows device name on Windows) fails the whole
// sync instead of escaping the working copy or replacing clone metadata. The
// root's own name is a label that never becomes part of a path.
func validateSyncTree(tree *domain.TreeNode) error {
	if tree == nil {
		return nil
	}
	return validateTreeChildren(tree, "")
}

func validateTreeChildren(node *domain.TreeNode, prefix string) error {
	for _, f := range node.FileChildren {
		if f == nil {
			continue
		}
		if err := checkTreeName(prefix, f.Name); err != nil {
			return err
		}
	}
	for _, child := range node.TreeChildren {
		if child == nil {
			continue
		}
		if err := checkTreeName(prefix, child.Name); err != nil {
			return err
		}
		if err := validateTreeChildren(child, prefix+child.Name+"/"); err != nil {
			return err
		}
	}
	return nil
}

func checkTreeName(prefix, name string) error {
	if err := domain.ValidateLocalPathSegment(name); err != nil {
		return unsafeTreePath(prefix+name, err)
	}
	return nil
}

func unsafeTreePath(path string, err error) error {
	return clientDomain.NewUserError(fmt.Sprintf("refusing to sync unsafe path %q from the server: %s", path, err.Error())).
		WithHint("the server tree has a name that cannot be written safely inside the working copy; nothing was written", "rename or remove the path on the server, then sync again")
}

// checkWorkingPath guards every write of a tracked path into the working
// copy, whatever tree or snapshot the path came from.
func checkWorkingPath(p string) error {
	if err := domain.ValidateLocalPath(p); err != nil {
		return unsafeTreePath(p, err)
	}
	return nil
}
