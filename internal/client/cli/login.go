package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/sshkey"
)

func (c *Cli) setupLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to a nipa server",
		Long: "Authenticate against a nipa server and store the session token for the host.\n" +
			"A registered SSH key logs in without prompting (--ssh-key, NIPA_SSH_KEY, the\n" +
			"sshKeyPath user config, ssh-agent or ~/.ssh): the key identifies its account,\n" +
			"like git. Password login prompts for the email and password instead. The\n" +
			"server defaults to the host of the enclosing working copy.",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, err := c.resolveLoginHost(cmd)
			if err != nil {
				return err
			}
			host := endpoint.Host
			prompter := NewPrompter()

			username, _ := cmd.Flags().GetString("username")
			username = strings.TrimSpace(username)

			if err := c.connector.Connect(cmd.Context(), endpoint.Address); err != nil {
				return err
			}

			sshKey, _ := cmd.Flags().GetString("ssh-key")
			signers, err := sshkey.Resolve(sshKey, prompter.PromptPassphrase)
			if err != nil {
				return err
			}
			if len(signers) > 0 {
				err := c.useCase.Auth().LoginWithSSHKey(cmd.Context(), endpoint.Address, username, signers)
				if err == nil {
					if username != "" {
						cmd.Printf("Logged in to %s as %s using SSH key\n", host, username)
					} else {
						cmd.Printf("Logged in to %s using SSH key\n", host)
					}
					return nil
				}
				if sshKey != "" || os.Getenv(sshkey.EnvSSHKey) != "" {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "SSH key login failed: %v\n", err)
			}

			if username == "" {
				username, err = prompter.PromptUsername()
				if err != nil {
					return err
				}
			}
			if username == "" {
				return domain.NewUserError("username cannot be empty")
			}

			password, err := prompter.PromptPassword()
			if err != nil {
				return err
			}
			if err := c.useCase.Auth().LoginWithUsernamePassword(cmd.Context(), endpoint.Address, username, password); err != nil {
				return err
			}
			cmd.Printf("Logged in to %s as %s\n", host, username)
			return nil
		},
	}
	cmd.Flags().String("username", "", "Email address for password login (prompted when omitted; SSH keys identify their account)")
	cmd.Flags().String("ssh-key", "", "SSH private key path (defaults to NIPA_SSH_KEY, the sshKeyPath user config, ssh-agent, then ~/.ssh keys)")
	cmd.Flags().String("host", "", "Nipa server URL or host (defaults to the host of the enclosing working copy)")
	return cmd
}

func (c *Cli) resolveLoginHost(cmd *cobra.Command) (domain.Endpoint, error) {
	if raw, _ := cmd.Flags().GetString("host"); raw != "" {
		return domain.ParseEndpoint(raw)
	}
	cfg, err := c.loadConfig()
	if err != nil {
		return domain.Endpoint{}, domain.NewUserError("host is required: pass --host or run inside a nipa working copy")
	}
	nipaUrl, err := domain.ParseNipaUrl(cfg.Url)
	if err != nil {
		return domain.Endpoint{}, err
	}
	return domain.Endpoint{Address: nipaUrl.Origin, Host: nipaUrl.Host}, nil
}
