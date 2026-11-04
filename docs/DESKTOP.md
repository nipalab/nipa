# Nipa Desktop

Design for the cross-platform desktop client (macOS, Windows, Linux) aimed at
game teams: artists, designers, audio, programmers and build engineers working
on binary-heavy projects.

## Decisions

- **Framework: Wails v3 (Go) + React/TypeScript.** Visual asset diffs are the core
  feature, and the web platform already provides what they need: WebGL2 for
  image compare modes and float textures, three.js for glTF/OBJ/FBX/USDZ,
  WebAudio for waveforms and playback. The Go side talks to `nipa serve`
  (`docs/DAEMON.md`) with the generated `daemonpb` client and reuses Go
  decoders. GPUI (Rust) was rejected: every viewer would be written from
  scratch on a pre-1.0 API in a second language. Tauri was rejected because it
  adds Rust while the reusable code is Go.
- **Decode in Go, render in the webview.** Formats the webview cannot open
  (TGA, DDS, KTX2, EXR, PSD, OGG/FLAC on WebKit) are decoded by
  `internal/preview` into normalized data (RGBA, float16, audio peaks), so the
  UI never depends on the webview's codec set.
- **Layout: `desktop/` in this repo as a nested Go module**
  (`github.com/nipalab/nipa/desktop`, `replace` to `../`). It can import
  `internal/...`, proto + daemon + UI changes land together, and Wails' cgo /
  WebKitGTK dependencies stay out of `go test ./...` and the server builds.
- **UI stack:** React 18, Vite, Tailwind + shadcn/ui, lucide icons, TanStack
  Query/Virtual, CodeMirror 6, three.js.

## The app (`desktop/`)

Wails v3.1.0 project, nested Go module `github.com/nipalab/nipa/desktop`
(`replace github.com/nipalab/nipa => ../`), so root `go test ./...`, vet and
Sonar never compile it.

- `main.go` wires one bound service and the window (binary `nipa-desktop`;
  it must not be called `nipa`, the app looks for the CLI next to itself).
- `internal/connector`: reads `daemon.json` (`daemon.ReadEndpoint`), checks the
  pid, dials loopback gRPC with the token as per-RPC credentials and `Ping`s.
  When no live daemon answers it starts `nipa serve` detached (own session /
  process group, output in the user cache dir `nipa/daemon.log`) and polls
  until it answers (10 s). The CLI is resolved from `NIPA_BIN`, next to the
  app binary, then `PATH`. The daemon outlives the app on purpose: Explorer
  and engine plugins share it.
- `internal/workspace`: the opened clones in `<config dir>/nipa/desktop.json`
  (atomic write, 0600). The daemon only keeps watched roots in memory, so the
  app re-watches them on start; a clone that fails to watch stays listed with
  its error.
- `internal/service`: the Wails-bound API (`Connect`, `Repos`, `PickRepo`,
  `AddRepo`, `RemoveRepo`; Changes screen: `Changes`, `Stage`, `Unstage`,
  `Discard`, `FileDiff`, `ImageDiff`, `Push`, `Update`). Plain Go behind `Dialer`/
  `RepoStore`/`FolderPicker` seams, so it is unit-tested without a webview; an
  `Unavailable` error drops the session so the next call reconnects (and
  restarts the daemon) and re-watches each clone on first use. Every watched
  clone gets a follower on the daemon's `WatchChanges` stream, relayed as the
  `repo:changed` event; `Push`/`Update` relay `OpEvent`s as `op:progress` and
  stop the daemon operation when the frontend cancels the call. Events are
  registered in `main.go`, so the bindings carry their types.
- Changes screen (`frontend/src/components/changes`): staged and unstaged
  groups in a virtualized list (checkbox = stage, discard with confirm,
  arrow-key navigation), push message + Push with progress/cancel, Update in
  the header. `FileDiff` turns one `DiffFiles` entry into the viewer model
  (line kinds `added`/`removed`/`context`); an untracked text file is read
  with `ReadBlob` and shown as a full addition. `DiffViewer.viewerFor` is the
  viewer registry: text diffs (unified/split, virtualized, per-line
  highlight.js), notices (too large, mode-only, empty), the image viewer and
  a metadata card for other binaries. The list refreshes on `repo:changed`,
  window focus and a 30 s poll.
- Image viewer (`ImageDiff.tsx`, `src/lib/imageCompare.ts`): `ImageDiff`
  calls the daemon's `Preview` (snapshot before, working file after; previews
  capped at 2048 px) and returns the PNGs as data URLs plus the pixel-diff
  stats; a 415 from the daemon (format without a decoder yet, too large)
  becomes `unsupported` and the metadata card. The frontend decodes the PNGs
  through a canvas and renders side by side, swipe, onion skin, a difference
  heatmap (client-side with a tolerance slider, changed region from the
  daemon's stats) and blink, with R/G/B/A channel isolation, synced zoom/pan
  (wheel, drag, 1:1, fit; fit enlarges small sprites up to 800% with
  pixelated rendering) and a before/after metadata table. Canvas 2D is enough
  at the preview size, so there is no WebGL path; the mode is remembered in
  `nipa.imageMode`.
- Texture and layer controls: when either side has mips, faces or float
  data a second toolbar offers a mip picker (sizes from the level-0
  dimensions), a face picker (`+X…−Z`, `Layer n` for arrays) and, for HDR
  sides, exposure (±8 EV) and a tone curve (clamp, Reinhard, ACES) applied
  client-side by `src/lib/hdr.ts` after unpacking the RGBE preview. Picking a
  surface refetches `ImageDiff` with `ImageOptions{Level, Face}` while the
  previous preview stays on screen; the choice resets per file. PSD/PSB
  sides list their layers (`src/lib/layers.ts` pairs them by kind, name and
  duplicate index) with added/removed/changed status and what changed
  (pixels, bounds, visibility, opacity, blend mode, nesting).
- Model viewer (`ModelDiff.tsx`, lazily loaded with three.js 0.186):
  `ModelDiff` in the service reads each side through `ReadBlob` (snapshot
  before, working file after, or a revision) plus the files it references —
  glTF buffers and images, OBJ `mtllib` libraries and their `map_*` textures —
  resolved next to the model and refused when they leave the repository;
  absent files are listed as `missing`. Stats come from
  `preview.DecodeModel`; the model is read up to 256 MiB and returned
  base64 only while it and its files fit 64 MiB (`tooLarge` otherwise, stats
  still shown). `src/lib/modelLoad.ts` parses glTF/GLB, OBJ+MTL, FBX and
  USD/USDA/USDC/USDZ from memory (a `LoadingManager` URL modifier serves the
  referenced files as data URLs) and counts the scene itself for formats
  without Go stats. `src/lib/modelStage.ts` drives one WebGL renderer with a
  shared camera and `OrbitControls`: side by side (two scissored viewports)
  or overlay (the old model as an orange wireframe on top of the new one),
  shaded/wireframe/both, synced animation playback by clip name, camera fit
  to the union of both bounds. The panel diffs vertices, triangles, meshes,
  materials, textures, bones, animations, morph targets, bounds and the
  exporter, and lists added/removed material and animation names.
- `frontend/`: React 18, Vite 8, Tailwind 4 (tokens in `src/index.css`,
  light/dark/system theme in `src/lib/theme.tsx`), TanStack Query, lucide.
  Generated bindings live in `frontend/bindings` and are committed (CI fails
  when they are stale). `src/api/desktop.ts` is the only module importing them,
  which keeps tests mockable.
- Linux uses GTK4/WebKitGTK 6.0; the Taskfile falls back to `-tags gtk3` when
  `pkg-config webkitgtk-6.0` is missing (`wails3 build EXTRA_TAGS=...`
  overrides).

Commands: `make desktop` (app + bundled `nipa` in `desktop/bin`),
`make desktop-dev` (hot reload, `NIPA_BIN` = `bin/nipa`), `make desktop-test`.
They need the `wails3` CLI (`go install
github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`, plus `-tags gtk3` on hosts
with WebKitGTK 4.1 only). CI: `.github/workflows/desktop.yml` (tests, then a
Linux/macOS/Windows build matrix with a bindings freshness check).

## Asset diff coverage

| Tier | Formats | Viewer |
|---|---|---|
| MVP | PNG JPEG GIF WebP BMP TIFF TGA | image: side-by-side, swipe, onion skin, pixel heatmap, blink, channels, synced zoom |
| MVP | DDS KTX2 EXR HDR PSD/PSB | image + mip/face picker, exposure, PSD layer list |
| MVP | text, code, Unity/Godot YAML, JSON, shaders | text diff (`internal/diff` hunks) |
| v1 | glTF/GLB OBJ FBX USDZ | 3D: synced orbit cameras, wireframe, stats diff |
| v1 | WAV OGG FLAC MP3 | waveform + spectrogram, synced A/B playback |
| v1 | CSV/TSV | cell-level table diff |
| v1 | Unity `.meta` | lint: missing/orphan sidecars, GUID change |
| v2 | video, fonts, `.blend`/`.uasset` thumbnails | playback, glyph sheet, embedded thumbnail |
| always | anything else | size/hash stats + external diff tool |

## `internal/preview`

Pure package (no client imports), shared by the daemon and later the server.

- `Classify(path, isBinary) Kind` picks the viewer kind
  (`image/model/audio/video/font/table/text/opaque`).
- `DecodeImage(name, data)` decodes PNG/JPEG/GIF/BMP/TIFF/WebP (stdlib +
  `golang.org/x/image`) and TGA (own decoder: true-color, gray, color-mapped,
  RLE, 15/16/24/32 bpp, any origin) into `*image.NRGBA`, with format, bit
  depth and alpha. `MaxPixels` (8192²) bounds every decode before allocation;
  float sources get half of it (`checkFloatSize`, 16 bytes per pixel).
- `DecodeImageAt(name, data, Select{Level, Face})` adds the game formats,
  chosen by extension, and picks one surface of a texture (out-of-range values
  clamp; `Image.Levels/Faces/Cube/Level/Face` describe the texture):
  - DDS: legacy FourCC/D3DFMT/bit-mask headers and DX10 (DXGI) headers,
    mipmaps, cube maps and arrays; BC1–BC7 incl. BC6H (UF16/SF16), 8-bit
    RGBA/BGRA/R/RG/A, R10G10B10A2, RGBA16, half/float formats, R11G11B10 and
    RGB9E5. Volume textures are refused.
  - KTX2: the same pixel formats by `vkFormat`, levels/faces/layers, Zstandard
    or zlib supercompression. Basis Universal (BasisLZ/UASTC) is refused.
  - EXR: single-part scanline and tiled (level 0) images, NONE/RLE/ZIPS/ZIP/
    PXR24, half/float/uint channels; RGBA of the unnamed layer, else the first
    named layer with colour, Y as gray, or the first channel as gray. PIZ,
    B44, DWA, multi-part and deep files are refused.
  - Radiance HDR: adaptive RLE, old runs and flat scanlines, `-Y`/`+Y`.
  - PSD/PSB: the merged image (raw or PackBits; 1/8/16/32-bit; RGB, gray,
    indexed, CMYK, bitmap, duotone, multichannel; merged transparency when
    the layer count is negative) plus `Layers` top-most first with group
    nesting, visibility, opacity, blend key, bounds and an FNV hash of each
    layer's channel data (also read from `Lr16`/`Lr32`).
  Block decoders are checked bit-for-bit against bcdec goldens
  (`testdata/bcn`, generators in `testdata/gen`). Refusals wrap
  `ErrUnsupported` with a reason ("EXR PIZ compression") that the daemon
  forwards in its 415.
- `DecodeModel(name, data)` reads `ModelStats` from glTF/GLB (JSON only:
  accessor counts, triangles by primitive mode, materials, textures, skins and
  unique joints, animations, morph targets, world-space bounds from the
  POSITION min/max boxes through the node transforms of the default scene,
  external buffer/image URIs) and OBJ (vertices, fan-triangulated faces,
  objects/groups, `usemtl` names, `mtllib` files, bounds);
  `MaterialLibraryDependencies` lists an `.mtl` file's textures. Other model
  formats return `ErrUnsupported`.
- Float sources keep linear samples in `Image.HDR` (`FloatImage`) while
  `Pixels` is their clamped sRGB rendering, used for `CompareImages`.
  `DownscaleFloat` box-filters them and `PackHDR` packs them into an opaque
  8-bit PNG twice as wide (RGBE mantissas left, exponent + alpha right) so a
  browser canvas does not premultiply them away.
- `Downscale(img, maxSide)` fits a preview box with bilinear scaling.
- `CompareImages(old, new, threshold)` returns changed pixel count/ratio, max
  channel delta, the changed bounding box and size/format/depth/alpha changes;
  fully transparent pixels compare equal.
- Decoders are fuzzed (`FuzzDecodeTGA`, `FuzzDecodeImage`, `FuzzDecodeDDS`,
  `FuzzDecodeKTX2`, `FuzzDecodeEXR`, `FuzzDecodeHDR`, `FuzzDecodePSD`,
  `FuzzDecodeModel`);
  crashers found by the fuzzer are kept as seeds in `testdata/fuzz`.

## Roadmap (one PR each)

1. `internal/preview`: classification, common images, TGA, compare. **(done)**
2. Daemon `DiffFiles`: structured diff stream (paths, status, kind, hunks). **(done)**
3. Daemon `ReadBlob` + `Preview` for images. **(done)**
4. `desktop/` Wails scaffold + daemon connector. **(done)**
5. Changes screen + text diff, daemon `WatchChanges`. **(done)**
6. Image compare viewer. **(done)**
7. DDS/KTX2/EXR/HDR/PSD decoders + viewer controls. **(done)**
8. 3D model viewer. **(done)**
9. Audio viewer, CSV table diff, Unity `.meta` lint.
10. Locks (lock-aware status, Lock & edit, Locks page).
11. Sign in, clone, sparse checkout.
12. History, branches, tags, revert.
13. Merge requests with asset viewers.
14. Conflict resolution.
15. Tray, notifications, deep links, settings.
16. Packaging and auto-update.
