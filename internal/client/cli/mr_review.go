package cli

import (
	"fmt"
	"time"

	"github.com/nipalab/nipa/internal/client/domain"
	"github.com/nipalab/nipa/internal/client/localrepo"
	"github.com/nipalab/nipa/internal/client/output"
	"github.com/spf13/cobra"
)

func actorLabel(actor domain.ReviewActor) string {
	if actor.Name != "" {
		return actor.Name
	}
	return actor.UserID
}

func (c *Cli) setupMrViewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "view <number>",
		Short:         "Show a merge request with its mergeability and reviews",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, info, err := c.useCase.MR().View(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			reviews, err := c.useCase.MR().Reviews(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			commits, err := c.useCase.MR().Commits(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.NewMergeRequestView(mr, info, reviews, commits))
			}
			cmd.Printf("#%d %s\n", mr.Number, mr.Title)
			status := mr.Status
			if info != nil && info.Status != "" {
				status += " · " + info.Status
			}
			if info != nil && info.BlockedBy != "" {
				status += " (blocked by " + info.BlockedBy + ")"
			}
			cmd.Printf("status: %s\n", status)
			cmd.Printf("branches: %s -> %s\n", mr.SourceBranch, mr.TargetBranch)
			cmd.Printf("author: %s\n", mr.CreatedBy)
			cmd.Printf("commits: %d\n", len(commits))
			if mr.Review != nil {
				cmd.Printf("approvals: %d · changes requested: %d · outstanding reviewers: %d\n",
					mr.Review.Approvals, mr.Review.ChangesRequested, len(mr.Review.OutstandingReviewers))
			}
			if len(reviews) > 0 {
				cmd.Println("reviews:")
				for _, review := range reviews {
					line := fmt.Sprintf("  %s %s", actorLabel(review.Reviewer), review.State)
					switch {
					case review.DismissedAt != nil:
						line += " (dismissed: " + review.DismissedReason + ")"
					case review.Stale:
						line += " (stale)"
					}
					cmd.Println(line)
				}
			}
			if mr.Description != "" {
				cmd.Printf("\n%s\n", mr.Description)
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) setupMrReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "reopen <number>",
		Short:         "Reopen a closed merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			mr, err := c.useCase.MR().Reopen(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%d reopened.\n", mr.Number)
			return nil
		},
	}
}

func (m *mrReviewFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&m.approve, "approve", false, "Approve the merge request")
	cmd.Flags().BoolVar(&m.requestChanges, "request-changes", false, "Request changes before merging")
	cmd.Flags().StringVarP(&m.message, "message", "m", "", "Review message (required)")
}

type mrReviewFlags struct {
	approve        bool
	requestChanges bool
	message        string
}

func (c *Cli) setupMrReviewCmd() *cobra.Command {
	flags := &mrReviewFlags{}
	cmd := &cobra.Command{
		Use:           "review <number>",
		Short:         "Submit a review decision",
		Long:          "Submit a review decision: --approve, --request-changes, or no flag for a comment-only review. A message is required.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if flags.approve && flags.requestChanges {
				return domain.NewUserError("pass either --approve or --request-changes, not both")
			}
			state := domain.MergeRequestReviewCommented
			if flags.approve {
				state = domain.MergeRequestReviewApproved
			}
			if flags.requestChanges {
				state = domain.MergeRequestReviewChangesRequested
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			review, err := c.useCase.MR().Review(cmd.Context(), root, args[0], state, flags.message)
			if err != nil {
				return err
			}
			cmd.Printf("Merge request #%s reviewed: %s\n", args[0], review.State)
			return nil
		},
	}
	flags.addTo(cmd)
	return cmd
}

func (c *Cli) setupMrCommentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "comments <number>",
		Short:         "List the comment threads of a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			threads, err := c.useCase.MR().Threads(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.MergeRequestThreads{Threads: threads})
			}
			if len(threads) == 0 {
				cmd.Println("no comments")
				return nil
			}
			for _, thread := range threads {
				cmd.Println(threadHeader(thread))
				for _, comment := range thread.Comments {
					cmd.Printf("  %s: %s\n", actorLabel(comment.User), comment.Body)
				}
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}

func threadHeader(thread *domain.MergeRequestThread) string {
	location := "top-level"
	if thread.FilePath != "" {
		location = thread.FilePath
		switch {
		case thread.NewLine != nil:
			location += fmt.Sprintf(":%d", *thread.NewLine)
		case thread.OldLine != nil:
			location += fmt.Sprintf("(old):%d", *thread.OldLine)
		}
	}
	state := "open"
	if thread.Resolved {
		state = "resolved"
	}
	if thread.Outdated {
		state += ", outdated"
	}
	return fmt.Sprintf("thread %s (%s, %s)", thread.ID, location, state)
}

func (c *Cli) setupMrCommentCmd() *cobra.Command {
	var message, file string
	var newLine, oldLine int64
	cmd := &cobra.Command{
		Use:           "comment <number>",
		Short:         "Start a comment thread on a merge request",
		Long:          "Start a top-level conversation thread, or anchor an inline comment with --file and --new-line/--old-line.",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" && (newLine != 0 || oldLine != 0) {
				return domain.NewUserError("pass --file with --new-line or --old-line")
			}
			if file != "" && newLine == 0 && oldLine == 0 {
				return domain.NewUserError("pass --new-line or --old-line with --file")
			}
			var newPtr, oldPtr *int64
			if newLine != 0 {
				newPtr = &newLine
			}
			if oldLine != 0 {
				oldPtr = &oldLine
			}
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			thread, err := c.useCase.MR().Comment(cmd.Context(), root, args[0], file, oldPtr, newPtr, message)
			if err != nil {
				return err
			}
			cmd.Printf("thread %s opened\n", thread.ID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Comment body (required)")
	cmd.Flags().StringVar(&file, "file", "", "Anchor the comment to this file")
	cmd.Flags().Int64Var(&newLine, "new-line", 0, "Anchor to this line of the new file")
	cmd.Flags().Int64Var(&oldLine, "old-line", 0, "Anchor to this line of the old file")
	return cmd
}

func (c *Cli) setupMrReplyCmd() *cobra.Command {
	var message string
	cmd := &cobra.Command{
		Use:           "reply <number> <thread-id>",
		Short:         "Reply to a comment thread",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			comment, err := c.useCase.MR().Reply(cmd.Context(), root, args[0], args[1], message)
			if err != nil {
				return err
			}
			cmd.Printf("comment %s added to thread %s\n", comment.ID, comment.ThreadID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&message, "message", "m", "", "Reply body (required)")
	return cmd
}

func (c *Cli) setupMrResolveCmd() *cobra.Command {
	var unresolve bool
	cmd := &cobra.Command{
		Use:           "resolve <number> <thread-id>",
		Short:         "Resolve or reopen a comment thread",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			thread, err := c.useCase.MR().Resolve(cmd.Context(), root, args[0], args[1], !unresolve)
			if err != nil {
				return err
			}
			if thread.Resolved {
				cmd.Printf("thread %s resolved\n", thread.ID)
			} else {
				cmd.Printf("thread %s reopened\n", thread.ID)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&unresolve, "unresolve", false, "Reopen a resolved thread")
	return cmd
}

func (c *Cli) setupMrTimelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "timeline <number>",
		Short:         "Show the activity timeline of a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			items, err := c.useCase.MR().Timeline(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.MergeRequestTimeline{Timeline: items})
			}
			if len(items) == 0 {
				cmd.Println("no activity")
				return nil
			}
			for _, item := range items {
				line := fmt.Sprintf("%s  %s  %s", item.CreatedAt.Format(time.RFC3339), item.Kind, actorLabel(item.Actor))
				if item.Subject != nil {
					line += " -> " + actorLabel(*item.Subject)
				}
				if item.Body != "" {
					line += ": " + item.Body
				}
				cmd.Println(line)
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) setupMrRequestsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "requests <number>",
		Short:         "List the pending review requests of a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			requests, err := c.useCase.MR().ReviewRequests(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.MergeRequestReviewRequests{ReviewRequests: requests})
			}
			if len(requests) == 0 {
				cmd.Println("no review requests")
				return nil
			}
			for _, request := range requests {
				cmd.Printf("%s  requested from %s by %s\n", request.ID, actorLabel(request.Reviewer), actorLabel(request.RequestedBy))
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}

func (c *Cli) setupMrRequestReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "request-review <number> <user-id>",
		Short:         "Request a review from a user (base36 user id)",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			request, err := c.useCase.MR().RequestReview(cmd.Context(), root, args[0], args[1])
			if err != nil {
				return err
			}
			cmd.Printf("review requested from %s\n", actorLabel(request.Reviewer))
			return nil
		},
	}
}

func (c *Cli) setupMrUnrequestReviewCmd() *cobra.Command {
	return &cobra.Command{
		Use:           "unrequest-review <number> <user-id>",
		Short:         "Withdraw a pending review request (base36 user id)",
		Args:          cobra.ExactArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			if err := c.useCase.MR().RemoveReviewRequest(cmd.Context(), root, args[0], args[1]); err != nil {
				return err
			}
			cmd.Printf("review request for %s removed\n", args[1])
			return nil
		},
	}
}

func (c *Cli) setupMrDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "diff <number>",
		Short:         "Show the patch of a merge request",
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := localrepo.FindRepoRoot()
			if err != nil {
				return err
			}
			files, err := c.useCase.MR().Diff(cmd.Context(), root, args[0])
			if err != nil {
				return err
			}
			if jsonRequested(cmd) {
				return output.WriteJSON(cmd.OutOrStdout(), output.MergeRequestDiff{Files: files})
			}
			if len(files) == 0 {
				cmd.Println("no changes")
				return nil
			}
			for _, file := range files {
				if file.OldPath != "" {
					cmd.Printf("%s %s -> %s (+%d -%d)\n", file.Status, file.OldPath, file.Path, file.Additions, file.Deletions)
				} else {
					cmd.Printf("%s %s (+%d -%d)\n", file.Status, file.Path, file.Additions, file.Deletions)
				}
				if file.Binary {
					cmd.Println("  binary file")
					continue
				}
				for _, line := range file.Patch {
					cmd.Println("  " + line)
				}
			}
			return nil
		},
	}
	addJSONFlag(cmd)
	return cmd
}
