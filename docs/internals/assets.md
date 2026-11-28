# Asset pipeline internals

Agent-oriented implementation notes (moved from AGENTS.md). User-facing design docs live one level up in `docs/`.

- **Asset previews** (`internal/preview/`): pure package for the desktop GUI's
  visual diffs (design and PR roadmap in `docs/DESKTOP.md`) — `Classify`
  (viewer kind by extension + binary flag), `DecodeImage`/`DecodeImageAt`
  (PNG/JPEG/GIF/BMP/TIFF/WebP via stdlib + `golang.org/x/image`; own decoders
  for TGA, DDS and KTX2 with BC1–BC7/BC6H and mip/face `Select`;
  `DecodeImageForSize` is the thumbnail variant: DDS/KTX2 decode the smallest
  mip whose longer side still covers the requested size and report the
  selected level and its size through `Image.SurfaceSize`, other formats
  decode in full; EXR, Radiance
  HDR and PSD/PSB composite + layer list; float sources keep `Image.HDR`;
  `DecodeModel` glTF/GLB/OBJ stats with world bounds and referenced files;
  `DecodeAudio` WAV/AIFF/FLAC/Vorbis/MP3 analysis (peaks, spectrogram,
  BS.1770 loudness, playback WAV); `ParseTable`/`DiffTables` CSV/TSV cell
  diffs; `ParseWorkbook` `.xlsx` (zip + XML, cached values only, shared/inline
  strings, sheet, part, cell and decompressed-size caps, fuzzed) behind
  `ParseSheets`/`DiffSheets` (sheets paired by name); `ParseUnityYAML` (Force Text Unity files → `Scene` objects with
  flattened props, GUIDs through a caller-supplied resolver) and
  `DiffScenes` (ID-matched object/prop/dependency changes, `KindScene`);
  `ParseGodotResource` (Godot text resources over `internal/godotres`,
  nodes keyed by path, ext/sub resource references rendered);
  `ProbeVideo` MP4/MOV (ISO BMFF) and WebM/MKV (EBML) container stats with
  box/element caps; `DecodeFont` TTF/OTF/WOFF1 over `font/sfnt` (names,
  metrics, coverage ranges, per-script counts), `RenderFontSheet`,
  `CompareFonts`; `DecodeThumbnail` Blender `TEST` blocks (classic and 5.0
  headers, gzip/zstd) and Unreal package PNG/JPEG thumbnails, fixtures from
  `testdata/gen/media`;
  `MaxPixels` checked before allocating, half of it for float), `Downscale`/
  `DownscaleFloat`/`PackHDR` and `CompareImages` (changed pixels/ratio/max
  delta/bbox; transparent pixels compare equal). Block decoders are checked
  against bcdec goldens (`testdata/bcn`, generators in `testdata/gen`).
  Fuzzed; keep it free of client imports like `internal/diff`.
  `internal/assetdiff` (pure, same rule) builds on it: `Compare(ctx, Loader,
  old, new *Ref, Options)` loads both versions through a caller `Loader`
  (`ErrMissing` = added/removed side, `ErrTooLarge` per-kind limits) and
  returns a JSON-friendly `Result` per kind (scene changes capped by
  `MaxChanges`, image stats + optional PNG thumbnails, model/audio stats and
  deltas, table rows capped by `MaxRows` (workbooks: `Options.Sheet`/`PickSheet`
  pick the sheet, `sheets[]` lists every sheet's status and counts), text line counts, sizes otherwise);
  `Summarize` turns items into sorted one-line `Headline`s capped by
  `MaxFiles`. It also owns `SceneEngine`/`UnityProject`/`BuildGUIDIndex`/
  `ReadGUID`/`WorkingGUIDs` (the desktop keeps its cached index) and
  `MergeGodot` (the daemon's `MergeGodotResource` core over `godotres.Merge3`);
  `unitymeta.ChangesFromStatus` is the status → lint change adapter behind
  `MetaWarnings`.
  `internal/unitymeta` (pure, `fs.FS` based) lints Unity `.meta` sidecars
  for pending changes: missing, orphaned, not staged, GUID changed.
  `internal/godotmeta` is its Godot twin for `.import`/`.uid` sidecars
  (nearest `project.godot`, `.gdignore`/hidden dirs skipped, `UID` parser).
  `internal/godotres` (pure) parses `.tscn`/`.tres`/`project.godot`/
  `.import` into header, ordered sections (attrs + raw multi-line props) and
  `Section.Identity()`, and `Write` round-trips the input byte for byte
  (fuzzed). `Merge3(base, ours, theirs)` merges sections by identity and
  properties/attributes by key (ours' order, theirs' additions after their
  predecessor), renumbers colliding new ext/sub resource ids with their
  `ExtResource()`/`SubResource()` references, shifts `load_steps`, and flags
  conflicts (same key changed differently, delete vs modify, references,
  parents or order the merge broke) instead of writing markers; fuzzed for
  `Merge3(x,x,x)==x` and `Merge3(b,o,b)==o`. The daemon's
  `MergeGodotResource` exposes it.

Flow for asset budgets (`.nipaassets.yml` at the repo root, versioned and kept
in sparse manifests like `.nipaignore`; reference in `docs/AI.md`): the pure
`internal/assetbudget` parses it (`go.yaml.in/yaml/v3` with `KnownFields`,
`version` 1, rules of `paths` globs in `pathmatch` syntax plus
`max_bytes` (int or human size), `max_texture_size`, `power_of_two`,
`max_triangles`, `max_vertices`, `max_audio_seconds`, `max_peak_dbfs`,
`name_pattern`, `require_provenance`), merges every matching rule in file order (a later rule
overrides only the keys it sets; violations name the rule that set the limit)
and `Check`s `assetdiff.Result`s on the new side only (removed files skipped,
undecoded files only checked for size/name; sorted by path, limit).
`CheckWith(cfg, results, CheckOptions{HasRecord})` adds the
`require_provenance` violation (`no provenance record`); `CheckChanges` reads
`.nipaprovenance.yml` from the new side through the loader
(`assetscan.LoadProvenance`) only when a covered file requires it.
`internal/client/assetscan` is the shared client runner: `Enumerate` (the
`nipa diff`-style change list as `assetdiff.Item`s, also used by
`nipa_describe_changes`), `BlobLoader`/`SideRef`/`ChangeItem`, and
`Check`/`CheckChanges`, which decode only covered images/models/audio with
matching limits (`Config.NeedsDecode`). Surfaces: `nipa assets check [<rev>
[<rev>]] [--staged] [--json] [-- <path>...]` (`cli/assets.go`, exit 2 via
`cli.ErrBudgetViolations`), the MCP read tool `nipa_asset_check`, and
`nipa push --dry-run` (`asset_violations` in JSON, warning block in text;
offline over the plan's staged changes, assembled by `assetscan.PushReport`
together with the impact below and shared with the daemon `PushPlan`). The budget always comes from the
working copy; push itself does not enforce it.

Flow for AI asset provenance (`.nipaprovenance.yml` at the repo root,
versioned, in `rootTeamFiles` so sparse manifests keep it; reference in
`docs/AI.md`): the pure `internal/provenance` (no client imports) parses it
(`version` 1, `KnownFields`, paths normalized like `NormalizePathPrefix`,
`source` `png-text|c2pa|manual`, `c2pa_verified` only with `c2pa`) and
`Marshal`s sorted paths in a fixed field order so disjoint additions merge as
text; `Set`/`Get`/`Delete`/`Move`, `HashContent` (`blake3:<hex>` of the raw
bytes, independent of chunker encodings), `PromptHash` (SHA-256; raw prompts
are never stored) and `Status` (`recorded`/`modified`/`edited`/`missing`).
`Extract` reads PNG (`IDAT` skipped by `Seek`, chunks ≤8 MiB, `zTXt`/`iTXt`
inflate ≤1 MiB, A1111 `parameters`, ComfyUI `prompt` graph: first
`KSampler*` seed, `CheckpointLoader*` model, positive `CLIPTextEncode*` text
hash) and C2PA JUMBF (PNG `caBX`, JPEG APP11 reassembled by box instance and
sequence; active manifest's `c2pa.claim*` via the bounded in-package CBOR
reader, `c2pa.actions*` AI `digitalSourceType`; never verified). Fuzzed
(`FuzzParseFile`, `FuzzExtractPNG`, `FuzzExtractJPEG`, `FuzzParseC2PA`,
`FuzzDecodeCBOR`) with bomb tests; fixtures in `testdata` are regenerated by
`NIPA_PROVENANCE_GEN=1 go test -run TestWriteFixtures`. Client side
(`usecase/provenance.go`): the file's presence is the opt-in
(`LoadProvenance` nil otherwise). `WorkingCopy.AddWithReport` (`Add` wraps
it; CLI, daemon `Stage` and MCP `nipa_add` all go through it) runs after
`withoutUnchanged` unless `AddOptions.NoProvenance`: records whose file is
gone follow a staged path with the same content hash (`Move`, no
re-extraction), new supported files (≤512 MiB) get `Extract` + content hash +
`recorded_at`, existing records are never overwritten, staged deletions drop
what is left, and a changed file is written atomically (`SaveProvenance`) and
staged in the same `StageAddMany`. `Remove` (unstage) drops records of
untracked paths only and re-stages the file. `assetscan.ScanProvenance`
builds the report (since a revision against the working copy via `Enumerate`,
else recorded + `require_provenance`-covered tracked paths; orphaned records;
totals by tool/model) behind `nipa provenance init|show|set|report`
(`cli/provenance.go`, `nipa add --no-provenance`) and the MCP read tool
`nipa_provenance` (`tools_provenance.go`, `max_files` 200/2000).
`WorkingCopy.SetProvenance(ctx, path, ProvenanceInput)`
(`usecase/provenance_set.go`, nil fields keep the record's value) is the
`provenance set` core shared by the CLI and the daemon: manual source, fresh
content hash and time, creates the file when missing (the opt-in) and stages
it. The daemon `Provenance` RPC answers per path the record, its `Status` and
`required` (a `require_provenance` rule), leaving out paths with neither;
`SetProvenance` runs the usecase under the shared slot like `Stage`.

Flow for asset references (reference in `docs/AI.md`, client-side only, warn
only): the pure `internal/assetrefs` (fs.FS based, no client imports)
`Build`s a graph from the working file list: Unity Force Text sources
(`.unity`, `.prefab`, `.asset`, `.mat`, ...; `IsSource`) are byte-scanned for
`guid: <32 hex>` (built-ins skipped, `ParseRefs`, fuzzed) and resolved per
Unity project (folder before `Assets`/`Packages`/`ProjectSettings`) through
the `.meta` files whose asset exists (folders count); Godot `.tscn`/`.tres`
`ext_resource`s resolve `uid://` first (`.uid`/`.import` via
`godotmeta.AssetOf`, resource headers) then `res://` below the nearest
`project.godot`. Caps: 5000 sources (max 20000), 8 MiB per file, either sets
`Truncated`; `Options.Outside` (sparse prefixes) turns unresolvable refs into
`Unknown`. `ComputeImpact` reads the old side of changed sidecars/sources
through an `assetdiff.Loader` (unreadable = truncated) and reports `Broken`
(removed/renamed/`guid_changed`/`uid_changed` targets, or new unresolved
refs of changed sources; refs already unresolved before stay quiet; Unity
GUID ids are keyed per project) and `NewlyUnused` (lost last referrer;
scenes, scripts, `Resources`/`StreamingAssets`/`Editor`/`Plugins`/`addons`
excluded; empty when truncated or `Graph.Partial` (sparse)). `assetscan.Graph`/`Refs`/`Impact`
(`refs.go`) list files through `usecase.Diff.WorkingFiles` (walk + ignores +
sparse) and skip the scan unless `Relevant` (an engine asset or sidecar
changed). Surfaces: MCP read tools `nipa_asset_refs` / `nipa_asset_impact`
and `nipa push --dry-run` (`assetscan.PushReport`: `asset_impact` in JSON,
warning block in text, a failure only warns on stderr; `planReport` in
`cli/dryrun.go`) plus the daemon `PushPlan`. The new side is the working copy, so unstaged edits are
included. The daemon `AssetRefs(root, path)` answers `Uses`/`UsedBy`/
`Unknown` from a graph cached per root (`repo.cachedGraph`, built once by
`RepoOps.Refs` = `NewGraphBuilder` over its own `Diff`, concurrent callers
wait for that build); the change hub's `refsGeneration` only moves on empty
notices and paths that are `assetrefs.Relevant` or `.nipaignore`, and a
2-minute TTL covers missed watcher events. Paths that are not `Relevant`
answer `asset: false` without a scan.

