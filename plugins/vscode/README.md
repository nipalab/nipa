# Nipa for VS Code

Nipa version control in VS Code's Source Control view. The extension talks to
the local `nipa serve` daemon over its JSON bridge, so it shares the daemon,
the session and the file watcher with the Nipa desktop app and the engine
plugins.

## Features

- A Source Control panel for each Nipa clone in the workspace, including
  nested clones, with **Staged Changes**, **Changes** and **Conflicts** groups.
- Stage, unstage and discard files from the panel or its context menus.
- Push the staged changes with the message box (Ctrl+Enter). If nothing is
  staged, the extension offers to stage the tracked changes first.
- Before a push, warnings for asset budget violations, broken asset
  references, and binaries that need a lock or are locked by someone else.
- Update (get the latest version of the branch) with progress and
  cancellation.
- Change markers in the editor gutter and "Open Changes" diffs against the
  last synced version.
- Status letters and lock state on files in the Explorer and in the panel.
- The current branch, or the detached tag, and a pending merge, revert or
  shelf in the status bar. Click the branch to switch to another branch or
  check out a tag. Incoming commits show on the Update button, and an active
  maintenance window shows as its own item.
- Before an update or a switch, if it would replace local changes, the
  extension offers to shelve just those files first.
- Lock, Unlock and Ask to Release from the Explorer, the editor tab or the
  Source Control view. Locks can cover a file or a whole folder. When a
  changed binary needs your lock, or someone else holds it, you get one
  warning with a quick action.
- Conflicts: Take Mine, Take Theirs and Compare Sides for any file. Text files
  open with VS Code's conflict markers, and Mark as Resolved works only once
  no markers remain.
- Blame (Nipa: Toggle Blame): the author and age of each run of lines,
  "Not pushed" for local lines, and a hover card linking to the commit in
  the desktop app.
- Open in Nipa Desktop and Copy nipa:// Link for any file or folder.

## Requirements

You need the `nipa` CLI. The extension attaches to a running daemon, or
starts `nipa serve` when none is running. It looks for the CLI in this order:

1. the `nipa.path` setting
2. the `NIPA_BIN` environment variable
3. `PATH`

## Settings

| Setting | Default | Description |
|---|---|---|
| `nipa.path` | empty | Path to the `nipa` CLI |
| `nipa.lockRefreshSeconds` | 30 | How often lock states are fetched from the server |
| `nipa.lockWarnings` | true | Warn when a changed binary needs your lock or is locked by someone else |
| `nipa.blame` | false | Show blame annotations (toggled by Nipa: Toggle Blame) |

## Development

```sh
npm ci
npm run lint      # tsc
npm test          # vitest with an 80% coverage gate
npm run build     # bundle to dist/extension.js
npm run package   # .vsix into ../../bin
```

Press F5 with a launch configuration of type `extensionHost` to try the
extension on a clone. `NIPA_VSCODE_DAEMON_BIN=/path/to/nipa npm test` also runs
the integration test against a real `nipa serve`.
