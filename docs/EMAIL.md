# Email notifications

Nipa sends transactional email through a pluggable sender. The sender layer
(`internal/mail`) supports SMTP, the SendGrid v3 REST API and a generic HTTP
JSON sender that adapts to any REST provider; delivery is durable through an
outbox (see [Delivery model](#delivery-model)).

Status: complete. The sender layer, configuration, the durable outbox, the
merge request notification wiring and the delivery admin surface (inspection
and redelivery) are implemented.

## Choosing a sender

`EMAIL_SENDER` selects the transport:

| Value      | Transport                                                              |
| ---------- | ---------------------------------------------------------------------- |
| `off`      | Default. No email is sent; the notification seam is unwired.           |
| `log`      | Writes rendered messages to the server log. Development and tests.     |
| `smtp`     | Any provider with an SMTP endpoint (SES, Mailgun, Postmark, Gmail, …). |
| `sendgrid` | SendGrid v3 API (`POST /v3/mail/send`) with an API key.                |
| `http`     | Generic REST sender: configurable endpoint, method, headers and body.  |

`EMAIL_FROM` is required by every active sender except `log` and accepts
either `noreply@example.com` or `Nipa <noreply@example.com>`. `EMAIL_REPLY_TO`
is optional. `EMAIL_BASE_URL` is the public web UI URL used for deep links in
notification emails (for example `https://nipa.example.com`).

## SMTP

```yaml
EMAIL_SENDER: smtp
EMAIL_FROM: 'Nipa <noreply@example.com>'
EMAIL_SMTP_HOST: smtp.example.com
EMAIL_SMTP_PORT: 587            # 0 = 587/465/25 for starttls/ssl/none
EMAIL_SMTP_USERNAME: mailer
EMAIL_SMTP_PASSWORD: secret
EMAIL_SMTP_TLS: starttls        # starttls | ssl | none
```

`starttls` upgrades the connection on the submission port, `ssl` dials with
implicit TLS (port 465) and `none` speaks plain SMTP (port 25, local relays).
`EMAIL_SMTP_INSECURE_SKIP_VERIFY: true` accepts self-signed certificates and
should stay off outside development. Authentication uses AUTH PLAIN; the Go
SMTP client refuses to send credentials over an unencrypted connection to a
non-local host.

Provider notes:

- **Amazon SES** — SMTP credentials from the SES console, host
  `email-smtp.<region>.amazonaws.com`, port 587, `starttls`.
- **Gmail / Google Workspace** — `smtp.gmail.com`, port 587, `starttls`, an
  app password.
- **Mailgun / Postmark** — `smtp.mailgun.org` / `smtp.postmarkapp.com`, port
  587, the provider's SMTP credentials.

## SendGrid

```yaml
EMAIL_SENDER: sendgrid
EMAIL_FROM: 'Nipa <noreply@example.com>'
EMAIL_SENDGRID_API_KEY: SG.xxxxx
# EMAIL_SENDGRID_ENDPOINT: https://api.sendgrid.com   # override for tests
```

Messages carry plain-text and HTML parts; `Message-ID`, `In-Reply-To` and
`References` are forwarded as personalization headers so notification threads
stay grouped in the recipient's mail client.

## Generic HTTP

`http` posts a rendered template per recipient to any REST provider. The
template receives `.From`, `.FromName`, `.FromEmail`, `.ReplyTo`, `.To` (list),
`.ToHeader` (comma-joined), `.Subject`, `.Text`, `.HTML`, `.MessageID`,
`.InReplyTo` and `.References`, plus a `json` helper and the standard
`urlquery` helper. `EMAIL_HTTP_HEADERS` is a JSON object of extra request
headers.

The default body is provider-neutral:

```json
{"from":"…","from_email":"…","to":["…"],"subject":"…","text":"…","html":"…"}
```

Provider examples:

**Resend**

```yaml
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: https://api.resend.com/emails
EMAIL_HTTP_HEADERS: '{"Authorization":"Bearer re_xxxxx"}'
EMAIL_HTTP_BODY_TEMPLATE: '{"from":{{json .FromEmail}},"to":{{json .To}},"subject":{{json .Subject}},"text":{{json .Text}},"html":{{json .HTML}}}'
```

**Postmark**

```yaml
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: https://api.postmarkapp.com/email
EMAIL_HTTP_HEADERS: '{"X-Postmark-Server-Token":"xxxxx"}'
EMAIL_HTTP_BODY_TEMPLATE: '{"From":{{json .FromEmail}},"To":{{json .ToHeader}},"Subject":{{json .Subject}},"TextBody":{{json .Text}},"HtmlBody":{{json .HTML}}}'
```

**Brevo**

```yaml
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: https://api.brevo.com/v3/smtp/email
EMAIL_HTTP_HEADERS: '{"api-key":"xxxxx"}'
EMAIL_HTTP_BODY_TEMPLATE: '{"sender":{"email":{{json .FromEmail}}},"to":[{"email":{{json .ToHeader}}}],"subject":{{json .Subject}},"textContent":{{json .Text}},"htmlContent":{{json .HTML}}}'
```

**Mailgun** (form-encoded)

```yaml
EMAIL_SENDER: http
EMAIL_HTTP_ENDPOINT: https://api.mailgun.net/v3/mg.example.com/messages
EMAIL_HTTP_METHOD: POST
EMAIL_HTTP_CONTENT_TYPE: application/x-www-form-urlencoded
EMAIL_HTTP_HEADERS: '{"Authorization":"Basic <base64 of api:key>"}'
EMAIL_HTTP_BODY_TEMPLATE: 'from={{urlquery .FromEmail}}&to={{urlquery .ToHeader}}&subject={{urlquery .Subject}}&text={{urlquery .Text}}'
```

## Log

`EMAIL_SENDER: log` renders and logs messages without sending them. Useful for
local development and for the test suite.

## Delivery model

Email delivery is durable: notification events render one message per
recipient and enqueue a row in the `email_deliveries` outbox in the same
request that landed the change. A background dispatcher claims due rows,
sends them through the configured sender with bounded retries and exponential
backoff, and records `delivered`/`failed` state with the last error.

## Notifications

Merge request events produce email to the participants: `mr.created`,
`mr.ready_for_review`, `mr.review_requested`, `mr.review_submitted`,
`mr.comment_created`, `mr.synchronized`, `mr.merged`, `mr.closed`,
`mr.reopened` and failed `mr.check_reported` reports. The recipient set is the
author, the requested reviewers, the assignees and the commenters; the actor,
deleted users, users without a valid email address and users who disabled
`notify_email` in their profile are skipped, duplicates collapse and one event
fans out to at most 200 recipients. Each message carries a deep link to the
merge request when `EMAIL_BASE_URL` is set; the first message a recipient gets
about a merge request uses a per-recipient thread `Message-ID` that later
messages reference with `In-Reply-To`/`References`, so mail clients group the
conversation.

## Inspecting and redelivering

Project admins can inspect the outbox and retry deliveries:

- `GET /api/v1/orgs/{org}/projects/{project}/emails/deliveries` — newest first,
  filters `state`, keyset `after` cursor and `limit` (default 50, max 200);
  the response carries `next_cursor` when a next page exists.
- `POST /api/v1/orgs/{org}/projects/{project}/emails/deliveries/{id}/redeliver`
  — re-queues a `delivered` or `failed` delivery, resetting its attempt
  history; `pending` and `sending` deliveries return a 409.

The same surface is in the SPA under project settings → "Email deliveries"
(state filter, last error, redeliver). Delivery retries are tuned with
`EMAIL_MAX_ATTEMPTS` (default 5), `EMAIL_RETRY_BACKOFF_SECONDS` (default 10,
exponential) and `EMAIL_POLL_SECONDS` (default 10, how often the outbox is
polled).

## Configuration reference

| Key                             | Default                  | Description                                             |
| ------------------------------- | ------------------------ | ------------------------------------------------------- |
| `EMAIL_SENDER`                  | `off`                    | `off`, `log`, `smtp`, `sendgrid` or `http`.             |
| `EMAIL_FROM`                    | –                        | Envelope and header From address.                       |
| `EMAIL_REPLY_TO`                | –                        | Optional Reply-To address.                              |
| `EMAIL_BASE_URL`                | –                        | Public web UI URL for links in emails.                  |
| `EMAIL_TIMEOUT_SECONDS`         | `10`                     | Timeout per delivery attempt.                           |
| `EMAIL_MAX_ATTEMPTS`            | `5`                      | Total delivery attempts per message.                    |
| `EMAIL_RETRY_BACKOFF_SECONDS`   | `10`                     | Delay before the second attempt (exponential after).    |
| `EMAIL_POLL_SECONDS`            | `10`                     | How often the outbox is polled for due deliveries.      |
| `EMAIL_SMTP_HOST`               | –                        | SMTP host.                                              |
| `EMAIL_SMTP_PORT`               | per TLS mode             | 587 / 465 / 25 for starttls / ssl / none.               |
| `EMAIL_SMTP_USERNAME`           | –                        | SMTP username; empty skips authentication.              |
| `EMAIL_SMTP_PASSWORD`           | –                        | SMTP password.                                          |
| `EMAIL_SMTP_TLS`                | `starttls`               | `starttls`, `ssl` or `none`.                            |
| `EMAIL_SMTP_INSECURE_SKIP_VERIFY` | `false`                | Accept self-signed SMTP certificates.                   |
| `EMAIL_SENDGRID_API_KEY`        | –                        | SendGrid API key.                                       |
| `EMAIL_SENDGRID_ENDPOINT`       | `https://api.sendgrid.com` | API base URL override.                                |
| `EMAIL_HTTP_ENDPOINT`           | –                        | Generic sender endpoint URL.                            |
| `EMAIL_HTTP_METHOD`             | `POST`                   | HTTP method.                                            |
| `EMAIL_HTTP_HEADERS`            | –                        | JSON object of extra headers.                           |
| `EMAIL_HTTP_CONTENT_TYPE`       | `application/json`       | Request content type.                                   |
| `EMAIL_HTTP_BODY_TEMPLATE`      | provider-neutral JSON    | Go text/template for the request body.                  |
