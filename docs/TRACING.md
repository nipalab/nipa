# Nipa Tracing

Distributed tracing for the push/clone data path using OpenTelemetry. One
trace follows an operation from the `nipa` CLI through gRPC, the presigned
chunk HTTP transfers and the server-side chunk store (local filesystem or S3),
so failures can be pinpointed to an exact chunk and hop.

Implemented by `internal/obs/tracing.go`, wired through `internal/serverapp`
and both server mains. Tracing is opt-in and a no-op when disabled: the
process stays on the OpenTelemetry no-op provider and adds no exporter, no
network I/O and no span recording.

## Configuration

### Server (`nipad` / `nipad-ee`)

Environment variables (or `config.yaml` keys with the same names):

| Variable | Default | Meaning |
|---|---|---|
| `TRACING_ENABLED` | `false` | Export spans over OTLP |
| `TRACING_OTLP_ENDPOINT` | `OTEL_*` env, else `localhost:4317` | OTLP gRPC collector endpoint (`host:port` or URL; `https://` uses TLS, bare/`http://` is plaintext) |
| `TRACING_STDOUT` | `false` | Pretty-print spans to stderr instead of exporting (debugging) |
| `TRACING_SAMPLE_RATIO` | `1.0` | Root span sampling ratio in `(0, 1]` |
| `TRACING_SERVICE_NAME` | `nipad` / `nipad-ee` | `service.name` resource attribute |

When `TRACING_OTLP_ENDPOINT` is unset, the standard
`OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`
variables are honored.

### Client (`nipa`, including `nipa serve`)

Environment only, off by default:

| Variable | Meaning |
|---|---|
| `NIPA_TRACING` | `1`/`true`/`on` enables OTLP export; `stdout` pretty-prints to stderr |
| `NIPA_TRACING_OTLP_ENDPOINT` | OTLP gRPC collector endpoint (default `localhost:4317`) |
| `NIPA_TRACING_SAMPLE_RATIO` | Sampling ratio in `(0, 1]` (default 1.0) |

## Quickstart

```sh
# collector + UI
docker run --rm -p 16686:16686 -p 4317:4317 jaegertracing/all-in-one:latest

# server
TRACING_ENABLED=true ./bin/nipad

# client
NIPA_TRACING=1 nipa push -m "traced push"
# open http://localhost:16686 and search service "nipa" or "nipad"
```

For a quick look without a collector use `NIPA_TRACING=stdout nipa push` or
`TRACING_ENABLED=true TRACING_STDOUT=true ./bin/nipad`.

## Span inventory

Client side:

```
nipa.push                          org/project/branch; commit id+hash on success
├── push.upload                    files, upload_bytes
│   ├── nipa.NipaService/GetChunkUploadUrls   (gRPC, automatic)
│   ├── chunk.upload.put | chunk.upload.post  per chunk: nipa.chunk.hash, size,
│   │                                          http.response.status_code
│   └── nipa.NipaService/ConfirmChunkUploads  (gRPC, automatic)
└── push.commit                    branch, files, removed
    └── nipa.NipaService/Push      (gRPC, automatic)

nipa.clone / nipa.update / nipa.switch
└── nipa.sync                      org/project
    └── chunk.download.get         per chunk: nipa.chunk.hash, size
        └── nipa.NipaService/GetChunkDownloadUrls (gRPC, automatic)
```

Server side (child of the client span when the client propagates):

```
nipa.NipaService/Push               (gRPC server span, automatic)
└── push.apply                      project/branch, files, removed, resulting commit

nipa.NipaService/ConfirmChunkUploads
└── chunk.confirm                   chunk count, confirmed bytes, missing count
    ├── chunk.verify                per direct-uploaded chunk: hash, size
    │   └── storage.s3.get_object | storage.local.get
    └── storage.s3.stat_object | storage.local.size

"PUT /api/chunks/..."               (HTTP server span, automatic)
└── chunk.store                     nipa.chunk.hash, size
    └── storage.local.put           (local backend)

nipa.NipaService/GetChunkUploadUrls
└── chunk.presign_upload            chunk count
    └── storage.s3.presign_upload | chunkurl signing (local)
```

Every storage span carries `nipa.storage.backend` (`local` / `s3`) and the S3
spans the bucket; chunk spans carry the BLAKE3 hash, so a failed upload can be
matched between client and server by hash.

Direct (S3) transfers skip the server for the bytes: the client PUT/POSTs
straight to the object store with the W3C `traceparent` header attached, and
the server re-verifies the content at confirm time (`chunk.verify`), which is
where a corrupted direct upload shows up.

## Notes

- HTTP span names use `METHOD /path`, so each chunk transfer is visible
  individually (`PUT /api/chunks/<org>/<project>/<hash>`).
- `/healthz`, `/readyz`, `/version`, `/metrics` and pprof are never traced.
- Chunk-heavy pushes emit one span per chunk; use `*_SAMPLE_RATIO` (or a
  collector-side sampler) when the volume becomes noisy.
- Spans are flushed on server shutdown (SIGTERM/SIGINT) and when the CLI
  exits; `nipa serve` flushes when the daemon stops.
