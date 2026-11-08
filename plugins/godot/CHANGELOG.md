# Changelog

## Unreleased

### Phase 1: extension MVP

- Engine-free C++17 core (`core/`): daemon client and connector, project paths
  and sidecars, status mapping, diff model with Myers line diff, and the
  session (worker thread, stream loops, events).
- godot-cpp GDExtension (`extension/`): `NipaVCSInterface` (native VCS plugin),
  the `Nipa` engine singleton and `NipaRequest`.
- GDScript add-on (`addons/nipa/`): plugin, settings, and the Nipa dock.
- Headless smoke project and gdUnit4 spec (`tests/project/`).
