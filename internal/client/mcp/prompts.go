package mcp

import (
	"context"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nipalab/nipa/internal/client/aicontext"
	"github.com/nipalab/nipa/internal/client/aiprompt"
	"github.com/nipalab/nipa/internal/client/domain"
)

var repoArgument = &mcp.PromptArgument{Name: "repo", Description: "Path inside a nipa working copy (defaults to the server working directory)"}

func addPrompts(srv *mcp.Server, s *server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "nipa_commit_message",
		Title:       "Commit message for staged changes",
		Description: "Draft a commit message for the changes staged with nipa add: changed files, asset summaries, .nipaassets.yml violations and the text diff (capped). Runs offline; binary contents are never included.",
		Arguments:   []*mcp.PromptArgument{repoArgument},
	}, s.commitMessagePrompt)
	srv.AddPrompt(&mcp.Prompt{
		Name:        "nipa_mr_description",
		Title:       "Merge request title and description",
		Description: "Draft the title and description of a merge request from source into target: commits since the merge base, asset summaries, budget violations and the text diff (capped).",
		Arguments: []*mcp.PromptArgument{
			repoArgument,
			{Name: "source", Description: "Source branch (defaults to the working copy branch)"},
			{Name: "target", Description: "Target branch (defaults to the project default branch)"},
		},
	}, s.mrDescriptionPrompt)
	srv.AddPrompt(&mcp.Prompt{
		Name:        "nipa_review_mr",
		Title:       "Review a merge request",
		Description: "Review an open merge request: metadata, commits, existing threads, asset summaries, budget violations and the text diff (capped). Asks for findings, not an approval.",
		Arguments: []*mcp.PromptArgument{
			repoArgument,
			{Name: "number", Description: "Merge request number", Required: true},
		},
	}, s.reviewMRPrompt)
}

func (s *server) collector(uc UseCases, root string) *aicontext.Collector {
	c := aicontext.New(uc.Diff, root)
	if uc.MR != nil {
		c.MR = uc.MR
	}
	if uc.Repo != nil {
		c.History = uc.Repo
	}
	if uc.Connector != nil {
		c.Connector = uc.Connector
	}
	return c
}

func promptArgs(req *mcp.GetPromptRequest) map[string]string {
	if req == nil || req.Params == nil {
		return nil
	}
	return req.Params.Arguments
}

func (s *server) commitMessagePrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	root, uc, err := s.openDiff(promptArgs(req)["repo"])
	if err != nil {
		return nil, err
	}
	defer uc.close()
	in, err := s.collector(uc, root).Commit(ctx, root)
	if err != nil {
		return nil, toolError(err)
	}
	return promptResult("Commit message for the staged changes", aiprompt.CommitMessage(*in)), nil
}

func (s *server) mrDescriptionPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := promptArgs(req)
	root, uc, err := s.openDiff(args["repo"])
	if err != nil {
		return nil, err
	}
	defer uc.close()
	in, err := s.collector(uc, root).MRDescription(ctx, root, args["source"], args["target"])
	if err != nil {
		return nil, toolError(err)
	}
	return promptResult("Merge request from "+in.Source+" into "+in.Target, aiprompt.MRDescription(*in)), nil
}

func (s *server) reviewMRPrompt(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := promptArgs(req)
	number, err := strconv.ParseInt(strings.TrimSpace(args["number"]), 10, 64)
	if err != nil || number <= 0 {
		return nil, toolError(domain.NewUserError("number must be a positive merge request number"))
	}
	root, uc, err := s.openMR(args["repo"])
	if err != nil {
		return nil, err
	}
	defer uc.close()
	if uc.Diff == nil {
		return nil, notConfigured("diff is")
	}
	in, err := s.collector(uc, root).Review(ctx, root, number)
	if err != nil {
		return nil, toolError(err)
	}
	return promptResult("Review of merge request #"+strconv.FormatInt(number, 10), aiprompt.ReviewMR(*in)), nil
}

// promptResult sends the instructions and the repository data as one user
// message, since MCP prompt messages have no system role.
func promptResult(description string, p aiprompt.Prompt) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: description,
		Messages: []*mcp.PromptMessage{{
			Role:    "user",
			Content: &mcp.TextContent{Text: p.System + "\n\n---\n\n" + p.User},
		}},
	}
}
