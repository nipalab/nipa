# Webhooks, service tokens and commit ancestry

Integrations such as issue trackers (BugBase) and CI connect to a project with
three pieces: webhooks for events, a project service token for REST calls, and
the commit ancestry endpoint to ask whether a release contains a fix.

## Webhooks

Project admins manage webhooks under **Settings → Webhooks** or
`/api/v1/orgs/{org}/projects/{project}/webhooks`. The signing secret is returned
once, on create and on rotate. Deliveries are queued, retried with backoff and
can be redelivered; consumers must be idempotent (a redelivery, or the overlap
of two server colors during a deploy, delivers the same payload again).

Destinations are checked against `WEBHOOK_EGRESS_ALLOWLIST`: without an entry,
only public addresses are allowed, so a receiver on `127.0.0.1` or a private
network needs to be listed.

### Request

`POST <url>` with `Content-Type: application/json`, `User-Agent: Nipa/1.0` and:

| Header | Value |
| --- | --- |
| `X-Nipa-Event` | event name, e.g. `push` |
| `X-Nipa-Delivery` | delivery id (base36); stays the same on redelivery, use it to deduplicate |
| `X-Nipa-Hook-Id` | webhook id |
| `X-Nipa-Signature` | `sha256=` + hex HMAC-SHA256 of the exact body, keyed with the webhook secret |

Verify the signature over the raw body with a constant-time comparison before
parsing it. Any 2xx response marks the delivery delivered; redirects are not
followed.

### Envelope

Every payload starts with:

```json
{
  "event": "push",
  "timestamp": "2026-11-08T12:00:00Z",
  "actor": {"id": "k3j2", "username": "alice"},
  "organization": {"slug": "acme"},
  "project": {"slug": "rocket", "default_branch": "main"},
  "webhook_id": "k9x1"
}
```

### Events used by issue trackers

`push` — one entry per push; a push is one commit, `after` is its id and
`message` its message. `created`/`deleted` mark branch creation and deletion.

```json
{"changes": [{"branch": "main", "branch_id": "b1", "created": false, "deleted": false,
  "forced": false, "before": "c1", "after": "c2", "message": "fix crash, fixes #1",
  "files": [{"path": "src/a.c", "op": "modified", "binary": false, "size_bytes": 120}]}]}
```

`tag.created` / `tag.deleted`:

```json
{"tag": {"id": "t1", "name": "v0.2", "commit_id": "c2", "message": "", "created_by": "u1"}}
```

`lock.release_requested` (someone asked the holder of a file lock to release
it; sent when a request is first made and again only after 24 hours, a repeat
inside that window just updates the message):

```json
{"lock": {"id": "l1", "path": "art/hero.psd", "branch": "", "held_by": "u2",
  "held_by_name": "bob", "requester_id": "u1", "requester_name": "alice",
  "message": "need it for the trailer"}}
```

`mr.created`, `mr.updated` (title, description, draft or target changed),
`mr.synchronized` (new commits on the source branch), `mr.merged`,
`mr.closed`, `mr.reopened`, `mr.ready_for_review` and the review/check events
share one shape. `state` is `open`, `merged` or `closed`; `merge_commit_id` is
set once merged. Merging does not emit a `push`.

```json
{"merge_request": {"id": "m1", "number": 3, "title": "Fix crash", "description": "Fixes #2",
  "state": "open", "source_branch": "fix", "target_branch": "main", "author_id": "u1",
  "merge_commit_id": "", "created_at": "...", "updated_at": "..."}}
```

The payload carries no commit list: fetch it with
`GET .../merge-requests/{number}/commits`, and the source head (needed to
report a check) with `GET .../commits?branch=<source_branch>&limit=1`.

`ping` is only sent by the **Send test** button / `POST .../webhooks/{id}/test`.

## Project service tokens

Project admins create tokens under **Settings → Service tokens** or
`POST /api/v1/orgs/{org}/projects/{project}/service-tokens`:

```json
{"name": "BugBase", "scopes": ["read", "checks:write", "webhooks:write"], "expires_at": "2027-01-01T00:00:00Z"}
```

The response carries the plaintext `token` (`nipa_st_...`) once; only its
SHA-256 is stored, plus a short `hint`. `GET .../service-tokens` lists tokens
with `last_used_at`, `expires_at` and `revoked_at`; `DELETE
.../service-tokens/{id}` revokes one. Creating and revoking are audited
(`service_token.created`, `service_token.revoked`).

Send it as `Authorization: Bearer nipa_st_...`. A token acts as the admin who
created it, but only on its own project and only on these routes (everything
else answers 403):

| Scope | Routes under `/api/v1/orgs/{org}/projects/{project}` |
| --- | --- |
| `read` | `GET` the project, `/branches`, `/commits`, `/commits/{commit}/diff`, `/commits/{commit}/contains`, `/tags`, `/tags/{name}`, `/merge-requests`, `/merge-requests/{id}`, `/merge-requests/{id}/commits`, `/merge-requests/{id}/checks` |
| `checks:write` | `POST /merge-requests/{id}/checks` |
| `webhooks:write` | `GET`/`POST /webhooks`, `GET`/`PATCH`/`DELETE /webhooks/{id}`, `POST /webhooks/{id}/test`, `GET /webhooks/{id}/deliveries`, `POST /webhooks/{id}/deliveries/{deliveryId}/redeliver` |

Unknown, revoked or expired tokens, and tokens whose creator was deleted or is
unverified, answer 401. A token stops working (403) as soon as its creator is
no longer a project admin. Checks reported with it name the creator as
reporter; unlike the creator's own session, a token may report checks on the
creator's merge requests even when the target requires them. Audit entries
written through a token carry its id (`service_token_id`), and each token has
its own per-user rate-limit bucket. A project has at most 50 active tokens.

## Commit ancestry

`GET /api/v1/orgs/{org}/projects/{project}/commits/{commit}/contains?ref=<ref>`
answers whether `commit` is the commit `ref` points at or one of its
ancestors. `ref` is resolved as a tag name, a branch name, a base36 commit id
or a commit hash, in that order. Read permission (or the `read` scope) is
required. The walk stops after 10000 commits and answers 400 rather than guess.

```json
{"commit": "c1", "ref": "v0.2", "ref_commit": "c2", "contains": true}
```
