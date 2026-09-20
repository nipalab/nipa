package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func (c *Cli) setupGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "group",
		Short:         "Manage organization groups",
		Long:          "Create and list organization groups and manage their members. Requires organization admin rights. The organization is read from the current working copy.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(c.setupGroupCreateCmd())
	cmd.AddCommand(c.setupGroupListCmd())
	cmd.AddCommand(c.setupGroupAddMemberCmd())
	cmd.AddCommand(c.setupGroupRemoveMemberCmd())
	return cmd
}

func (c *Cli) setupGroupCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "create <name>",
		Short:         "Create a group",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			description, _ := cmd.Flags().GetString("description")
			host, org, _, err := c.repoScope()
			if err != nil {
				return err
			}
			group, err := c.useCase.Permission().CreateGroup(context.Background(), host, org, args[0], description)
			if err != nil {
				return err
			}
			cmd.Printf("Created group %s (%s)\n", group.Name, group.ID)
			return nil
		},
	}
	cmd.Flags().String("description", "", "Group description")
	return cmd
}

func (c *Cli) setupGroupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List groups",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, org, _, err := c.repoScope()
			if err != nil {
				return err
			}
			groups, err := c.useCase.Permission().Groups(context.Background(), host, org)
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				cmd.Println("no groups")
				return nil
			}
			for _, group := range groups {
				cmd.Printf("%s\t%s\t%s\n", group.ID, group.Name, group.Description)
			}
			return nil
		},
	}
}

func (c *Cli) setupGroupAddMemberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "add-member",
		Short:         "Add a user to a group",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, _ := cmd.Flags().GetString("group")
			userID, _ := cmd.Flags().GetString("user")
			host, org, _, err := c.repoScope()
			if err != nil {
				return err
			}
			if err := c.useCase.Permission().AddGroupMember(context.Background(), host, org, groupID, userID); err != nil {
				return err
			}
			cmd.Printf("Added user %s to group %s\n", userID, groupID)
			return nil
		},
	}
	cmd.Flags().String("group", "", "Group ID (base36)")
	cmd.Flags().String("user", "", "User ID (base36)")
	_ = cmd.MarkFlagRequired("group")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}

func (c *Cli) setupGroupRemoveMemberCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "remove-member",
		Short:         "Remove a user from a group",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			groupID, _ := cmd.Flags().GetString("group")
			userID, _ := cmd.Flags().GetString("user")
			host, org, _, err := c.repoScope()
			if err != nil {
				return err
			}
			if err := c.useCase.Permission().RemoveGroupMember(context.Background(), host, org, groupID, userID); err != nil {
				return err
			}
			cmd.Printf("Removed user %s from group %s\n", userID, groupID)
			return nil
		},
	}
	cmd.Flags().String("group", "", "Group ID (base36)")
	cmd.Flags().String("user", "", "User ID (base36)")
	_ = cmd.MarkFlagRequired("group")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}
