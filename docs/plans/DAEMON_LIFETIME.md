# Daemon lifetime plan

Plan for stopping a `nipa serve` daemon that nobody uses any more. Not
started; written 2026-11-20 to pick up later.

## Today

The desktop connector (`desktop/internal/connector`) attaches to a live daemon
through `daemon.json` or starts `nipa serve` detached (own session / process
group). Quitting the app never stops it (`docs/DESKTOP.md`: "The daemon
outlives the app on purpose"). That is deliberate:

- **One daemon per user, shared.** The Unity plugin, the Godot plugin and the
  VS Code extension attach to the same daemon. Stopping it on app quit would
  cut an open engine editor off mid-session (lock badges, status, the push
  lock check). The VS Code extension already avoids `UnwatchRepo` because it
  is not ref-counted.
- **The app cannot tell who else is connected.** Even when the app started
  the daemon, a plugin may have attached since; the daemon keeps no client
  count.
- **Closing the window is not always quitting.** With "keep in tray" the app
  keeps running; notifications and the `--background` autostart depend on
  the daemon.
- **Operations would be cancelled.** `Shutdown` drains by cancelling queued
  and running operations, so quitting during a long push or update would
  abort it.
- **Warm start.** Reopening the app reuses the daemon's watched roots, cached
  handles and reference graphs instead of a cold start (up to 10 s).

The cost: once the app is closed and no plugin is used, `nipa serve` keeps
running indefinitely with its file watchers and a small amount of memory.

## Plan

### 1. Idle timeout in the daemon (main fix)

The daemon exits by itself when it has been idle long enough. No client has
to decide for the others.

- `nipa serve --idle-timeout <duration>` (default `30m`, `0` = never), also
  `NIPA_DAEMON_IDLE_TIMEOUT` like the other daemon env knobs.
- Idle means all of:
  - no open server stream (`WatchChanges`, `WatchEvents`, `OpEvent` streams,
    NDJSON streams on the JSON bridge, which reach the gRPC listener too);
  - no queued or running operation in any root's coordinator;
  - no RPC received during the timeout.
- Track it with a stream counter and a last-activity timestamp updated by a
  unary and a stream interceptor (one place, covers the JSON bridge since it
  calls the daemon's own gRPC listener). A ticker checks the condition.
- On timeout: the same path as `Shutdown` (drain, close handles, remove
  `daemon.json`), logged once with the idle duration.
- Race with a connecting client: a client that read `daemon.json` just before
  removal fails its `Ping`; the connector already falls back to starting a new
  daemon, the plugins' connectors (`Connector` in Unity Core, Godot `core`
  connector, VS Code `src/daemon`) must do the same. Check each one.
- The desktop app keeps `WatchEvents`/`WatchChanges` streams open while it
  runs (also hidden in the tray), so it keeps the daemon alive without extra
  pings. Verify the tray-only state really holds a stream.

### 2. Autostart

The login item starts the app with `--background`, which starts the daemon.
With the app in the tray its streams keep the daemon alive, so nothing
changes. If the background app is not kept in the tray, the daemon simply
idles out like any other.

### 3. Optional: stop on quit (opt-in)

A desktop setting "Stop the background service when Nipa quits" (off by
default, Settings > Background). On a real quit (not close-to-tray) the app
calls `Shutdown` only when no operation is running (`op:progress` state), and
only for a daemon whose version matches. Plugins attached at that moment lose
the daemon and restart one through their connectors. Needs new UI text in
every catalog.

## Tests

- Daemon unit tests with a fake clock: idle with no activity exits; an open
  stream, a running operation or a recent RPC keeps it alive; `0` disables.
- Connector tests: a daemon that idled out between `daemon.json` read and
  `Ping` leads to a fresh start.
- Suite (`tests/suite`): `nipa serve --idle-timeout 2s` exits and removes
  `daemon.json`; a client attaching afterwards starts a new one.

## Docs to update when implemented

`docs/DAEMON.md` (Shutdown section, flags), `docs/DESKTOP.md` (connector
paragraph), `AGENTS.md` (`nipa serve` flow).
