package suite_test

import (
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nipalab/nipa/internal/client/grpc/daemonpb"
	"github.com/nipalab/nipa/internal/grpc/pb"
)

type createdMRJSON struct {
	Number    int64 `json:"number"`
	Assignees []struct {
		UserID string `json:"user_id"`
		Name   string `json:"name"`
	} `json:"assignees"`
}

type reviewRequestJSON struct {
	Reviewer struct {
		UserID string `json:"user_id"`
	} `json:"reviewer"`
	RequestedBy struct {
		UserID string `json:"user_id"`
	} `json:"requested_by"`
}

// featureClone clones the project and pushes a feature branch to open
// requests from.
func featureClone(project string) string {
	GinkgoHelper()
	seedRepo(orgSlug, project, map[string][]byte{"base.txt": []byte("base\n")}, "seed")
	dir := cloneRepo(workspace(), repoURLFor(orgSlug, project), "work")
	Expect(runNipa(dir, "branch", "-c", "feature").ExitCode).To(Equal(0))
	writeText(dir, "feature.txt", "feature\n")
	Expect(runNipa(dir, "add", "feature.txt").ExitCode).To(Equal(0))
	Expect(pushRepo(dir, "feature work").ExitCode).To(Equal(0))
	return dir
}

func reviewerIDsOf(requests []reviewRequestJSON) []string {
	ids := make([]string, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.Reviewer.UserID)
	}
	return ids
}

var _ = Describe("merge request create participants", func() {
	It("creates with reviewers and assignees over REST and emits only mr.created", func() {
		project := newProject("mr-create-rest")
		featureClone(project)
		fixture := newWebhook(project, []string{"mr.created", "mr.updated", "mr.review_requested"}, nil)

		reviewer := newIdentity("mr-create-rest-reviewer")
		assignee := newIdentity("mr-create-rest-assignee")
		api.createPathRule(orgSlug, project, reviewer.userID, "", 1)
		api.createPathRule(orgSlug, project, assignee.userID, "", 1)
		base := fmt.Sprintf("/orgs/%s/projects/%s/merge-requests", orgSlug, project)

		var created createdMRJSON
		api.do(http.MethodPost, base, map[string]any{
			"title": uniqueMessage("REST participants"), "source_branch": "feature", "target_branch": "main",
			"reviewer_ids": []string{reviewer.userID, reviewer.userID},
			"assignee_ids": []string{assignee.userID},
		}, &created, true)
		Expect(created.Number).To(Equal(int64(1)))
		Expect(created.Assignees).To(HaveLen(1))
		Expect(created.Assignees[0].UserID).To(Equal(assignee.userID))

		var requests []reviewRequestJSON
		api.do(http.MethodGet, fmt.Sprintf("%s/1/review-requests", base), nil, &requests, true)
		Expect(reviewerIDsOf(requests)).To(Equal([]string{reviewer.userID}), "duplicates collapse")
		Expect(requests[0].RequestedBy.UserID).To(Equal(api.me()))

		var fetched createdMRJSON
		api.do(http.MethodGet, fmt.Sprintf("%s/1", base), nil, &fetched, true)
		Expect(fetched.Assignees).To(HaveLen(1))

		fixture.receiver.wait("mr.created")
		Consistently(func() []int {
			return []int{
				fixture.receiver.count("mr.created"),
				fixture.receiver.count("mr.review_requested"),
				fixture.receiver.count("mr.updated"),
			}
		}, "1s", "100ms").Should(Equal([]int{1, 0, 0}))
	})

	It("rejects invalid participants over REST without creating the request", func() {
		project := newProject("mr-create-rest-invalid")
		featureClone(project)
		outsider := newIdentity("mr-create-outsider")
		base := fmt.Sprintf("/orgs/%s/projects/%s/merge-requests", orgSlug, project)
		body := func(extra map[string]any) map[string]any {
			out := map[string]any{"title": uniqueMessage("Invalid"), "source_branch": "feature", "target_branch": "main"}
			for key, value := range extra {
				out[key] = value
			}
			return out
		}

		for _, tc := range []struct {
			extra   map[string]any
			message string
		}{
			{map[string]any{"reviewer_ids": []string{"not base36!"}}, "invalid reviewer id"},
			{map[string]any{"assignee_ids": []string{"not base36!"}}, "invalid assignee id"},
			{map[string]any{"reviewer_ids": []string{api.me()}}, "you cannot request a review from yourself"},
			{map[string]any{"reviewer_ids": []string{outsider.userID}}, "not found"},
			{map[string]any{"assignee_ids": []string{outsider.userID}}, "not found"},
		} {
			code, data := api.doRaw(http.MethodPost, base, body(tc.extra))
			Expect(code).To(Equal(http.StatusBadRequest), string(data))
			Expect(string(data)).To(ContainSubstring(tc.message))
		}

		var list mergeRequestsJSON
		api.do(http.MethodGet, base, nil, &list, true)
		Expect(list.MergeRequests).To(BeEmpty(), "a rejected create leaves nothing behind")
	})

	It("requires project write only when participants are given", func() {
		project := newProject("mr-create-readonly")
		featureClone(project)
		reader := newIdentity("mr-create-reader")
		other := newIdentity("mr-create-other")
		api.createPathRule(orgSlug, project, reader.userID, "", 1)
		api.createPathRule(orgSlug, project, other.userID, "", 1)
		readerAPI := newAPIClient(cli.apiURL, reader.email, reader.password)
		base := fmt.Sprintf("/orgs/%s/projects/%s/merge-requests", orgSlug, project)

		for _, extra := range []map[string]any{
			{"reviewer_ids": []string{other.userID}},
			{"assignee_ids": []string{other.userID}},
		} {
			payload := map[string]any{"title": uniqueMessage("Reader"), "source_branch": "feature", "target_branch": "main"}
			for key, value := range extra {
				payload[key] = value
			}
			code, data := readerAPI.doRaw(http.MethodPost, base, payload)
			Expect(code).To(Equal(http.StatusForbidden), string(data))
		}

		var created createdMRJSON
		readerAPI.do(http.MethodPost, base, map[string]any{
			"title": uniqueMessage("Reader plain"), "source_branch": "feature", "target_branch": "main",
		}, &created, true)
		Expect(created.Number).To(Equal(int64(1)))
		Expect(created.Assignees).To(BeEmpty())
	})

	It("forwards reviewers and assignees through the daemon proxy", func() {
		project := newProject("mr-create-daemon")
		dir := featureClone(project)
		reviewer := newIdentity("mr-create-daemon-reviewer")
		assignee := newIdentity("mr-create-daemon-assignee")
		api.createPathRule(orgSlug, project, reviewer.userID, "", 1)
		api.createPathRule(orgSlug, project, assignee.userID, "", 1)

		d := startDaemon()
		d.watch(dir)

		_, err := d.client.ProxyMergeRequestCreate(d.ctx, &daemonpb.ProxyMergeRequestCreateRequest{
			Root: dir,
			Request: &pb.CreateMergeRequestRequest{
				Title: "Bad", SourceBranch: "feature", TargetBranch: "main", ReviewerIds: []string{"not base36!"},
			},
		})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		created, err := d.client.ProxyMergeRequestCreate(d.ctx, &daemonpb.ProxyMergeRequestCreateRequest{
			Root: dir,
			Request: &pb.CreateMergeRequestRequest{
				Title: "Proxied participants", SourceBranch: "feature", TargetBranch: "main",
				ReviewerIds: []string{reviewer.userID}, AssigneeIds: []string{assignee.userID},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		mr := created.GetResponse().GetMergeRequest()
		Expect(mr.GetNumber()).To(Equal(int64(1)))
		Expect(mr.GetAssignees()).To(HaveLen(1))
		Expect(mr.GetAssignees()[0].GetUserId()).To(Equal(assignee.userID))

		timeline, err := d.client.ProxyMergeRequestTimeline(d.ctx, &daemonpb.ProxyMergeRequestTimelineRequest{
			Root:    dir,
			Request: &pb.ListMergeRequestTimelineRequest{Number: 1},
		})
		Expect(err).NotTo(HaveOccurred())
		items := timeline.GetResponse().GetItems()
		Expect(items).To(HaveLen(2))
		Expect(items[0].GetKind()).To(Equal("opened"))
		Expect(items[1].GetKind()).To(Equal("review_requested"))
		Expect(items[1].GetSubject().GetUserId()).To(Equal(reviewer.userID))
	})

	It("creates with reviewers and assignees from the MCP tool", func() {
		project := newProject("mr-create-mcp")
		dir := featureClone(project)
		reviewer := newIdentity("mr-create-mcp-reviewer")
		assignee := newIdentity("mr-create-mcp-assignee")
		api.createPathRule(orgSlug, project, reviewer.userID, "", 1)
		api.createPathRule(orgSlug, project, assignee.userID, "", 1)

		m := startMCP(dir, true)
		result := m.callTool("nipa_mr_create", map[string]any{
			"repo": dir, "title": uniqueMessage("MCP participants"),
			"reviewers": []string{reviewer.userID}, "assignees": []string{assignee.userID},
		})
		Expect(result.IsError).To(BeFalse(), result.text())

		view := runNipa(dir, "mr", "view", "1")
		Expect(view.ExitCode).To(Equal(0), view.Output())
		Expect(view.Output()).To(MatchRegexp(`assignees: .*mr-create-mcp-assignee`))
		requests := runNipa(dir, "mr", "requests", "1")
		Expect(requests.ExitCode).To(Equal(0), requests.Output())
		Expect(requests.Output()).To(ContainSubstring("mr-create-mcp-reviewer"))

		rejected := m.callTool("nipa_mr_create", map[string]any{
			"repo": dir, "title": uniqueMessage("MCP outsider"), "assignees": []string{"not base36!"},
		})
		Expect(rejected.IsError).To(BeTrue())
		Expect(rejected.text()).To(ContainSubstring("invalid assignee id"))
	})
})
