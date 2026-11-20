package usecase

//go:generate go run go.uber.org/mock/mockgen -source=$GOFILE -destination=import_mock_test.go -package=usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nipalab/nipa/internal/domain"
	"github.com/nipalab/nipa/internal/snow"
)

const (
	// MaxImportLookup bounds the origins one lookup may ask for.
	MaxImportLookup = 1000
	// maxImportAuthorRunes caps the stored author name and email.
	maxImportAuthorRunes = 255
	// maxAuditImportHeads bounds the ref heads listed in the audit entry.
	maxAuditImportHeads = 100
)

// Statuses of one ref in a SetRefs result.
const (
	ImportRefCreated   = "created"
	ImportRefUpdated   = "updated"
	ImportRefUnchanged = "unchanged"
	ImportRefExists    = "exists"
	ImportRefDiverged  = "diverged"
	ImportRefProtected = "protected"
	ImportRefInvalid   = "invalid"
)

// Kinds of SetRefs results.
const (
	ImportRefBranch  = "branch"
	ImportRefTag     = "tag"
	ImportRefDefault = "default"
)

type importUsers interface {
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByID(ctx context.Context, id snow.ID) (*domain.User, error)
}

type importOrgMembers interface {
	MemberRole(ctx context.Context, orgID, userID snow.ID) (string, error)
}

type importProjectOrgs interface {
	ProjectOrgID(ctx context.Context, projectID snow.ID) (snow.ID, error)
}

// importLocker serializes ref updates of one project across server processes
// for the current transaction.
type importLocker interface {
	LockProject(ctx context.Context, projectID snow.ID) error
}

type importHooks interface {
	EmitPush(ctx context.Context, event PushEvent) error
	EmitTag(ctx context.Context, event string, projectID snow.ID, tag *domain.Tag, actor snow.ID) error
}

// importStorageGate refuses writes to projects whose chunk store is read-only.
type importStorageGate interface {
	RequireWritable(ctx context.Context, projectID snow.ID) error
}

// importQuota checks an import's estimated upload against the storage quota.
type importQuota interface {
	CheckUploadHeadroom(ctx context.Context, org *domain.Organization, project *domain.Project, bytes int64) (bool, error)
}

// ImportCommit is one source commit to write. Parents are origins of commits
// imported earlier; the files and removed paths are the delta against the
// first parent's tree.
type ImportCommit struct {
	Origin        string
	ParentOrigins []string
	Message       string
	AuthorName    string
	AuthorEmail   string
	AuthoredAt    time.Time
	// AuthorUserID maps the author to a Nipa user explicitly (an author map
	// entry); zero matches the author email instead.
	AuthorUserID snow.ID
	Files        []*domain.PushFile
	Removed      []string
}

type ImportedCommit struct {
	Origin     string
	CommitID   snow.ID
	CommitHash domain.Hash
}

// ImportRef points a branch or tag at an imported commit.
type ImportRef struct {
	Name   string
	Origin string
	// Message is the annotation of a tag.
	Message string
}

type SetRefsInput struct {
	Branches []ImportRef
	Tags     []ImportRef
	Default  string
	// Update fast-forwards existing branches.
	Update bool
	// Commits is the number of commits the client imported in this run,
	// recorded in the audit entry.
	Commits int
}

type ImportRefResult struct {
	Kind     string
	Name     string
	Status   string
	CommitID snow.ID
	Message  string
}

// Import writes commits from another version control system with their
// original parents and authors, and points refs at them afterwards.
type Import struct {
	permUc     permissionUsecase
	branchRepo branchRepository
	pushRepo   pushRepository
	tagRepo    tagRepository
	users      importUsers
	orgs       importOrgMembers
	projects   importProjectOrgs
	locker     importLocker
	tx         transactor
	snowNode   snow.Node
	hooks      importHooks
	audit      AuditRecorder
	storage    importStorageGate
	quota      importQuota
	maxFiles   int
}

func NewImport(permUc permissionUsecase, branchRepo branchRepository, pushRepo pushRepository, tagRepo tagRepository, users importUsers, orgs importOrgMembers, projects importProjectOrgs, locker importLocker, tx transactor, snowNode snow.Node) *Import {
	return &Import{
		permUc:     permUc,
		branchRepo: branchRepo,
		pushRepo:   pushRepo,
		tagRepo:    tagRepo,
		users:      users,
		orgs:       orgs,
		projects:   projects,
		locker:     locker,
		tx:         tx,
		snowNode:   snowNode,
	}
}

func (i *Import) WithHooks(hooks importHooks) *Import {
	i.hooks = hooks
	return i
}

func (i *Import) WithAudit(rec AuditRecorder) *Import {
	i.audit = rec
	return i
}

// WithStorageGate refuses imports into projects whose store is read-only.
func (i *Import) WithStorageGate(gate importStorageGate) *Import {
	i.storage = gate
	return i
}

// WithQuota checks upload estimates against the storage quota.
func (i *Import) WithQuota(quota importQuota) *Import {
	i.quota = quota
	return i
}

// WithMaxFiles overrides DefaultPushMaxFiles; zero or less keeps the default.
func (i *Import) WithMaxFiles(n int) *Import {
	i.maxFiles = n
	return i
}

func (i *Import) MaxFiles() int {
	if i.maxFiles > 0 {
		return i.maxFiles
	}
	return DefaultPushMaxFiles
}

// TooManyFiles is the error a commit over the limit gets, for callers that
// count while receiving.
func (i *Import) TooManyFiles(n int) error {
	return tooManyPushFiles(n, i.MaxFiles())
}

// CheckAccess runs the checks every write of an import needs: project admin
// and a writable store.
func (i *Import) CheckAccess(ctx context.Context, projectID snow.ID) error {
	if !i.permUc.AdminHasProject(ctx, projectID) {
		return domain.NewErrorNoPermission()
	}
	if i.storage != nil {
		return i.storage.RequireWritable(ctx, projectID)
	}
	return nil
}

// Lookup returns the commits already imported for the given origins. It needs
// the same access as every write of an import, so a client is refused before
// it uploads anything.
func (i *Import) Lookup(ctx context.Context, projectID snow.ID, origins []string) (map[string]ImportedCommit, error) {
	if err := i.CheckAccess(ctx, projectID); err != nil {
		return nil, err
	}
	if len(origins) > MaxImportLookup {
		return nil, domain.NewErrorUser(fmt.Sprintf("lookup asks for %d origins, more than the limit of %d", len(origins), MaxImportLookup))
	}
	found := make(map[string]ImportedCommit, len(origins))
	if len(origins) == 0 {
		return found, nil
	}
	commits, err := i.branchRepo.CommitsByOrigins(ctx, projectID, origins)
	if err != nil {
		return nil, err
	}
	for _, c := range commits {
		if c.ProjectID == projectID && c.Origin != "" {
			found[c.Origin] = importedCommit(c)
		}
	}
	return found, nil
}

// EstimateUpload checks that the storage quota has room for bytes new bytes.
// It reports whether a quota was consulted.
func (i *Import) EstimateUpload(ctx context.Context, org *domain.Organization, project *domain.Project, bytes int64) (bool, error) {
	if err := i.CheckAccess(ctx, project.ID); err != nil {
		return false, err
	}
	if i.quota == nil || bytes <= 0 {
		return false, nil
	}
	return i.quota.CheckUploadHeadroom(ctx, org, project, bytes)
}

func importedCommit(c *domain.Commit) ImportedCommit {
	return ImportedCommit{Origin: c.Origin, CommitID: c.ID, CommitHash: c.Hash}
}

// Commit writes one imported commit without moving any ref. A commit whose
// origin is already stored is returned as is.
func (i *Import) Commit(ctx context.Context, projectID snow.ID, in ImportCommit) (*ImportedCommit, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if err := i.CheckAccess(ctx, projectID); err != nil {
		return nil, err
	}
	if _, err := domain.ParseOrigin(in.Origin); err != nil {
		return nil, err
	}
	if len(in.ParentOrigins) > 2 {
		return nil, domain.NewErrorUser(fmt.Sprintf("commit %s has %d parents; at most 2 are supported", in.Origin, len(in.ParentOrigins)))
	}
	if len(in.ParentOrigins) == 2 && in.ParentOrigins[0] == in.ParentOrigins[1] {
		return nil, domain.NewErrorUser(fmt.Sprintf("commit %s lists the same parent twice", in.Origin))
	}
	for _, p := range in.ParentOrigins {
		if p == in.Origin {
			return nil, domain.NewErrorUser(fmt.Sprintf("commit %s lists itself as a parent", in.Origin))
		}
	}

	known, err := i.branchRepo.CommitsByOrigins(ctx, projectID, append([]string{in.Origin}, in.ParentOrigins...))
	if err != nil {
		return nil, err
	}
	byOrigin := make(map[string]*domain.Commit, len(known))
	for _, c := range known {
		if c.ProjectID == projectID {
			byOrigin[c.Origin] = c
		}
	}
	if existing := byOrigin[in.Origin]; existing != nil {
		result := importedCommit(existing)
		return &result, nil
	}
	parents := make([]*domain.Commit, 0, len(in.ParentOrigins))
	for _, p := range in.ParentOrigins {
		parent := byOrigin[p]
		if parent == nil {
			return nil, domain.NewErrorUser(fmt.Sprintf("parent %s of %s has not been imported", p, in.Origin))
		}
		parents = append(parents, parent)
	}

	markLockableBinaries(in.Files)
	if err := checkPushContent(ctx, i.permUc, projectID, in.Files, in.Removed, i.MaxFiles()); err != nil {
		return nil, err
	}
	userID, err := i.authorUser(ctx, projectID, in, claim.UserID)
	if err != nil {
		return nil, err
	}

	var base *domain.Commit
	var parentID, parent2ID *snow.ID
	parentHashes := make([]domain.Hash, 0, len(parents))
	for n, p := range parents {
		id := p.ID
		if n == 0 {
			base, parentID = p, &id
		} else {
			parent2ID = &id
		}
		parentHashes = append(parentHashes, p.Hash)
	}
	root, err := buildNewTree(ctx, i.branchRepo, base, in.Files, in.Removed)
	if err != nil {
		return nil, err
	}
	req := buildApplyRequest(i.snowNode, projectID, 0, parentID, parent2ID, parentHashes, in.Message, in.Origin, root)
	req.UserID = userID
	req.AuthorName = capRunes(strings.TrimSpace(in.AuthorName), maxImportAuthorRunes)
	req.AuthorEmail = capRunes(strings.TrimSpace(in.AuthorEmail), maxImportAuthorRunes)
	if !in.AuthoredAt.IsZero() {
		at := in.AuthoredAt.UTC()
		req.CreatedAt = &at
	}
	stored, err := i.pushRepo.ApplyImport(ctx, req)
	if err != nil {
		return nil, err
	}
	result := importedCommit(stored)
	return &result, nil
}

// authorUser picks the Nipa user an imported commit is attributed to: the
// mapped user, else the org member whose verified email matches, else the
// importer. Only members of the project's org are ever matched.
func (i *Import) authorUser(ctx context.Context, projectID snow.ID, in ImportCommit, importer snow.ID) (snow.ID, error) {
	email := strings.TrimSpace(in.AuthorEmail)
	if in.AuthorUserID == 0 && !strings.Contains(email, "@") {
		return importer, nil
	}
	orgID, err := i.projects.ProjectOrgID(ctx, projectID)
	if err != nil {
		return 0, err
	}
	if in.AuthorUserID != 0 {
		ok, err := i.isMember(ctx, orgID, in.AuthorUserID)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, domain.NewErrorUser(fmt.Sprintf("author user %s is not a member of the project's organization", in.AuthorUserID.Base36()))
		}
		return in.AuthorUserID, nil
	}
	user, err := i.users.GetByEmail(ctx, strings.ToLower(email))
	if domain.IsErrorNotFound(err) {
		return importer, nil
	}
	if err != nil {
		return 0, err
	}
	if user.Deleted || !user.EmailVerified {
		return importer, nil
	}
	ok, err := i.isMember(ctx, orgID, user.ID)
	if err != nil {
		return 0, err
	}
	if !ok {
		return importer, nil
	}
	return user.ID, nil
}

func (i *Import) isMember(ctx context.Context, orgID, userID snow.ID) (bool, error) {
	if user, err := i.users.GetByID(ctx, userID); domain.IsErrorNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	} else if user.Deleted {
		return false, nil
	}
	_, err := i.orgs.MemberRole(ctx, orgID, userID)
	if domain.IsErrorNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

type importRefEvent struct {
	branch *domain.Branch
	before *domain.Commit
	after  *domain.Commit
	tag    *domain.Tag
}

// SetRefs points branches and tags at imported commits and sets the default
// branch, in one transaction under the project's import lock. A ref that
// cannot move is reported in its result instead of failing the call.
func (i *Import) SetRefs(ctx context.Context, projectID snow.ID, in SetRefsInput) ([]ImportRefResult, error) {
	claim, ok := domain.ClaimFromContext(ctx)
	if !ok {
		return nil, domain.NewErrorNoPermission()
	}
	if err := i.CheckAccess(ctx, projectID); err != nil {
		return nil, err
	}
	targets, err := i.refTargets(ctx, projectID, in)
	if err != nil {
		return nil, err
	}

	var results []ImportRefResult
	var events []importRefEvent
	write := func(ctx context.Context) error {
		results, events = nil, nil
		if err := i.locker.LockProject(ctx, projectID); err != nil {
			return err
		}
		for _, ref := range in.Branches {
			res, ev, err := i.setBranch(ctx, projectID, ref, targets[ref.Origin], in.Update)
			if err != nil {
				return err
			}
			results = append(results, res)
			if ev != nil {
				events = append(events, *ev)
			}
		}
		for _, ref := range in.Tags {
			res, ev, err := i.setTag(ctx, projectID, ref, targets[ref.Origin], claim.UserID)
			if err != nil {
				return err
			}
			results = append(results, res)
			if ev != nil {
				events = append(events, *ev)
			}
		}
		if in.Default != "" {
			res, err := i.setDefault(ctx, projectID, in.Default)
			if err != nil {
				return err
			}
			results = append(results, res)
		}
		return nil
	}
	err = i.tx.WithinTx(ctx, func(ctx context.Context) error {
		return auditedWrite(ctx, i.audit, write, func() *domain.AuditEntry {
			return projectAudit(domain.AuditProjectGitImported, projectID, domain.AuditTargetProject, projectID.Base36(), importAuditMetadata(in, results))
		})
	})
	if err != nil {
		return nil, err
	}
	i.emit(ctx, projectID, claim.UserID, events)
	return results, nil
}

func (i *Import) refTargets(ctx context.Context, projectID snow.ID, in SetRefsInput) (map[string]*domain.Commit, error) {
	seen := map[string]bool{}
	var origins []string
	for _, refs := range [][]ImportRef{in.Branches, in.Tags} {
		names := map[string]bool{}
		for _, ref := range refs {
			if names[ref.Name] {
				return nil, domain.NewErrorUser(fmt.Sprintf("ref %q is listed twice", ref.Name))
			}
			names[ref.Name] = true
			if !seen[ref.Origin] {
				seen[ref.Origin] = true
				origins = append(origins, ref.Origin)
			}
		}
	}
	targets := make(map[string]*domain.Commit, len(origins))
	for start := 0; start < len(origins); start += MaxImportLookup {
		end := min(start+MaxImportLookup, len(origins))
		commits, err := i.branchRepo.CommitsByOrigins(ctx, projectID, origins[start:end])
		if err != nil {
			return nil, err
		}
		for _, c := range commits {
			if c.ProjectID == projectID {
				targets[c.Origin] = c
			}
		}
	}
	for _, origin := range origins {
		if targets[origin] == nil {
			return nil, domain.NewErrorUser(fmt.Sprintf("commit %s has not been imported", origin))
		}
	}
	return targets, nil
}

func (i *Import) setBranch(ctx context.Context, projectID snow.ID, ref ImportRef, target *domain.Commit, update bool) (ImportRefResult, *importRefEvent, error) {
	res := ImportRefResult{Kind: ImportRefBranch, Name: ref.Name, CommitID: target.ID}
	if err := domain.ValidateBranchName(ref.Name); err != nil {
		res.Status, res.Message = ImportRefInvalid, err.Error()
		return res, nil, nil
	}
	branch, err := i.branchRepo.GetBranchByName(ctx, projectID, ref.Name)
	if domain.IsErrorNotFound(err) {
		existing, err := i.branchRepo.ListBranches(ctx, projectID, 1, nil, 0)
		if err != nil {
			return res, nil, err
		}
		created, err := i.branchRepo.CreateBranch(ctx, domain.Branch{
			ID:        i.snowNode.Generate(),
			ProjectID: projectID,
			Name:      ref.Name,
			CommitID:  &target.ID,
			IsDefault: len(existing) == 0,
		})
		if err != nil {
			return res, nil, err
		}
		res.Status = ImportRefCreated
		return res, &importRefEvent{branch: created, after: target}, nil
	}
	if err != nil {
		return res, nil, err
	}
	switch {
	case branch.CommitID != nil && *branch.CommitID == target.ID:
		res.Status = ImportRefUnchanged
		return res, nil, nil
	case branch.IsProtected:
		res.Status, res.Message = ImportRefProtected, "protected branches are never moved by an import"
		return res, nil, nil
	case branch.CommitID != nil && !update:
		res.Status, res.Message = ImportRefExists, "the branch already exists; import with update to fast-forward it"
		return res, nil, nil
	}
	var before *domain.Commit
	if branch.CommitID != nil {
		ancestor, err := isAncestor(ctx, i.branchRepo, projectID, *branch.CommitID, target.ID)
		if err != nil {
			return res, nil, err
		}
		if !ancestor {
			res.Status, res.Message = ImportRefDiverged, "the branch has commits the import does not contain"
			return res, nil, nil
		}
		if before, err = i.branchRepo.GetCommit(ctx, *branch.CommitID); err != nil {
			return res, nil, err
		}
	}
	if err := i.branchRepo.UpdateCommitIf(ctx, branch.ID, branch.CommitID, &target.ID); err != nil {
		return res, nil, err
	}
	branch.CommitID = &target.ID
	res.Status = ImportRefUpdated
	return res, &importRefEvent{branch: branch, before: before, after: target}, nil
}

func (i *Import) setTag(ctx context.Context, projectID snow.ID, ref ImportRef, target *domain.Commit, actor snow.ID) (ImportRefResult, *importRefEvent, error) {
	res := ImportRefResult{Kind: ImportRefTag, Name: ref.Name, CommitID: target.ID}
	if err := domain.ValidateTagName(ref.Name); err != nil {
		res.Status, res.Message = ImportRefInvalid, err.Error()
		return res, nil, nil
	}
	tag, err := i.tagRepo.GetTagByName(ctx, projectID, ref.Name)
	switch {
	case err == nil && tag.CommitID == target.ID:
		res.Status = ImportRefUnchanged
		return res, nil, nil
	case err == nil:
		res.Status, res.Message = ImportRefExists, "the tag already points at another commit; tags never move"
		return res, nil, nil
	case !domain.IsErrorNotFound(err):
		return res, nil, err
	}
	created, err := i.tagRepo.CreateTag(ctx, domain.Tag{
		ID:        i.snowNode.Generate(),
		ProjectID: projectID,
		Name:      ref.Name,
		CommitID:  target.ID,
		Message:   ref.Message,
		UserID:    actor,
	})
	if err != nil {
		return res, nil, err
	}
	res.Status = ImportRefCreated
	return res, &importRefEvent{tag: created}, nil
}

func (i *Import) setDefault(ctx context.Context, projectID snow.ID, name string) (ImportRefResult, error) {
	res := ImportRefResult{Kind: ImportRefDefault, Name: name}
	branch, err := i.branchRepo.GetBranchByName(ctx, projectID, name)
	if domain.IsErrorNotFound(err) {
		res.Status, res.Message = ImportRefInvalid, fmt.Sprintf("branch %q does not exist", name)
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if branch.CommitID != nil {
		res.CommitID = *branch.CommitID
	}
	if branch.IsDefault {
		res.Status = ImportRefUnchanged
		return res, nil
	}
	if err := i.branchRepo.SetDefaultBranch(ctx, projectID, branch.ID); err != nil {
		return res, err
	}
	res.Status = ImportRefUpdated
	return res, nil
}

func importAuditMetadata(in SetRefsInput, results []ImportRefResult) map[string]any {
	statuses := map[string]int{}
	origins := map[string]string{}
	for _, ref := range in.Branches {
		origins[ImportRefBranch+":"+ref.Name] = ref.Origin
	}
	for _, ref := range in.Tags {
		origins[ImportRefTag+":"+ref.Name] = ref.Origin
	}
	heads := []string{}
	for _, r := range results {
		statuses[r.Kind+"."+r.Status]++
		if (r.Status == ImportRefCreated || r.Status == ImportRefUpdated) && r.Kind != ImportRefDefault && len(heads) < maxAuditImportHeads {
			heads = append(heads, r.Kind+":"+r.Name+"="+origins[r.Kind+":"+r.Name])
		}
	}
	return map[string]any{
		"branches": len(in.Branches),
		"tags":     len(in.Tags),
		"commits":  in.Commits,
		"update":   in.Update,
		"default":  in.Default,
		"results":  statuses,
		"heads":    heads,
	}
}

// emit publishes the ref events after the transaction committed; a delivery
// failure never fails the import.
func (i *Import) emit(ctx context.Context, projectID, actor snow.ID, events []importRefEvent) {
	if i.hooks == nil {
		return
	}
	for _, ev := range events {
		var err error
		if ev.tag != nil {
			err = i.hooks.EmitTag(ctx, domain.WebhookEventTagCreated, projectID, ev.tag, actor)
		} else {
			err = i.hooks.EmitPush(ctx, PushEvent{
				ProjectID: projectID,
				Actor:     actor,
				Branch:    ev.branch,
				Before:    ev.before,
				AfterID:   ev.after.ID,
				Message:   ev.after.Message,
				Imported:  true,
			})
		}
		if err != nil {
			slog.Warn("emitting webhook import event failed", "project", projectID, "error", err)
		}
	}
}
