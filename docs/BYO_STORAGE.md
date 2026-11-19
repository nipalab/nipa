# Bring-your-own storage

On `nipad-ee` in SaaS mode an organization's billing owner can register their
own S3-compatible servers and put projects on them. Nipa then stores only the
metadata of those projects (trees, files, chunk rows); the chunk content goes
to the customer's bucket, and the storage ledger counts none of it.

## Enabling

Set `BYO_STORAGE_ENCRYPTION_KEY` (base64 of at least 32 bytes,
`openssl rand -base64 32`) next to `SAAS_MODE=1`. The key seals the stored
secret keys; losing it makes every stored server unreadable. Both deploy
colors must share it. `BYO_STORAGE_ALLOW_PRIVATE=true` accepts `http://` and
private or loopback addresses and is for tests only.

## Rules

- Each user owns up to 20 servers. Name, endpoint, region and keys can change;
  bucket and prefix are fixed once saved. The secret key is write-only.
- Creating or editing a server probes it first; a failed probe names the
  check (`bucket`, `signed_content_length`, `presigned_put`,
  `conditional_create`, `presigned_get`, `stat`, `delete`, or `open`).
- A server can be deleted only while no live project and no live
  organization's default uses it; deleting it drops the assignments of
  deleted projects and the defaults of deleted organizations.
- A project's store is chosen once, at creation: the requested server (the
  caller must own it and be the org's billing owner), else the org default,
  else Nipa storage. It never changes afterwards.
- The org default (`PUT /orgs/{org}/storage-server`) can be set by an org
  owner who is the billing owner, to one of their own servers. It applies to
  projects created later only.
- When a server cannot be reached, that project's transfers fail with 502
  `storage server unavailable`.
- With billing on, writing to own storage needs the Own storage add-on
  (`billing.Service.OwnStorageActive`, wired through
  `byostorage.Service.WithEntitlement`, see `docs/BILLING.md`). Without it,
  projects on own storage are read-only (pushes and merges get 402, reads keep
  working) and creating, editing or assigning servers gets 402; deleting
  stays allowed. With `BILLING_PROVIDER=off` nothing is gated. Billing on
  without add-on prices logs a warning: every owner is then read-only.

## S3 requirements

The server must support, over a public HTTPS endpoint reachable by Nipa and
by the users' clients:

- SigV4 presigned PUT with a signed `Content-Length`, refusing a longer body;
- conditional create on presigned PUT: `If-None-Match: *` answers 412 when
  the object exists (AWS S3, MinIO and Cloudflare R2 do; a backend that
  ignores it is refused);
- SigV4 presigned GET;
- `HeadBucket`, `HeadObject`, `GetObject` and `DeleteObject`.

The access key needs `s3:ListBucket`, `s3:GetObject`, `s3:PutObject` and
`s3:DeleteObject` on the bucket and prefix. Objects are keyed
`<prefix>/<hash[:2]>/<hash[2:]>`. For R2 use
`https://<account>.r2.cloudflarestorage.com` with region `auto`.

Clients download from the bucket directly, so the customer pays the
provider's egress for clones and updates; Nipa also reads every new object
back once to verify its hash.
