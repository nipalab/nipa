# syntax=docker/dockerfile:1

ARG GO_VERSION=1.27
ARG NODE_VERSION=24

FROM --platform=$BUILDPLATFORM node:${NODE_VERSION}-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/server/dist ./web/server/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=""
ARG REVISION=""
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X 'github.com/nipalab/nipa/internal/version.Version=${VERSION}' -X 'github.com/nipalab/nipa/internal/version.Revision=${REVISION}'" \
      -o /out/nipad ./cmd/nipad
RUN mkdir -p /out/data /out/app && \
    sed -e 's|^DATABASE_DSN:.*|DATABASE_DSN: /data/nipa.db|' \
        -e 's|^CHUNK_STORAGE_DIR:.*|CHUNK_STORAGE_DIR: /data/chunks|' \
        -e 's|^LOG_LEVEL:.*|LOG_LEVEL: info|' \
        -e 's|^LOG_FORMAT:.*|LOG_FORMAT: json|' \
        config.yaml.sample > /out/app/config.yaml && \
    grep -q '^DATABASE_DSN: /data/nipa.db$' /out/app/config.yaml && \
    grep -q '^CHUNK_STORAGE_DIR: /data/chunks$' /out/app/config.yaml

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=""
ARG REVISION=""
LABEL org.opencontainers.image.title="nipad" \
      org.opencontainers.image.description="Nipa version control server (free edition: SQLite + local chunk storage)" \
      org.opencontainers.image.source="https://github.com/nipalab/nipa" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}"
COPY --from=build /out/nipad /usr/local/bin/nipad
COPY --from=build /out/app/config.yaml /app/config.yaml
COPY --from=build --chown=65532:65532 /out/data /data
WORKDIR /app
VOLUME /data
EXPOSE 6745
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/nipad"]
