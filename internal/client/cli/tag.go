package cli

import (
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
	"github.com/spf13/cobra"
)

func (c *Cli) setupTagCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "tag [name]",
		Short:         "List, create or delete release tags",
		Long:          "List the tags of the project. Create a tag with -c, pointing at the checked-out commit by default or at --branch/--commit; add an annotation with -m. Delete a tag with -d.",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			create, _ := cmd.Flags().GetBool("create")
			remove, _ := cmd.Flags().GetBool("delete")
			if create && remove {
				return domain.NewUserError("--create and --delete are mutually exclusive")
			}
			message, _ := cmd.Flags().GetString("message")
			branch, _ := cmd.Flags().GetString("branch")
			commit, _ := cmd.Flags().GetString("commit")
			if !create && (message != "" || branch != "" || commit != "") {
				return domain.NewUserError("--message, --branch and --commit require --create")
			}
			if jsonRequested(cmd) && (create || remove) {
				return domain.NewUserError("--json cannot be combined with --create or --delete")
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			switch {
			case create:
				if len(args) != 1 {
					return domain.NewUserError("a tag name is required: nipa tag -c <name>")
				}
				tag, err := c.useCase.Tag().Create(cmd.Context(), root, args[0], message, usecase.TagTarget{Branch: branch, Commit: commit})
				if err != nil {
					return err
				}
				cmd.Printf("Created tag %q at commit %s.\n", tag.Name, tag.CommitID)
				return nil
			case remove:
				if len(args) != 1 {
					return domain.NewUserError("a tag name is required: nipa tag -d <name>")
				}
				if err := c.useCase.Tag().Delete(cmd.Context(), root, args[0]); err != nil {
					return err
				}
				cmd.Printf("Deleted tag %q.\n", args[0])
				return nil
			default:
				if len(args) != 0 {
					return domain.NewUserError("to create a tag use nipa tag -c <name>; to delete one use nipa tag -d <name>")
				}
				return c.listTags(cmd, root)
			}
		},
	}
	cmd.Flags().BoolP("create", "c", false, "Create a tag pointing at the checked-out commit")
	cmd.Flags().BoolP("delete", "d", false, "Delete a tag")
	cmd.Flags().StringP("message", "m", "", "Tag annotation message (with --create)")
	cmd.Flags().String("branch", "", "Tag the head of this branch instead of the checked-out commit")
	cmd.Flags().String("commit", "", "Tag this commit instead of the checked-out commit (base36 id or hex hash)")
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) listTags(cmd *cobra.Command, root string) error {
	tags, err := c.useCase.Tag().List(cmd.Context(), root)
	if err != nil {
		return err
	}
	if jsonRequested(cmd) {
		return output.WriteJSON(cmd.OutOrStdout(), output.NewTags(tags))
	}
	if len(tags) == 0 {
		cmd.Println("no tags")
		return nil
	}
	cmd.Printf("%-32s  %-16s  %-20s  %s\n", "NAME", "COMMIT", "CREATED", "MESSAGE")
	for _, tag := range tags {
		cmd.Printf("%-32s  %-16s  %-20s  %s\n",
			tag.Name, tag.CommitID, tag.CreatedAt.UTC().Format(time.RFC3339), tag.Message)
	}
	return nil
}
