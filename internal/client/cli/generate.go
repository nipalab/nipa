package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nipalab/nipa/internal/client/aicontext"
	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/generate"
)

func addGenerateFlags(cmd *cobra.Command, what string) {
	cmd.Flags().Bool("generate", false, "Draft the "+what+" with the LLM configured by NIPA_LLM_ENDPOINT/NIPA_LLM_MODEL or llm in ~/.config/nipa/config.json")
	cmd.Flags().BoolP("yes", "y", false, "Use the generated text without asking for confirmation")
}

func (c *Cli) collector(root string) (*aicontext.Collector, error) {
	d := c.useCase.Diff()
	if d == nil {
		return nil, domain.NewUserError("diff is not configured")
	}
	col := aicontext.New(d, root)
	if repo := c.useCase.Repo(); repo != nil {
		col.History = repo
	}
	if c.connector != nil {
		col.Connector = c.connector
	}
	return col, nil
}

func (c *Cli) generator() *generate.Generator {
	return &generate.Generator{Sources: func(root string) (generate.Sources, error) {
		return c.collector(root)
	}}
}

func (c *Cli) generateCommitMessage(cmd *cobra.Command, root string) (string, error) {
	res, err := c.generator().Commit(cmd.Context(), root)
	if err != nil {
		return "", err
	}
	return aiprompt.AddTrailer(res.Text, aiprompt.GeneratedTrailer, res.Model), nil
}

// confirmGenerated prints the draft and asks before it is used. --yes skips
// the question; without a terminal the question cannot be asked.
func (c *Cli) confirmGenerated(cmd *cobra.Command, label, text string) error {
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "%s:\n\n%s\n\n", label, text)
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	if !c.interactive(cmd.InOrStdin()) {
		return domain.NewUserError("refusing to use generated text without confirmation; pass --yes or run in a terminal").
			WithHint("review the draft above, then rerun with --yes", "")
	}
	ok, err := confirm(cmd.InOrStdin(), out, "Use it? [y/N] ")
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewUserError("cancelled; nothing was changed")
	}
	return nil
}

func (c *Cli) interactive(in io.Reader) bool {
	if c.terminal != nil {
		return c.terminal(in)
	}
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// generateMergeRequest drafts the title (kept when given) and description of
// a merge request and resolves the branches the draft was made for.
func (c *Cli) generateMergeRequest(cmd *cobra.Command, root, title, source, target string) (string, string, string, string, error) {
	res, err := c.generator().MergeRequest(cmd.Context(), root, source, target)
	if err != nil {
		return "", "", "", "", err
	}
	if strings.TrimSpace(title) == "" {
		title = res.Title
	}
	description := aiprompt.AddTrailer(res.Text, aiprompt.GeneratedTrailer, res.Model)
	if err := c.confirmGenerated(cmd, "Generated merge request", "Title: "+title+"\n\n"+description); err != nil {
		return "", "", "", "", err
	}
	return title, description, res.Source, res.Target, nil
}
