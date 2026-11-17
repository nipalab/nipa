# Changelog

## 0.2.1

- Activate for clones nested at any depth in the workspace.
- Mark as Resolved decodes unopened files as UTF-8 before checking for conflict markers.
- A lock warning skipped during the push check is still shown by the next refresh.
- Unlock and Ask to Release prefer the lock in the clone's branch scope (mainline on the default branch), like the daemon's lock states.
- A missing `nipa.path`/`NIPA_BIN` binary or a failed `nipa serve` spawn fails at once with the binary named, instead of a 10 s timeout.
- Closed synced-version documents are no longer refreshed.
- Serve synced versions under the `nipa-blob` scheme. `nipa-snapshot` is also registered by the separate `nipa-vscode` extension, which then answered for these documents, so diffs and gutter markers showed the whole file as added.

## 0.2.0

- Blame annotations next to the editor (Nipa: Toggle Blame, `nipa.blame`), with commit details and a desktop link on hover.
- Lock, Unlock and Ask to Release from the Explorer, the editor tab and the Source Control view; warnings when a changed binary needs your lock or is locked by someone else (`nipa.lockWarnings`).
- Conflicts: Take Mine, Take Theirs, Mark as Resolved (refused while markers remain) and Compare Sides.
- Switch Branch or Tag from the status bar, shelving only the local changes an update or switch would replace.
- Incoming commit count and active maintenance windows in the status bar.
- Open in Nipa Desktop and Copy nipa:// Link.

## 0.1.0

- Source Control panel for Nipa clones: status, stage, unstage, discard, push and update.
- Gutter change markers and diffs against the last synced version.
- Explorer decorations with lock states and pre-push lock and asset checks.
