package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/spf13/cobra"
)

const (
	permissionRead  uint64 = 1
	permissionWrite uint64 = 2
	permissionLock  uint64 = 4
	permissionAdmin uint64 = 1 << 16
)

func (c *Cli) setupAclCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "acl",
		Short:         "Manage path-based access rules",
		Long:          "List, grant and revoke path-based access rules on the server. Requires project admin rights. The organization and project are read from the current working copy.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(c.setupAclListCmd())
	cmd.AddCommand(c.setupAclGrantCmd())
	cmd.AddCommand(c.setupAclRevokeCmd())
	cmd.AddCommand(c.setupAclMyCmd())
	cmd.AddCommand(c.setupPermissionCmd())
	return cmd
}

func (c *Cli) setupAclListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List access rules",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			rules, err := c.useCase.Permission().Rules(context.Background(), host, org, project)
			if err != nil {
				return err
			}
			if len(rules) == 0 {
				cmd.Println("no access rules")
				return nil
			}
			for _, rule := range rules {
				subject := "user:" + rule.UserID
				if rule.GroupID != "" {
					subject = "group:" + rule.GroupID
				}
				path := rule.PathPrefix
				if path == "" {
					path = "/"
				}
				cmd.Printf("%d\t%s\t%s\t%s\n", rule.ID, subject, path, formatPermission(rule.Permission))
			}
			return nil
		},
	}
}

func (c *Cli) setupAclGrantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "grant",
		Short:         "Grant a rule to a user or group",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			user, _ := cmd.Flags().GetString("user")
			group, _ := cmd.Flags().GetString("group")
			path, _ := cmd.Flags().GetString("path")
			rawPermission, _ := cmd.Flags().GetString("permission")
			if (user == "") == (group == "") {
				return domain.NewUserError("exactly one of --user or --group is required")
			}
			permission, err := parsePermission(rawPermission)
			if err != nil {
				return err
			}
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			rule, err := c.useCase.Permission().Grant(context.Background(), host, org, project, user, group, path, permission)
			if err != nil {
				return err
			}
			cmd.Printf("Granted rule %d (%s on %q)\n", rule.ID, formatPermission(rule.Permission), rule.PathPrefix)
			return nil
		},
	}
	cmd.Flags().String("user", "", "User ID (base36) to grant")
	cmd.Flags().String("group", "", "Group ID (base36) to grant")
	cmd.Flags().String("path", "", "Directory prefix the rule applies to (empty = whole project)")
	cmd.Flags().String("permission", "", "Comma-separated permissions: read,write,lock,admin")
	_ = cmd.MarkFlagRequired("permission")
	return cmd
}

func (c *Cli) setupAclRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "revoke",
		Short:         "Delete a rule by id",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			id, _ := cmd.Flags().GetInt64("id")
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			if err := c.useCase.Permission().Revoke(context.Background(), host, org, project, id); err != nil {
				return err
			}
			cmd.Printf("Deleted rule %d\n", id)
			return nil
		},
	}
	cmd.Flags().Int64("id", 0, "Rule id to delete")
	_ = cmd.MarkFlagRequired("id")
	return cmd
}

func (c *Cli) setupAclMyCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "my",
		Short:         "Show the caller's effective permissions",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			info, err := c.useCase.Permission().My(context.Background(), host, org, project)
			if err != nil {
				return err
			}
			cmd.Printf("project: %s\n", formatPermission(info.ProjectPermission))
			for _, entry := range info.Rules {
				cmd.Printf("rule\t%s\t%s\n", pathOrRoot(entry.PathPrefix), formatPermission(entry.Permission))
			}
			for _, entry := range info.Defaults {
				cmd.Printf("default\t%s\t%s\n", pathOrRoot(entry.PathPrefix), formatPermission(entry.Permission))
			}
			return nil
		},
	}
}

func (c *Cli) setupPermissionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "permission",
		Short:         "Manage project path defaults",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.AddCommand(c.setupPermissionListCmd())
	cmd.AddCommand(c.setupPermissionSetCmd())
	cmd.AddCommand(c.setupPermissionRemoveCmd())
	return cmd
}

func (c *Cli) setupPermissionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "list",
		Short:         "List project path defaults",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			entries, err := c.useCase.Permission().PathPermissions(context.Background(), host, org, project)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				cmd.Println("no path defaults")
				return nil
			}
			for _, entry := range entries {
				cmd.Printf("%s\t%s\n", pathOrRoot(entry.PathPrefix), formatPermission(entry.Permission))
			}
			return nil
		},
	}
}

func (c *Cli) setupPermissionSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "set",
		Short:         "Set the default permission for a path prefix",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("path")
			rawPermission, _ := cmd.Flags().GetString("permission")
			permission, err := parsePermission(rawPermission)
			if err != nil {
				return err
			}
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			entry, err := c.useCase.Permission().SetPath(context.Background(), host, org, project, path, permission)
			if err != nil {
				return err
			}
			cmd.Printf("Set %s default on %s\n", formatPermission(entry.Permission), pathOrRoot(entry.PathPrefix))
			return nil
		},
	}
	cmd.Flags().String("path", "", "Directory prefix (empty = whole project)")
	cmd.Flags().String("permission", "", "Comma-separated permissions: read,write,lock,admin")
	_ = cmd.MarkFlagRequired("permission")
	return cmd
}

func (c *Cli) setupPermissionRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "remove",
		Short:         "Remove the default permission for a path prefix",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("path")
			host, org, project, err := c.repoScope()
			if err != nil {
				return err
			}
			if err := c.useCase.Permission().RemovePath(context.Background(), host, org, project, path); err != nil {
				return err
			}
			cmd.Printf("Removed default on %s\n", pathOrRoot(path))
			return nil
		},
	}
	cmd.Flags().String("path", "", "Directory prefix (empty = whole project)")
	_ = cmd.MarkFlagRequired("path")
	return cmd
}

func (c *Cli) repoScope() (host, org, project string, err error) {
	cfg, err := c.loadConfig()
	if err != nil {
		return "", "", "", err
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return "", "", "", err
	}
	return nipaUrl.Host, nipaUrl.Org, nipaUrl.Project, nil
}

func parsePermission(raw string) (uint64, error) {
	var permission uint64
	for _, part := range strings.Split(raw, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "":
			continue
		case "read":
			permission |= permissionRead
		case "write":
			permission |= permissionWrite
		case "lock":
			permission |= permissionLock
		case "admin":
			permission |= permissionAdmin
		default:
			return 0, domain.NewUserError(fmt.Sprintf("unknown permission %q", strings.TrimSpace(part)))
		}
	}
	if permission == 0 {
		return 0, domain.NewUserError("at least one permission is required")
	}
	return permission, nil
}

func formatPermission(permission uint64) string {
	var parts []string
	if permission&permissionRead != 0 {
		parts = append(parts, "read")
	}
	if permission&permissionWrite != 0 {
		parts = append(parts, "write")
	}
	if permission&permissionLock != 0 {
		parts = append(parts, "lock")
	}
	if permission&permissionAdmin != 0 {
		parts = append(parts, "admin")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ",")
}

func pathOrRoot(path string) string {
	if path == "" {
		return "/"
	}
	return path
}
