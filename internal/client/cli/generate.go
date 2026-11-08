package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/nipalab/nipa/internal/client/aicontext"
	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/config"
	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/llm"
)

type generator interface {
	Complete(ctx context.Context, p aiprompt.Prompt) (string, error)
	Model() string
}

func addGenerateFlags(cmd *cobra.Command, what string) {
	cmd.Flags().Bool("generate", false, "Draft the "+what+" with the LLM configured by NIPA_LLM_ENDPOINT/NIPA_LLM_MODEL or llm in ~/.config/nipa/config.json")
	cmd.Flags().BoolP("yes", "y", false, "Use the generated text without asking for confirmation")
}

func newGenerator() (generator, error) {
	settings, err := config.ResolveLLM()
	if err != nil {
		return nil, err
	}
	return llm.New(settings)
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

// generateText asks the model for one answer and appends nothing; callers
// add the Nipa-Generated trailer where the text lands.
func generateText(ctx context.Context, gen generator, p aiprompt.Prompt) (string, error) {
	text, err := gen.Complete(ctx, p)
	if err != nil {
		return "", err
	}
	text = aiprompt.CleanOutput(text)
	if text == "" {
		return "", domain.NewUserError("the LLM returned an empty answer")
	}
	return text, nil
}

func (c *Cli) generateCommitMessage(cmd *cobra.Command, root string) (string, error) {
	gen, err := newGenerator()
	if err != nil {
		return "", err
	}
	col, err := c.collector(root)
	if err != nil {
		return "", err
	}
	in, err := col.Commit(cmd.Context(), root)
	if err != nil {
		return "", err
	}
	text, err := generateText(cmd.Context(), gen, aiprompt.CommitMessage(*in))
	if err != nil {
		return "", err
	}
	return aiprompt.AddTrailer(text, aiprompt.GeneratedTrailer, gen.Model()), nil
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
	gen, err := newGenerator()
	if err != nil {
		return "", "", "", "", err
	}
	col, err := c.collector(root)
	if err != nil {
		return "", "", "", "", err
	}
	in, err := col.MRDescription(cmd.Context(), root, source, target)
	if err != nil {
		return "", "", "", "", err
	}
	text, err := generateText(cmd.Context(), gen, aiprompt.MRDescription(*in))
	if err != nil {
		return "", "", "", "", err
	}
	genTitle, body := aiprompt.SplitTitle(text)
	if strings.TrimSpace(title) == "" {
		title = genTitle
	}
	description := aiprompt.AddTrailer(body, aiprompt.GeneratedTrailer, gen.Model())
	if err := c.confirmGenerated(cmd, "Generated merge request", "Title: "+title+"\n\n"+description); err != nil {
		return "", "", "", "", err
	}
	return title, description, in.Source, in.Target, nil
}
