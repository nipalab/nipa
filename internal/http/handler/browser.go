package handler

import (
	nethttp "net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nipalab/nipa/internal/diff"
	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/http"
	"github.com/nipalab/nipa/internal/http/model"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	defaultBranchLimit = 100
	defaultCommitLimit = 50
	maxBlobBytes       = 32 << 20
)

func (h *Handler) ListProjectBranches(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	limit := queryInt(appCtx.QueryParameter("limit"), defaultBranchLimit)
	var lastID snow.ID
	if raw := appCtx.QueryParameter("last_id"); raw != "" {
		id, err := parseID(raw, "branch")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		lastID = id
	}
	var updatedAfter *time.Time
	if raw := appCtx.QueryParameter("last_updated_at"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			appCtx.HandleError(domain.NewErrorUser("invalid last_updated_at"))
			return
		}
		updatedAfter = &parsed
	}
	branches, err := h.useCase.Branch().ListBranches(appCtx.Context(), project.ID, limit, updatedAfter, lastID)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.BranchResponse, 0, len(branches))
	for _, branch := range branches {
		resp = append(resp, toBranchResponse(branch))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) ListCommits(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	branchName := strings.TrimSpace(appCtx.QueryParameter("branch"))
	if branchName == "" {
		branch, err := h.useCase.Branch().GetDefault(appCtx.Context(), project.ID)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		branchName = branch.Name
	}
	var start *snow.ID
	if raw := appCtx.QueryParameter("start"); raw != "" {
		id, err := parseID(raw, "commit")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		start = &id
	}
	limit := queryInt(appCtx.QueryParameter("limit"), defaultCommitLimit)
	path := strings.Trim(appCtx.QueryParameter("path"), "/")
	var entries []*domain.CommitLogEntry
	if path != "" {
		entries, err = h.useCase.Branch().PathCommitLog(appCtx.Context(), project.ID, branchName, path, start, limit)
	} else {
		entries, err = h.useCase.Branch().GetCommitLog(appCtx.Context(), project.ID, branchName, start, limit)
	}
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := make([]model.CommitResponse, 0, len(entries))
	for _, entry := range entries {
		resp = append(resp, toCommitResponse(entry))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) GetCommitDiff(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	commitID, err := parseID(appCtx.PathParameter("commit"), "commit")
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	var base *snow.ID
	if raw := appCtx.QueryParameter("base"); raw != "" {
		id, err := parseID(raw, "commit")
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		base = &id
	}
	files, resolvedBase, err := h.useCase.Branch().CommitDiff(appCtx.Context(), project.ID, commitID, base)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	resp := model.CommitDiffResponse{
		CommitID: commitID.Base36(),
		Files:    make([]model.DiffFileResponse, 0, len(files)),
	}
	if resolvedBase != nil {
		resp.BaseID = resolvedBase.Base36()
	}
	for _, file := range files {
		resp.Files = append(resp.Files, toDiffFileResponse(file))
	}
	appCtx.WriteJson(nethttp.StatusOK, resp)
}

func (h *Handler) GetTree(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	path := strings.Trim(appCtx.QueryParameter("path"), "/")
	rev := appCtx.QueryParameter("rev")
	if appCtx.QueryParameter("recursive") == "1" {
		files, err := h.useCase.Branch().TreeFilesAt(appCtx.Context(), project.ID, rev, path)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		resp := model.TreeResponse{Path: path, Entries: make([]model.TreeEntryResponse, 0, len(files))}
		for _, file := range files {
			name := file.Path
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			resp.Entries = append(resp.Entries, model.TreeEntryResponse{
				Name:      name,
				Path:      file.Path,
				Type:      "file",
				Mode:      file.Mode,
				SizeBytes: file.SizeBytes,
				IsBinary:  file.IsBinary,
				Hash:      file.Hash.String(),
			})
		}
		appCtx.WriteJson(nethttp.StatusOK, resp)
		return
	}
	if appCtx.QueryParameter("history") == "1" {
		node, history, err := h.useCase.Branch().TreeAtWithHistory(appCtx.Context(), project.ID, rev, path)
		if err != nil {
			appCtx.HandleError(err)
			return
		}
		resp := toTreeResponse(path, node)
		if history != nil {
			if history.Latest != nil {
				latest := toCommitResponse(history.Latest)
				resp.LatestCommit = &latest
			}
			for i := range resp.Entries {
				if commit, ok := history.ByPath[resp.Entries[i].Path]; ok {
					summary := toCommitResponse(commit)
					resp.Entries[i].LastCommit = &summary
				}
			}
		}
		appCtx.WriteJson(nethttp.StatusOK, resp)
		return
	}
	node, err := h.useCase.Branch().TreeAt(appCtx.Context(), project.ID, rev, path)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	appCtx.WriteJson(nethttp.StatusOK, toTreeResponse(path, node))
}

func (h *Handler) GetBlob(appCtx http.AppContext) {
	_, project, err := h.resolveProject(appCtx)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	path := strings.Trim(appCtx.QueryParameter("path"), "/")
	content, entry, err := h.useCase.Branch().FileContent(appCtx.Context(), project.ID, appCtx.QueryParameter("rev"), path)
	if err != nil {
		appCtx.HandleError(err)
		return
	}
	if entry.SizeBytes > maxBlobBytes {
		appCtx.WriteJson(nethttp.StatusRequestEntityTooLarge, model.NewAPIError("file is too large for the web viewer"))
		return
	}
	contentType := "text/plain; charset=utf-8"
	if entry.IsBinary {
		contentType = "application/octet-stream"
	}
	appCtx.WriteBytes(nethttp.StatusOK, contentType, content)
}

func queryInt(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func toTreeResponse(path string, node *domain.TreeNode) model.TreeResponse {
	resp := model.TreeResponse{Path: path, Entries: make([]model.TreeEntryResponse, 0)}
	for _, child := range node.TreeChildren {
		resp.Entries = append(resp.Entries, model.TreeEntryResponse{
			Name: child.Name,
			Path: joinPath(path, child.Name),
			Type: "tree",
			Hash: child.Hash.String(),
		})
	}
	for _, file := range node.FileChildren {
		resp.Entries = append(resp.Entries, model.TreeEntryResponse{
			Name:      file.Name,
			Path:      joinPath(path, file.Name),
			Type:      "file",
			Mode:      file.Mode,
			SizeBytes: file.SizeBytes,
			IsBinary:  file.IsBinary,
			Hash:      file.Hash.String(),
		})
	}
	sort.SliceStable(resp.Entries, func(i, j int) bool {
		if resp.Entries[i].Type != resp.Entries[j].Type {
			return resp.Entries[i].Type == "tree"
		}
		return resp.Entries[i].Name < resp.Entries[j].Name
	})
	return resp
}

func toBranchResponse(branch *domain.Branch) model.BranchResponse {
	resp := model.BranchResponse{
		ID:          branch.ID.Base36(),
		Name:        branch.Name,
		IsDefault:   branch.IsDefault,
		IsProtected: branch.IsProtected,
		UpdatedAt:   branch.UpdatedAt,
	}
	if branch.CommitID != nil {
		resp.CommitID = branch.CommitID.Base36()
	}
	return resp
}

func toCommitResponse(entry *domain.CommitLogEntry) model.CommitResponse {
	resp := model.CommitResponse{
		ID:          entry.ID.Base36(),
		Message:     entry.Message,
		AuthorName:  entry.AuthorName,
		AuthorEmail: entry.AuthorEmail,
		CreatedAt:   entry.CreatedAt,
	}
	if entry.Parent1ID != nil {
		resp.Parent1ID = entry.Parent1ID.Base36()
	}
	if entry.Parent2ID != nil {
		resp.Parent2ID = entry.Parent2ID.Base36()
	}
	return resp
}

func toDiffFileResponse(file diff.FileDiff) model.DiffFileResponse {
	resp := model.DiffFileResponse{
		Path:   file.Change.Path,
		Status: file.Change.Status.String(),
		Binary: file.Change.New.IsBinary || file.Change.Old.IsBinary,
	}
	if file.Change.Status == diff.Renamed {
		resp.OldPath = file.Change.Old.Path
	}
	if !resp.Binary {
		resp.Patch = append([]string{diff.HeaderLine(file.Change)}, diff.FilePatch(file, diff.Options{})...)
		resp.Additions, resp.Deletions = diff.CountLines(file.Old, file.New)
	}
	return resp
}

func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}
