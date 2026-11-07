# Changelog

## [Unreleased]

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
