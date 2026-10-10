# Email notifications

Nipa sends transactional email through a pluggable sender. The sender layer
(`internal/mail`) supports SMTP, the SendGrid v3 REST API and a generic HTTP
JSON sender that adapts to any REST provider; delivery is durable through an
outbox (see [Delivery model](#delivery-model)).

Status: the sender layer, configuration, the durable outbox and the merge
request notification wiring are implemented. The delivery admin surface
(listing failed deliveries and redelivery) follows in a later change.

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
backoff, and records `delivered`/`failed` state with the last error. Failed
deliveries can be inspected and redelivered from the project settings page.
(The inspection and redelivery surface is implemented in a follow-up phase.)

## Configuration reference

| Key                             | Default                  | Description                                             |
| ------------------------------- | ------------------------ | ------------------------------------------------------- |
| `EMAIL_SENDER`                  | `off`                    | `off`, `log`, `smtp`, `sendgrid` or `http`.             |
| `EMAIL_FROM`                    | –                        | Envelope and header From address.                       |
| `EMAIL_REPLY_TO`                | –                        | Optional Reply-To address.                              |
| `EMAIL_BASE_URL`                | –                        | Public web UI URL for links in emails.                  |
| `EMAIL_TIMEOUT_SECONDS`         | `10`                     | Timeout per delivery attempt.                           |
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
