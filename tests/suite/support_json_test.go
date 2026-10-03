package suite_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func runJSONInto(cwd string, out any, args ...string) commandResult {
	GinkgoHelper()
	res := runNipa(cwd, args...)
	Expect(res.ExitCode).To(Equal(0), res.Output())
	Expect(json.Unmarshal([]byte(res.Stdout), out)).To(Succeed(), "stdout: %s", res.Stdout)
	return res
}

type statusJSON struct {
	Branch string `json:"branch"`
	Head   *struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"head"`
	Staged    []string `json:"staged"`
	Deleted   []string `json:"deleted"`
	Modified  []string `json:"modified"`
	Untracked []string `json:"untracked"`
	Missing   []string `json:"missing"`
	Conflicts []string `json:"conflicts"`
}

type currentBranchJSON struct {
	Branch string `json:"branch"`
}

type branchesJSON struct {
	Current  string `json:"current"`
	Branches []struct {
		Name      string `json:"name"`
		Current   bool   `json:"current"`
		Default   bool   `json:"default"`
		Protected bool   `json:"protected"`
		CommitID  string `json:"commit_id"`
	} `json:"branches"`
}

type logJSON struct {
	Commits []struct {
		ID          string   `json:"id"`
		Hash        string   `json:"hash"`
		Message     string   `json:"message"`
		AuthorName  string   `json:"author_name"`
		AuthorEmail string   `json:"author_email"`
		CreatedAt   string   `json:"created_at"`
		ParentIDs   []string `json:"parent_ids"`
	} `json:"commits"`
}

type planJSON struct {
	Kind         string   `json:"kind"`
	UpToDate     bool     `json:"up_to_date"`
	FastForward  bool     `json:"fast_forward"`
	SourceBranch string   `json:"source_branch"`
	Targets      []string `json:"targets"`
	Conflicts    []string `json:"conflicts"`
	Changes      []struct {
		Path      string `json:"path"`
		Status    string `json:"status"`
		Binary    bool   `json:"binary"`
		SizeBytes int64  `json:"size_bytes"`
	} `json:"changes"`
	UploadObjects int   `json:"upload_objects"`
	UploadBytes   int64 `json:"upload_bytes"`
}

type tagsJSON struct {
	Tags []struct {
		Name      string `json:"name"`
		CommitID  string `json:"commit_id"`
		Message   string `json:"message"`
		CreatedAt string `json:"created_at"`
	} `json:"tags"`
}

type locksJSON struct {
	Locks []struct {
		Path               string `json:"path"`
		Scope              string `json:"scope"`
		Branch             string `json:"branch"`
		HeldBy             string `json:"held_by"`
		HeldByName         string `json:"held_by_name"`
		MergeRequestNumber *int64 `json:"merge_request_number"`
	} `json:"locks"`
}

type mergeRequestsJSON struct {
	MergeRequests []struct {
		ID           string `json:"id"`
		Number       int64  `json:"number"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		Status       string `json:"status"`
	} `json:"merge_requests"`
}

type diffJSON struct {
	Changes []struct {
		Path    string `json:"path"`
		OldPath string `json:"old_path"`
		Status  string `json:"status"`
		Binary  bool   `json:"binary"`
		Hunks   []struct {
			OldStart int `json:"old_start"`
			OldLines int `json:"old_lines"`
			NewStart int `json:"new_start"`
			NewLines int `json:"new_lines"`
			Lines    []struct {
				Kind string `json:"kind"`
				Old  int    `json:"old"`
				New  int    `json:"new"`
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"hunks"`
	} `json:"changes"`
}
