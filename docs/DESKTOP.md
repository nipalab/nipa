# Nipa Desktop

Design for the cross-platform desktop client (macOS, Windows, Linux) aimed at
game teams: artists, designers, audio, programmers and build engineers working
on binary-heavy projects.

## Decisions

- **Framework: Wails (Go) + React/TypeScript.** Visual asset diffs are the core
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
  depth and alpha. `MaxPixels` (8192²) bounds every decode before allocation.
- `Downscale(img, maxSide)` fits a preview box with bilinear scaling.
- `CompareImages(old, new, threshold)` returns changed pixel count/ratio, max
  channel delta, the changed bounding box and size/format/depth/alpha changes;
  fully transparent pixels compare equal.
- Decoders are fuzzed (`FuzzDecodeTGA`, `FuzzDecodeImage`); crashers found by
  the fuzzer are kept as seeds in `testdata/fuzz`.

## Roadmap (one PR each)

1. `internal/preview`: classification, common images, TGA, compare. **(done)**
2. Daemon `DiffFiles`: structured diff stream (paths, status, kind, hunks). **(done)**
3. Daemon `ReadBlob` + `Preview` for images. **(done)**
4. `desktop/` Wails scaffold + daemon connector.
5. Changes screen + text diff.
6. Image compare viewer.
7. DDS/KTX2/EXR/HDR/PSD decoders + viewer controls.
8. 3D model viewer.
9. Audio viewer, CSV table diff, Unity `.meta` lint.
10. Locks (lock-aware status, Lock & edit, Locks page).
11. Sign in, clone, sparse checkout.
12. History, branches, tags, revert.
13. Merge requests with asset viewers.
14. Conflict resolution.
15. Tray, notifications, deep links, settings.
16. Packaging and auto-update.
