# Nipa

A next-generation centralized Version Control System engineered for binary-heavy projects (e.g., game development, 3D assets, rich media) and large team monorepos.

`nipa` combines the strengths of Git (lightweight branching, Merge Requests, client 3-way text merges), Perforce (exclusive file locking, high-throughput binary streaming), and SVN (fine-grained path-based access control, centralized single source of truth) to provide scalable asset tracking with developer autonomy.

- **Language/Tech Stack:** Go (Golang), gRPC, Protobuf, SQLite (`modernc.org/sqlite`), FastCDC, BLAKE3
- **Architecture Model:** Client-Server Monorepo (`nipa` / `nipad`)

## Quick Start

```sh
go build ./...
# run the server (see config.yaml.sample for settings)
./bin/nipad

# clone a repository (prompts for credentials on first use)
nipa clone http://localhost:6745/org/project ./work
cd work

# edit files, then stage and commit them on the server
nipa add path/to/file
nipa status
nipa push -m "my change message"
```

The working copy tracks the branch configured at clone time. Files are split
into content-addressed chunks (FastCDC) and rehydrated from a local chunk cache,
so updates and branch switches only transfer what actually changed.

## Commands

Run `nipa <command> --help` for full details.

| Command                     | Description                                                                 |
| --------------------------- | --------------------------------------------------------------------------- |
| `nipa clone <url> <target>` | Clone a repository. `-b <name>` selects the starting branch (default `main`). |
| `nipa branch`               | Show the current branch. `-a` lists all server branches, `-c <name>` creates a new branch and switches to it. |
| `nipa switch <branch>`      | Fetch the given branch, materialize its tree in the working copy, and repoint the local repository at it. Not allowed while changes are staged. |
| `nipa add <path> [...]`     | Mark files (or everything inside directories) for the next push.            |
| `nipa remove <path> [...]`  | Unmark files for the next push. `-a` unmarks everything.                    |
| `nipa status`               | Show the working copy status: `A` staged, `M` modified, `?` untracked, `!` missing. |
| `nipa push -m "<message>"`  | Upload staged changes to the server and commit them on the configured branch. |
| `nipa update`               | Fetch and apply the latest changes of the configured branch.                |

Branch creation (`nipa branch -c <name>`) forks from the exact commit the
working copy is pinned to (a push records the server's commit id and hash
locally), falling back to the current branch head when there is nothing pinned
yet.

## Architecture

- **Server** — `internal/`: `domain` (entities/errors), `usecase` (business
  logic), `repository` (SQLite/Postgres over sqlc), `grpc` (protobuf service +
  handlers), `http` (REST API).
- **Client** — `internal/client/`: `cli` (cobra commands), `usecase`
  (clone/branch/push/update orchestration), `grpc` (transport), `localrepo`
  (`.nipa/` local metadata + SQLite), `securestorage` (keyring-backed token
  store).
- Local state lives in `.nipa/` inside the clone target: a JSON `config` with
  the repository URL and current branch, and a SQLite database tracking the tree
  snapshot, the content chunk cache (never erased, so updates can skip unchanged
  content) and the staged file list.
- Content is stored server-side as FastCDC chunks addressed by BLAKE3 hash;
  the tree is a recursive manifest of directories, files and their chunk lists.

## License

This project is dual-licensed:

- Community open source license: Apache License 2.0. The main project code is covered by the current [LICENSE](https://github.com/nipalab/nipa/blob/main/LICENSE).
- Commercial enterprise license: all files and folders under the `ee` directory are licensed separately under the enterprise license in [ee/LICENSE](https://github.com/nipalab/nipa/blob/main/ee/LICENSE).

The Apache 2.0 license applies to the community OSS edition. The commercial enterprise license applies only to code under the `ee` directory.