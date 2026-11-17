# Changelog

## [Unreleased]

- Merge another branch into the current one, and create or delete branches,
  from the branch switcher.
- Update, branch switch, tag checkout, merge and the merge/revert/shelf banners
  ask to save modified open scenes first.
- Shelves: a Shelves tab (Apply, Keep, Delete), Shelve on the Changes and
  Staged lists, "Shelve and continue" when Update or a switch would replace
  local changes, and a pending shelf apply banner with Abort.
- Ask to release a lock held by someone else (Inspector bar, Locks tab,
  Assets menu), with a Release prompt for the holder and request counts on
  your own locks.
- Notify when free: watch a lock someone else holds and get a "Lock now"
  prompt when it is released.
- Maintenance windows: a banner, notices for scheduled/started/ended windows,
  Push/Lock (and Update/switch/merge in closed mode) refused up front, and
  refusals that name the estimated end.
- Push checks: asset budget violations and broken or unused references from
  `PushPlan`, with Push anyway.
- Lock expiry: an optional Lock expiry setting, time left in the Locks tab and
  Inspector bar, and Renew.
- History tab Load more.
- Compare for image conflicts: mine and theirs side by side with pixel stats.

- Setup tab: checks Force Text, visible meta files and `.nipaignore`, with Fix buttons
  and the Unity `.nipaignore` template.
- Install from a git URL or tarball (`make unity-pack`).

- Branch/tag switcher in the window and as a Scene view overlay.
- Conflicts tab with Mine/Theirs and UnityYAMLMerge for Unity YAML assets.
- Merge/revert pending banner (Finish, Abort, Continue, Skip).
- History tab with Open diff in Nipa Desktop.

- File locking: assets locked by others are read-only, optional strict mode,
  `MakeEditable` locks the asset and its `.meta`, saves/moves/deletes of
  locked assets are refused.
- Lock overlays, Inspector lock bar, Lock/Unlock menu entries, Locks tab.
- Push pre-check with a "Lock N files" prompt.
- Teammate events: out-of-date marker with Update, review request notices,
  lock cache refresh on released locks.

## [0.1.0]

- Version Control provider "Nipa" with Project window status overlays.
- Pending Changes window: stage, unstage, restore, push with a message, update.
- Asset and `.meta` files are staged and restored together.
- `.meta` hygiene warnings from the daemon.
- Assets are refreshed after update and restore.
