# Review comments on image regions and audio ranges

Deferred by the owner: do not start it without a decision. It needs a server
schema and API change on both editions. Left over from the desktop features
plan (phases 1 to 14 shipped, see `docs/DESKTOP.md`).

**Why.** MR threads anchor to text lines only, so feedback on art and audio
lands in a top-level comment with a description of where to look.

**Server**
1. `merge_request_threads` gains a nullable `anchor` (JSON TEXT / JSONB):
   `{"kind":"region","x","y","w","h"}` normalized 0..1, or
   `{"kind":"range","start_ms","end_ms"}`; requires `file_path`, excludes
   `old_line`/`new_line`. Validation in `merge_request_review.go`: the path
   must be in the current diff; a thread is outdated once the file's new-side
   hash differs from the stored `anchor_hash`.
2. REST model, gRPC `anchor` field on the thread and comment messages, client
   `grpc` conversion, MCP `nipa_mr_comment` passthrough.
3. Web SPA: such threads render under their file as file-level comments with
   the region/range described in text (no viewer on the web yet).

**Desktop**
1. Image viewer in MR context: drag a rectangle to comment; existing threads
   appear as numbered pins, clicking opens the `ThreadCard`.
2. Audio viewer: drag a range on the waveform to comment; ranges render as
   markers, clicking one seeks and plays the range.
3. Outdated anchors are listed but dimmed.

New UI text goes into every desktop catalog (`frontend/src/i18n/locales`).

**Tests.** Usecase validation and outdated logic, both repositories, REST and
gRPC handlers, e2e spec, viewer interactions.
