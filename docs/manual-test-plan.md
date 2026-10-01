# Nipa Manual Test Plan

Manual, end-to-end tests exercising the real `nipa` CLI against a real `nipad`
server. These complement the automated tests (`internal/e2e`, unit tests) by
validating the actual binary, the on-disk `.nipa` state, restart/persistence
behavior, and the CLI user experience.

All test cases assume two people ("A" and "B") with separate working copies
when concurrency is involved; single-user cases can be run by one person with
multiple worktrees.

---

## 0. Environment setup

Pre-conditions for every test run:

1. Build everything:
   ```sh
   make build        # or: go build ./... && go build -o bin/nipad ./cmd/nipad
   ```
2. Prepare a throwaway config directory (do not touch `config.yaml` in the repo):
   ```sh
   mkdir -p /tmp/nipa-server && cp config.yaml.sample /tmp/nipa-server/config.yaml
   sed -i 's#sqlite://nipa.db#sqlite:///tmp/nipa-server/nipa.db#' /tmp/nipa-server/config.yaml
   sed -i 's#./chunks#/tmp/nipa-server/chunks#' /tmp/nipa-server/config.yaml
   ```
3. Start the server and keep it running:
   ```sh
   bin/nipad --config /tmp/nipa-server/config.yaml
   ```
   or, if the binary reads `./config.yaml` from CWD:
   ```sh
   cd /tmp/nipa-server && /path/to/bin/nipad
   ```
4. Confirm the server is up and reachable:
   ```sh
   curl -s http://localhost:6745/healthz        # http
   # or use the CLI (any network call produces a clear error if down)
   ```
5. The default server seeds an admin user from config
   (`e2eSuperAdminEmail`/`e2eSuperAdminPass` in the test env, or the values the
   server was seeded with). Note down those credentials.

Server URL used throughout: `http://localhost:6745`.

### Markup used below

- Worktree = a directory with a `.nipa/` subfolder created by `nipa clone`.
- `$WORK`, `$WORK_A`, `$WORK_B` = distinct worktree directories.
- `<org>`, `<project>` = org/project slugs from the URL path.
- Each scenario is independent unless it says "continues from TC-xx".

---

## 1. Authentication

### TC-AUTH-01. First login with valid credentials
- **Pre:** fresh worktree, no stored token.
- **Steps:** `nipa clone http://localhost:6745/<org>/<project> $WORK`; answer the
  username/password prompt with valid credentials.
- **Expected:** clone succeeds; token persisted (`$WORK/.nipa/config` exists and
  the secure store holds a token); a second command (`nipa status`) does not
  prompt again.

### TC-AUTH-02. Wrong password is rejected
- **Pre:** fresh worktree, valid clone URL.
- **Steps:** clone with a deliberately wrong password.
- **Expected:** clear "invalid credentials"-style error; command fails; no
  partial `.nipa` repository left behind (or if created, it is cleaned up).

### TC-AUTH-03. Unknown user is rejected
- **Steps:** clone/login with a username that does not exist.
- **Expected:** clear error; no token stored.

### TC-AUTH-04. Token persists across server restart
- **Steps:** (1) login successfully, (2) restart `nipad` with the same database,
  (3) run `nipa status`/`nipa update`.
- **Expected:** command works without re-prompting (refresh-token flow
  transparently re-issues tokens).

### TC-AUTH-05. Server down / unreachable host
- **Steps:** `nipa status` with server stopped, or clone to a bogus port/host.
- **Expected:** clear connection error ("connection refused", timeout); the CLI
  exits non-zero; no corruption of `.nipa`.

### TC-AUTH-06. Authentication is required to read a repository
- **Pre:** server with auth required; a worktree whose token was deleted.
- **Steps:** delete the stored token, then run `nipa update`.
- **Expected:** CLI reports the 401-style error and prompts again (if
  interactive) rather than silently succeeding.

---

## 2. Clone

### TC-CLONE-01. Clone an empty repository (no commits)
- **Pre:** a fresh org/project on the server with no pushes.
- **Steps:** `nipa clone http://localhost:6745/<org>/<project> $WORK`.
- **Expected:** succeeds; `$WORK` is empty; `nipa status` shows no files;
  `nipa branch` prints `main`.

### TC-CLONE-02. Clone a repository with content
- **Pre:** repo with several commits on `main` (incl. nested directories).
- **Steps:** `nipa clone http://localhost:6745/<org>/<project> $WORK`.
- **Expected:** working tree matches the server head exactly; file modes are
  preserved; `nipa status` shows no modifications; `.nipa/nipa.db` chunk cache is
  warm for all content.

### TC-CLONE-03. Clone to a non-empty target directory
- **Steps:** `mkdir $WORK2; echo hi > $WORK2/x`; then clone into `$WORK2`.
- **Expected:** command errors out; no `.nipa` is created in a non-empty target;
  the existing file is untouched.

### TC-CLONE-04. Clone with `-b` to select a non-default branch
- **Steps:** push work to branch `dev`, then `nipa clone -b dev <url> $WORK`.
- **Expected:** status shows `dev`; working tree = `dev` head, not `main`.

### TC-CLONE-05. Clone a nonexistent org/project
- **Steps:** `nipa clone http://localhost:6745/does-not/exist $WORK`.
- **Expected:** 404-style error; no `.nipa` left behind.

### TC-CLONE-06. Clone a subdirectory path (`/org/project/sub/dir`)
- **Pre:** repo with content under `sub/dir/`.
- **Steps:** `nipa clone http://localhost:6745/<org>/<project>/sub/dir $WORK`.
- **Expected:** materializes only files under that path (paths relative to the
  subtree root); `nipa status` matches the subtree.

### TC-CLONE-07. Clone a missing subdirectory path
- **Steps:** clone `.../<org>/<project>/nope/missing`.
- **Expected:** 404 error surfaced cleanly; no `.nipa` left behind.

### TC-CLONE-08. Clone URL malformed / wrong scheme
- **Steps:** `nipa clone not-a-url $WORK`, `nipa clone ftp://host/o/p $WORK`.
- **Expected:** clear parse/unsupported-scheme error.

---

## 3. Branching (`nipa branch`, `nipa switch`)

### TC-BR-01. Show current branch
- **Steps:** in a cloned worktree, `nipa branch`.
- **Expected:** prints the current branch name (e.g. `main`).

### TC-BR-02. List server branches (`-a`)
- **Steps:** create `dev` elsewhere, then `nipa branch -a` in the worktree.
- **Expected:** lists all branches incl. `dev`; current branch is marked.

### TC-BR-03. Create a branch (`-c dev`) and switch to it
- **Steps:** `nipa branch -c dev`.
- **Expected:** branch created on the server; local branch pointer moves to
  `dev`; working tree unchanged (content identical to where you forked);
  `.nipa/config.branch == "dev"`.

### TC-BR-04. Created branch forks from the pushed commit
- **Pre:** worktree has pushed commits (so it is pinned to a commit id).
- **Steps:** (1) in the other worktree, push more commits to `main`;
  (2) in the pinned worktree create `nipa branch -c fork`; (3) switch others to `fork`.
- **Expected:** `fork` is created at the *pinned* commit, not at `main`'s new head.

### TC-BR-05. Switch to an existing branch
- **Steps:** `nipa switch dev`.
- **Expected:** tree replaced with `dev` content; deleted/added files reflect the
  branch; those file bytes land in the local chunk cache.

### TC-BR-06. Switch to a nonexistent branch
- **Steps:** `nipa switch nope`.
- **Expected:** clear "branch not found" error; working copy and current branch
  unchanged.

### TC-BR-07. Switch with staged changes is blocked
- **Steps:** `nipa add a.txt` (leave it staged), then `nipa switch dev`.
- **Expected:** command errors ("cannot switch with staged changes"); nothing is
  staged-cleared or switched.

### TC-BR-08. Switch with a modified (unstaged) file
- **Steps:** edit `a.txt` without staging, then `nipa switch dev`.
- **Expected:** switch succeeds, replacing content (documented behavior: no
  prompt for unstructured/unstaged changes yet). Note observed behavior.

### TC-BR-09. Create branch name already exists
- **Steps:** `nipa branch -c main`.
- **Expected:** clear "branch exists" error; current branch unchanged.

### TC-BR-10. Branch with invalid name
- **Steps:** `nipa branch -c "has space"`, `nipa branch -c "UPPER"`,
  `nipa branch -c "" `.
- **Expected:** the server rejects invalid branch names with a clear error.

---

## 4. Release tags (`nipa tag`, `nipa switch --tag`)

### TC-TAG-01. Create a tag at the checked-out commit
- **Pre:** worktree has pushed commits.
- **Steps:** `nipa tag -c v1.0.0 -m "first release"`, then `nipa tag`.
- **Expected:** tag created at the locally pinned commit; `nipa tag` lists
  `v1.0.0` with its commit id and message; the branch head is unchanged.

### TC-TAG-02. Create a tag from a branch or commit
- **Steps:** `nipa tag -c v1.0.1 --branch dev`; then
  `nipa tag -c v1.0.2 --commit <base36 id from nipa log>`.
- **Expected:** each tag points at the resolved commit (branch head at creation
  time, or the exact commit id); `--branch`/`--commit` together are rejected.

### TC-TAG-03. Duplicate tag name is rejected
- **Steps:** `nipa tag -c v1.0.0` again.
- **Expected:** 409-style "tag already exists" error; existing tag unchanged.

### TC-TAG-04. Delete frees the name
- **Steps:** `nipa tag -d v1.0.0`, list tags, recreate `v1.0.0`.
- **Expected:** delete succeeds; the name disappears from the list; recreation
  succeeds; deleting a missing tag errors 404.

### TC-TAG-05. Detached checkout (`nipa switch --tag`)
- **Pre:** tag `v1.0.0` exists and the branch has newer commits.
- **Steps:** `nipa switch --tag v1.0.0`, then `nipa status`.
- **Expected:** working tree matches the tagged commit; `status` prints
  `HEAD detached at tag "v1.0.0"`; `.nipa/config.branch` is unchanged and a
  `head` marker names the tag.

### TC-TAG-06. Update while detached re-syncs the tag
- **Steps:** `nipa update`, then `nipa status`.
- **Expected:** working tree stays at the tag (no move to the branch head) and
  `status` still reports the detached tag.

### TC-TAG-07. Push/merge/revert are blocked while detached
- **Steps:** stage a change and run `nipa push -m x`; run `nipa merge main`;
  run `nipa revert <commit>`.
- **Expected:** each refuses with a "HEAD is detached at tag" error and a
  `nipa switch <branch>` hint; nothing is committed or rewritten.

### TC-TAG-08. Switch back to a branch re-attaches
- **Steps:** `nipa switch main`, then `nipa status`.
- **Expected:** working tree at the branch head; `status` prints `On branch
  main`; the `head` marker is gone; a normal push works again.

### TC-TAG-09. Invalid tag names
- **Steps:** `nipa tag -c "has space"`, `nipa tag -c "a/b"`.
- **Expected:** clear 400-style invalid-name errors; nothing is created.

---

## 5. Working copy status (`nipa status`), `add`, `remove`

### TC-WC-01. Fresh clone shows clean status
- **Expected:** `nipa status` prints `On branch main` (header only); exit 0.

### TC-WC-02. Status letters
- **Steps:** create new file `new.txt` (?), modify tracked `a.txt` (M), stage
  `new.txt` (A), delete tracked `b.txt` on disk (!), and `touch` a `/tmp/watchme`.
- **Expected:** each file mapped to the right letter
  (`A` staged, `M` modified, `?` untracked, `!` missing); untracked paths under `.nipa/` never appear.

### TC-ADD-01. Add a file, then a directory
- **Steps:** `nipa add a.txt`; `nipa add subdir` (with several files inside).
- **Expected:** files listed by `nipa status` as `A`; `nipa remove a.txt` unstages
  a file; `nipa remove -a` unstages everything.

### TC-ADD-02. Add a nonexistent path
- **Steps:** `nipa add ghost.txt`.
- **Expected:** clear "does not exist" error; no phantom staged entries.

### TC-ADD-03. Add a file inside `.nipa/`
- **Steps:** `nipa add .nipa/config` (or any path under `.nipa`).
- **Expected:** rejected with a "cannot push path inside .nipa" error.

### TC-REM-01. Remove exactly what was staged
- **Steps:** `nipa add a.txt b.txt`; `nipa remove a.txt`.
- **Expected:** only `b.txt` remains staged in `status`.

---

## 6. Push (`nipa push -m "..."`)

### TC-PUSH-01. First push to a fresh repository
- **Steps:** clone empty repo, add a `.gitignore`-like file + a text file, push.
- **Expected:** push succeeds; `nipa branch` still `main`; `nipa status` clean;
  fresh clone elsewhere sees the files.

### TC-PUSH-02. Push a staged removal
- **Steps:** push file `keep.txt` and `gone.txt`; then `nipa add gone.txt` after
  `rm gone.txt` (or `nipa remove` semantics), push.
- **Expected:** server tree no longer contains `gone.txt`; a fresh clone has only
  `keep.txt`.

### TC-PUSH-03. Push with no staged files
- **Steps:** clean worktree, `nipa push -m "nothing"`.
- **Expected:** clear "nothing staged" error; no commit created on server.

### TC-PUSH-04. Push with empty/whitespace message
- **Steps:** `nipa push -m ""`, `nipa push -m "   "`.
- **Expected:** clear "commit message required" error; nothing pushed.

### TC-PUSH-05. Push without -m flag
- **Steps:** `nipa push` with files staged.
- **Expected:** an editor/prompt or a clear "message required" error (documented
  behavior); no silent default message.

### TC-PUSH-06. Push a binary file
- **Steps:** push a PNG/JPEG/ZIP (few MB).
- **Expected:** file pushed as binary (`is_binary` true); clone/download reproduces
  byte-for-byte (compare checksums `sha256sum`).

### TC-PUSH-07. Push a large file (100 MB+)
- **Steps:** generate `dd if=/dev/urandom of=big.bin bs=1M count=200`, add, push.
- **Expected:** progress bar shows upload progress; completion is successful; a
  fresh clone re-downloads with progress and the file checksum matches.

### TC-PUSH-08. Stale base push is rejected (concurrency)
- **Pre:** worktrees A and B on `main`, both up to date.
- **Steps:** (1) A pushes commit 1; (2) B edits another file and pushes.
- **Expected:** B's push fails with a "branch has moved / base mismatch" error;
  B must `nipa update` (or `nipa switch main`) before pushing successfully.

### TC-PUSH-09. Push from a subdirectory clone is blocked
- **Steps:** in a subpath clone, stage a file and push.
- **Expected:** clear "not supported from subdirectory clone" error; nothing sent.

### TC-PUSH-10. Push only uploads chunks the server lacks
- **Steps:** push the same content from worktrees A and B (or re-push unchanged
  content after an update). Watch server side (`LOG_LEVEL: debug` chunk upload log
  and storage dir).
- **Expected:** server reports `skipped` for already-stored chunks; no duplicate
  content written to the chunk storage dir.

### TC-PUSH-11. Push keeps staged content cached locally after success
- **Steps:** push `a.txt`, then immediately modify nothing and `nipa status`.
- **Expected:** `a.txt` content is recoverable from the local cache (no re-download
  needed) — observable via a subsequent `nipa update`/`switch` not transfering that file.

---

## 7. Update (`nipa update`)

### TC-UPD-01. Update applies another worktree's push
- **Pre:** A pushes a change; B is one commit behind.
- **Steps:** in B, `nipa update`.
- **Expected:** B's tree now matches A's head; added/changed/removed files updated;
  status clean; chunk cache avoids re-fetching unchanged files.

### TC-UPD-02. Update when already up to date
- **Steps:** `nipa update` twice in a row.
- **Expected:** second run reports "already up to date" (or no-op) and transfers
  nothing.

### TC-UPD-03. Update with staged changes
- **Steps:** stage a file, then `nipa update`.
- **Expected:** clear "cannot update with staged changes" error; nothing changes.

### TC-UPD-04. Update picks up a merge commit (non-fast-forward history)
- **Pre:** server branch head has a merge commit with two parents.
- **Steps:** `nipa update`.
- **Expected:** tree materialized from the merge result correctly; local pinned
  commit = merge commit.

### TC-UPD-05. Update large repo efficiency
- **Pre:** repo with 10k+ files, one file changed since last update.
- **Steps:** `nipa update`.
- **Expected:** only the changed file's bytes are transferred (observable in debug
  logs); no full-tree rewrite locally (timing sane).

---

## 8. Merge (`nipa merge <branch>`)

Pre: every scenario uses two worktrees on `main` and `feature`.

### TC-MRG-01. Fast-forward merge
- **Pre:** `feature` forks from `main`, gets commits; `main` unchanged.
- **Steps:** in `main` worktree, `nipa merge feature`.
- **Expected:** outcome fast-forward; `main` now at `feature` head; `feature`
  commits appear in `main` history; no merge commit (single parent); local
  commit == `feature` head.

### TC-MRG-02. `--no-ff` forces a merge commit on fast-forwardable history
- **Steps:** same as TC-MRG-01 but with `nipa merge feature --no-ff`.
- **Expected:** a merge commit with `parent1=main`, `parent2=feature` is created;
  message defaults to `Merge branch 'feature' into 'main'`; tree = feature head.

### TC-MRG-03. Custom merge message (`-m`)
- **Steps:** `nipa merge feature -m "Ship feature"`.
- **Expected:** merge commit message is exactly `Ship feature`.

### TC-MRG-04. Clean three-way merge
- **Pre:** base `a.txt`; `feature` edits line 2; `main` edits line 3.
- **Steps:** `nipa merge feature`.
- **Expected:** merged file has both edits with no conflict markers; outcome
  "merge committed"; `main` head is a merge commit with two parents; no pending
  merge state (`.nipa` clean); fresh clone == merged content.

### TC-MRG-05. Conflict produces markers and pending state
- **Pre:** `feature` and `main` both edit the *same* line.
- **Steps:** `nipa merge feature`.
- **Expected:** command reports conflicts (`a.txt`), exits non-zero (or with a
  message to resolve); working copy `a.txt` contains `<<<<<<< ours ... =======
  ... >>>>>>> theirs` markers; `.nipa` holds pending merge state.

### TC-MRG-06. Resolve a conflict and complete the merge
- **Continues from TC-MRG-05.**
- **Steps:** edit `a.txt` to the wanted content; `nipa add a.txt`; `nipa push -m
  "resolve conflict"`.
- **Expected:** push succeeds (does NOT hit a stale-base error); commit is a merge
  commit with two parents; pending state cleared; fresh clone == resolved content.

### TC-MRG-07. `--abort` cancels a conflicted merge
- **Continues from TC-MRG-05 (or any conflict).**
- **Steps:** `nipa merge --abort`.
- **Expected:** working copy restored to `main` head (no markers); local branch
  and snapshot untouched; pending merge state cleared; `nipa status` clean; no
  merge commit created on the server.

### TC-MRG-08. `--abort` with no merge in progress
- **Steps:** `nipa merge --abort` on a clean worktree.
- **Expected:** clear "no merge in progress to abort" error.

### TC-MRG-09. Merge when the target is already up to date
- **Steps:** create branch at `main`, no commits, `nipa merge feature` where
  `feature` has no commits.
- **Expected:** "no commits to merge" error (feature empty) OR "up to date"
  (feature == main head). Document which.

### TC-MRG-10. Merge the target into itself
- **Steps:** `nipa merge main` while on `main`.
- **Expected:** clean "up to date"-style result; no commit created.

### TC-MRG-11. `--ff-only` when a merge commit is required
- **Pre:** diverged history (main moved, feature moved).
- **Steps:** `nipa merge feature --ff-only`.
- **Expected:** errors out with "cannot fast-forward" message; nothing changes.

### TC-MRG-12. `--ff-only` and `--no-ff` together
- **Steps:** `nipa merge feature --ff-only --no-ff`.
- **Expected:** clear "mutually exclusive" usage error; nothing happens.

### TC-MRG-13. Merge with staged changes
- **Steps:** `nipa add x` then `nipa merge feature`.
- **Expected:** clear "cannot merge with staged changes" error.

### TC-MRG-14. Merge with a pending merge already in progress
- **Continues from TC-MRG-05.**
- **Steps:** run `nipa merge feature` again without resolving.
- **Expected:** clear "merge already in progress" error.

### TC-MRG-15. Concurrent modification between conflict and resolution
- **Continues from TC-MRG-05.** While B fixes the conflict, A pushes a commit to
  `main`.
- **Steps:** B resolves + pushes.
- **Expected:** B's push fails with the stale-base error; B runs `nipa update`
  then must reconcile, proving the base is the *functional* `main` head, not a
  pre-merge snapshot.

### TC-MRG-16. Binary file conflict → documented marker behavior
- **Pre:** same binary file changed on both branches.
- **Steps:** `nipa merge feature`.
- **Expected:** reports a binary conflict; working copy keeps the target version
  (`ours`) with *no* text markers; pending state lists the file; resolution acts
  on top of that.

### TC-MRG-17. Merge from a subdirectory clone
- **Steps:** in a subpath clone, `nipa merge feature`.
- **Expected:** clear "not supported from subdirectory clone" error.

### TC-MRG-18. Merge branch with no commits to merge
- **Steps:** `nipa branch -c empty` (no pushes), switch to main, `nipa merge empty`.
- **Expected:** clear "no commits to merge" error; no-op.

---

## 9. Binary / large-object behavior

### TC-BIN-01. Binary detection and round-trip
- **Steps:** push files with different extensions/types: `.txt`, `.png`, `.zip`,
  `.exe`-like bytes, file with embedded NUL.
- **Expected:** non-text files flagged binary (visible via API/`internal` or by
  behavior: no line-based merge attempts); downloaded bytes identical
  (`sha256sum` match on both ends).

### TC-BIN-02. Text file with NUL byte is treated as binary
- **Steps:** craft `printf 'a\0b\n' > hybrid.bin`, push, then merge a change on
  `feature` touching it.
- **Expected:** treated as binary (no text markers), target version kept on
  conflict.

### TC-BIN-03. Large binary partial-update efficiency
- **Pre:** 1 GB binary on `main`; worktree B cloned (cache warm).
- **Steps:** A modifies the last 200 MB of the file and pushes; B runs `nipa update`.
- **Expected:** only the changed chunk range transfers, not all 1 GB (verify via
  debug logs / chunk store activity). FastCDC chunking boundary detection works.

---

## 10. Two-worktree concurrency scenarios

### TC-CONC-01. Concurrent pushes to different files
- **Pre:** worktree A and B both fresh on `main`, both at head.
- **Steps:** A pushes `a.txt`; B then pushes `b.txt`.
- **Expected:** B gets stale-base error (documented optimistic-concurrency
  behavior); after `nipa update` B can push.

### TC-CONC-02. Same file edited on both worktrees, sequential
- **Steps:** A pushes a change to `a.txt`; B edits `a.txt`, runs `nipa update`.
- **Expected:** B's file now changed (or clear conflict-free overwrite if B had no
  local change); B re-applies & pushes.

### TC-CONC-03. Two worktrees + shared server after server restart
- **Steps:** A pushes; restart server; B updates and pushes.
- **Expected:** everything consistent (commit ids/tree hashes survive restart);
  no chunk or commit loss.

---

## 11. Persistence & crash safety

### TC-PERS-01. Server DB persists across restart
- **Steps:** seed several repos/commits; stop server; start again on same DB.
- **Expected:** `nipa branch -a`, clone of old data, and past commits all intact.

### TC-PERS-02. Chunk store persists and dedupes across restart
- **Steps:** push big file; restart server; push the same content again.
- **Expected:** server reports the chunks as already present (skipped); storage
  dir did not grow by the full file size.

### TC-PERS-03. Kill the client mid-push
- **Steps:** start a large upload, kill `nipa push` (e.g. Ctrl-C).
- **Expected:** `nipa status`/next push still work; `.nipa` is consistent
  (staged state intact or cleanly reportable); a follow-up push succeeds.

### TC-PERS-04. Kill the client mid-download (clone/update)
- **Steps:** cancel a large clone/update.
- **Expected:** partial `.nipa`/worktree is usable or clearly re-runnable:
  re-running `nipa clone`/`update` at the same target either errors loudly or
  completes correctly; no corrupted DB lock.

### TC-PERS-05. Re-run `nipa clone` over an existing clone
- **Steps:** clone `$WORK`; then clone the same URL into `$WORK` again.
- **Expected:** no double-init errors; `.nipa` config valid; either a clean
  error for non-empty target or an idempotent re-clone (document behavior).

---

## 12. CLI UX & error handling

### TC-CLI-01. `nipa --help` and per-command `--help`
- **Expected:** every implemented command appears with usage; unknown flags fail
  with usage output.

### TC-CLI-02. Unknown command
- **Steps:** `nipa frobnicate`.
- **Expected:** "unknown command" error + suggestion of similar commands.

### TC-CLI-03. Missing required arguments
- **Steps:** `nipa clone`, `nipa merge`, `nipa push` with no args as required.
- **Expected:** clear "requires arguments"-style errors with usage hints.

### TC-CLI-04. Server errors are surfaced with actionable messages
- **Steps:** clone nonexistent project (404), stale push (409/422-style),
  duplicate branch name.
- **Expected:** messages mention the server-provided code/message, not raw stack
  traces; exit codes non-zero for failures.

### TC-CLI-05. Progress reporting
- **Steps:** large push/update/clone with `--progress` (or default TTY).
- **Expected:** upload and download progress reports with byte counts; no panic
  when progress is requested in non-TTY contexts.

### TC-CLI-06. Idempotent `nipa update` / `nipa status` after each scenario
- **Expected:** after every push/merge/switch, `nipa update` reports up-to-date
  and `nipa status` is clean — the CLI ends each scenario in a consistent state.

---

## 13. Suggested priority / smoke suite

Minimum set to run before a release:

1. TC-AUTH-01, TC-AUTH-04
2. TC-CLONE-01, TC-CLONE-02, TC-CLONE-04
3. TC-BR-03, TC-BR-05, TC-BR-07
4. TC-TAG-01, TC-TAG-05, TC-TAG-07
5. TC-PUSH-01, TC-PUSH-02, TC-PUSH-06, TC-PUSH-08
6. TC-UPD-01, TC-UPD-03
7. TC-MRG-01, TC-MRG-04, TC-MRG-05, TC-MRG-06, TC-MRG-07
8. TC-BIN-01, TC-BIN-03
9. TC-CLI-04, TC-CLI-05

Anything the testers mark as "blocked/not implemented yet" should be recorded
with the observed CLI output so it can be turned into a regression test.