# Desktop app: large asset performance

Status: finding 1 addressed (bounded line diff); the other findings are
still open. Written 2026-11-28 on branch `update-by-delta`.

This lists where the desktop app (and the `nipa serve` daemon behind it) gets
slow, freezes or runs out of memory when a diff or preview involves a big
file: large images, 3D models, audio, video and large text assets.

## How this was measured

- Code was read end to end: `desktop/internal/service`, the viewers under
  `desktop/frontend/src/components/changes`, the daemon handlers in
  `internal/client/daemon` and the diff/blob usecases in
  `internal/client/usecase`.
- Timings come from throwaway Go tests (since deleted) run on one Linux
  machine with a warm disk cache. Treat them as orders of magnitude.
- The app itself was not run. Nothing on the webview side (JSON parsing,
  base64 decoding, three.js loading) was measured; those findings come from
  reading the code.

| Measurement | Result |
|---|---|
| Line diff, 50k lines, 100 changed | 166 ms, 315 MB allocated |
| Line diff, 50k lines, 300 changed | 369 ms, 927 MB allocated |
| Line diff, 100k lines, 300 changed | 549 ms, 1851 MB allocated |
| Decode 4096² PNG (13 MB file) | 0.37 s, 128 MB |
| Decode 8192² PNG (54 MB file) | 1.5 s, 512 MB |
| Downscale 4096² to 128 / to 2048 | 0.17 s / 0.48 s (272 MB) |
| Downscale 8192² to 128 / to 2048 | 0.66 s / 1.2 s (528 MB) |
| Full-resolution pixel compare, 4096² / 8192² | 0.2 s / 0.9 s |
| Base64 of 64 MB, then `json.Marshal` | 0.13 s + 0.18 s, 170 MB each |
| `DecodeAudio`, 200 MB WAV | 1.8 s, 288 MB |
| `hashWorkingFile`, 1 GB binary | 1.4 s |

## Findings, most serious first

### 1. Large text assets can exhaust the daemon's memory

`LinesWith` in `internal/diff/lines.go:43-66` is a Myers diff that appends a
full copy of its `v` array (`2*(n+m)+1` ints) to `trace` on every edit step.
Memory and time grow with (lines in both files) × (changed lines), and all of
it stays live until the diff ends.

- The table above shows the scaling: doubling the lines doubles the memory,
  tripling the changed lines triples it.
- A 500k-line file with a few thousand changed lines needs tens of GB. That
  is a realistic re-serialized Unity scene or prefab, `.obj`, `.gltf`, CSV,
  SVG or JSON.
- The 1 MB cap (`maxFileHunkBytes`, `internal/client/daemon/diff_files.go:70`)
  is checked after `diff.FileHunks` has already run, so it bounds the message
  size but not the work.
- `DiffFiles` computes hunks for every non-binary file it returns, so one
  such file in a commit or merge request listing (`CommitFiles`,
  `RevisionFiles`) is enough, even if nobody opens it.
- `internal/diff` is a shared package, so the server-side users of `Hunks`
  have the same exposure. Not checked in detail.

Fix direction: trim the common prefix and suffix before diffing, switch to
the linear-space Myers variant (or a patience/histogram diff), and add a
backstop: above a line or byte budget, skip hunks and report
`hunks_omitted` without computing them. Asset kinds with their own viewer
(scene, table, model) could skip text hunks entirely unless asked.

**Addressed.** `internal/diff` now trims the common prefix and suffix and
runs linear-space Myers (middle snake, divide and conquer): memory is
O(lines), e.g. 100k lines with 300 changed lines take 14 ms and 5 MB instead
of 309 ms and 1.9 GB, and 500k lines take 35 ms and 22 MB. `LinesBudget`,
`HunksBudget` and `FileHunksBudget` add a budget (`diff.DefaultBudget`:
200k lines or 16 MiB per side, 5000 changed lines). A file over it is not
diffed: it reports `hunks_omitted` plus estimated counts flagged
`counts_approximate` (lines left after trimming, an upper bound). The
daemon's `DiffFiles`, the server's merge request and commit diffs (gRPC
`too_large`, REST `too_large`) and review anchoring all use it. The desktop
app and the web UI show `~` before estimated counts, and the web UI shows a
"Diff too large to display" banner. The per-kind skip of text hunks was
not done.

### 2. Abandoned previews are never cancelled, and they queue

- `serialDiff` (`internal/client/daemon/diff.go:44`) holds one mutex per
  repository across the whole `ReadBlob` callback. For `Preview` that
  includes the read, the decode and both sides; for video it includes the
  full copy.
- The bindings return a cancellable promise (`Cancellable` in
  `desktop/frontend/src/api/desktop.ts:217`), but no viewer `queryFn` wires
  the react-query abort signal to it. Selecting another file leaves the old
  call running.
- Result: clicking through five big files runs five full decodes in order
  before the one on screen. Files-grid thumbnails share the same queue.

Fix direction: pass the query `signal` to `cancel()` in every viewer and
thumbnail query; make sure the daemon decode loops check `ctx`; narrow the
lock so it covers the localrepo handle use and not the decode, or give
blob reads their own handle.

**Addressed.** Every viewer, thumbnail and `fileDiff`/`conflictDiff` query
passes react-query's abort `signal` to the call's `cancel()` through
`withAbort` (`desktop/frontend/src/lib/abort.ts`), which cancels the Go
context and the daemon RPC. The desktop `copyBlob` stops on `ctx.Err()`,
including after a read that completed, so a cancelled model, audio, video,
scene, table or font load never decodes or reads its other side. The daemon
`Preview` now only reads bytes inside the `serialDiff` lock; decoding (full
images and embedded thumbnails), downscaling and the compare run after it,
with `ctx` checked before each decode, side and compare. Thumbnails still
share the lock for the read itself; their cache and concurrency limit are
finding 3.

### 3. Thumbnails decode the full image every time

`Thumbnail` (`desktop/frontend/src/components/files/FileTable.tsx:234`), the
history view (`HistoryView.tsx:305`) and the file detail
(`FileDetail.tsx:203`) call `imageDiff` with `maxSize: 128`. The daemon still
reads the whole file (up to 512 MB) and decodes it at full resolution before
downscaling.

- About 0.5 s per 4096² PNG and 2.2 s plus 512 MB per 8192².
- The only cache is react-query memory (10 minutes); nothing is kept on
  disk, so every session and every scroll back pays again.
- A grid of forty 4K textures is 20 s or more of serialized work that also
  delays diff views (finding 2).

Fix direction: a thumbnail cache on disk keyed by file hash and size
(working files: stat fingerprint), a small concurrency limit separate from
the diff lock, and decoders that can downscale while decoding where the
format allows (JPEG DCT scaling, mip levels for DDS/KTX2).

### 4. Image compare of very large textures is heavy

`Preview` (`internal/client/daemon/blob.go:104`) reads each side whole
(`maxPreviewInputBytes` is 512 MB), decodes both at full resolution
(`preview.MaxPixels` is 8192²), keeps both alive, downscales each to 2048
and runs the pixel compare at full resolution.

- An 8192² pair takes about 7 s with well over 1 GB allocated.
- Changing mip level or cube face (`ImageDiff.tsx`, query key includes
  `level` and `face`) calls `Preview` again, which re-reads and re-decodes
  both sides.
- HDR images (EXR) use float pixels, so their footprint is several times
  higher. Not measured.

Fix direction: cache the decoded or downscaled result per (hash, level,
face) for the open viewer, release the full-resolution pixels as soon as the
stats and preview are produced, and decode the two sides in parallel.

### 5. Models and audio cross the Wails bridge as base64 inside JSON

`loadModel` (`desktop/internal/service/models.go:155`) base64-encodes the
model (up to `maxRenderBytes`, 64 MB per side) and every referenced file into
the `ModelDiff` result. `loadAudio` does the same for the playback WAV (up to
`playbackBudget`, 48 MB per side).

- Go side: about 0.3 s and 340 MB of allocation per 64 MB, on top of the
  buffer growth while reading the blob.
- Webview side, all on the UI thread: `JSON.parse` of the response,
  `base64ToBytes` (`desktop/frontend/src/lib/utils.ts:28`, `atob` plus a
  per-byte loop), then the three.js loader. The window freezes for the
  duration.
- The result sits in the react-query cache for the default 5 minutes:
  roughly 170 MB of strings per visited model, so browsing several models
  adds up quickly.
- Models over 64 MB are not rendered at all (`TooLarge`), though up to
  256 MB is still read into memory to compute stats.

Fix direction: serve model, dependency and audio bytes through the existing
media route (`MediaMiddleware`, `desktop/internal/service/media.go`) as video
already does, and have the viewers `fetch` them as `ArrayBuffer`. Move model
parsing to a worker where the loader allows. Set a short `gcTime` on these
queries.

### 6. Video is fully copied before the first frame

`loadVideo` (`desktop/internal/service/videos.go:79`) copies each side to a
temp file, up to 4 GB each, one after the other, before returning. That
includes the working-copy side, which is already a plain file on disk. The
copy holds the repository lock from finding 2.

Fix direction: serve the working file directly from the repository through
the media route, copy only the stored side, copy both sides in parallel, and
return stats early so the UI can show progress.

### 7. Each click on a modified file re-walks the tree and re-hashes it

`FileDiff` calls `DiffFiles` with one path, but `scanWorking`
(`internal/client/usecase/diff.go:472-534`) still walks the entire working
tree and filters afterwards. For the selected file, the stat cache is only
trusted when the cached hash equals the old hash (`diff.go:519`), so a
modified file is re-chunked and re-hashed on every call: about 1.4 s per GB.
Status already reuses the cached hash for modified files
(`internal/client/usecase/stage.go:438`).

Also in this path: `attachLoader` reads a modified text file whole with no
size limit before it reaches finding 1.

Fix direction: scope the walk to the requested paths (as #298 did for add,
push and prune), and reuse the stat-cache hash for modified binaries, where
the diff only needs size, hash and the binary flag.

## Already in good shape

- Text diff rows, table rows and file lists are virtualized.
- Tables are capped at 16 MB (`maxTableBytes`), fonts at 32 MB.
- Image previews sent to the webview are downscaled to 2048 px.
- Audio decoding is proportionate: 200 MB WAV in 1.8 s and 288 MB.
- Stored blobs are streamed chunk by chunk (`blobReader`), not loaded whole,
  until a viewer buffers them.

## Suggested order

1. Bound the line diff (finding 1). It is the only one that can take the
   daemon down, and it also protects commit and merge request listings.
2. Cancel abandoned previews and narrow the lock (finding 2). Small change,
   and it makes every other slow path less painful.
3. Thumbnail cache on disk (finding 3).
4. Move model and audio bytes to the media route (finding 5), then serve
   working videos directly (finding 6).
5. Scope the working diff and reuse cached hashes (finding 7).
6. Image compare caching and memory (finding 4).

## Open questions

- Webview costs in finding 5 are unmeasured. Profile a 50 MB `.glb` in the
  running app before and after.
- Cold-disk and Windows numbers will be worse than the table; hashing and
  video copy are the most sensitive.
- Whether the server-side callers of `internal/diff` need the same guard as
  finding 1.
