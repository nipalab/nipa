package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/nipalab/nipa/internal/client/usecase"
)

// BlameRunner is the usecase behind nipa blame.
type BlameRunner interface {
	Run(ctx context.Context, root, path, rev string, progress usecase.BlameProgress) (*usecase.BlameResult, error)
}

func (c *Cli) setupBlameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blame <path>",
		Short: "Show which commit last changed each line of a text file",
		Long: "Print every line of a text file with the commit, author and date that last changed it. " +
			"Without --revision the working file is blamed and lines that are not pushed yet are marked; " +
			"with it, the file as of that branch, tag, commit ID or HEAD. History is read newest first " +
			"(at most the last 500 commits; text files up to 2 MiB), and lines older than what could be " +
			"read are marked with ?.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			rev, _ := cmd.Flags().GetString("revision")
			res, err := c.useCase.Blame().Run(cmd.Context(), root, args[0], rev, nil)
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), blameJSON(res))
			}
			return writeBlame(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringP("revision", "r", "", "Blame the file as of this branch, tag, commit ID or HEAD")
	addJSONFlag(cmd)
	return cmd
}

func writeBlame(out io.Writer, res *usecase.BlameResult) error {
	commits := make(map[string]usecase.BlameCommit, len(res.Commits))
	idWidth, nameWidth := len("not pushed"), 1
	for _, c := range res.Commits {
		commits[c.ID] = c
		idWidth = max(idWidth, len(c.ID))
		nameWidth = max(nameWidth, len(c.AuthorName))
	}
	numWidth := len(fmt.Sprint(len(res.Lines)))
	for i, line := range res.Lines {
		id, name, date := "?", "", ""
		switch {
		case line.Uncommitted:
			id = "not pushed"
		case line.CommitID != "":
			c := commits[line.CommitID]
			id, name, date = c.ID, c.AuthorName, c.CreatedAt.Local().Format(time.DateOnly)
		}
		if _, err := fmt.Fprintf(out, "%-*s (%-*s %10s %*d) %s\n", idWidth, id, nameWidth, name, date, numWidth, i+1, line.Text); err != nil {
			return err
		}
	}
	if res.Truncated {
		_, err := fmt.Fprintln(out, "? older than the history blame could read (500 commits, or a binary or too large version)")
		return err
	}
	return nil
}

type blameJSONLine struct {
	Line        int    `json:"line"`
	Text        string `json:"text"`
	CommitID    string `json:"commit_id,omitempty"`
	Uncommitted bool   `json:"uncommitted,omitempty"`
}

type blameJSONCommit struct {
	ID          string    `json:"id"`
	Hash        string    `json:"hash,omitempty"`
	AuthorName  string    `json:"author_name"`
	AuthorEmail string    `json:"author_email"`
	Message     string    `json:"message"`
	CreatedAt   time.Time `json:"created_at"`
}

type blameJSONResult struct {
	Path      string            `json:"path"`
	Revision  string            `json:"revision,omitempty"`
	Lines     []blameJSONLine   `json:"lines"`
	Commits   []blameJSONCommit `json:"commits"`
	Truncated bool              `json:"truncated"`
}

func blameJSON(res *usecase.BlameResult) blameJSONResult {
	out := blameJSONResult{Path: res.Path, Revision: res.Revision, Truncated: res.Truncated, Lines: []blameJSONLine{}, Commits: []blameJSONCommit{}}
	for i, l := range res.Lines {
		out.Lines = append(out.Lines, blameJSONLine{Line: i + 1, Text: l.Text, CommitID: l.CommitID, Uncommitted: l.Uncommitted})
	}
	for _, c := range res.Commits {
		out.Commits = append(out.Commits, blameJSONCommit{ID: c.ID, Hash: c.Hash, AuthorName: c.AuthorName, AuthorEmail: c.AuthorEmail, Message: strings.TrimSpace(c.Message), CreatedAt: c.CreatedAt})
	}
	return out
}
