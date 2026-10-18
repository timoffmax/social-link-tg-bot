# Owner dashboard

A statistics page for the bot's owner: a React app (`dashboard/web`) embedded into a separate Go binary (`cmd/dashboard`, package `dashboard`) that reads the bot's PostgreSQL database. It does not share code paths or configuration with the bot. It only reads, except two features: the Blog tab creates, edits, publishes and deletes rows of `blog_posts`, and the Reports tab queues replies to reports (`report_replies`) and resolves or reopens reports. It has no bot token and sends nothing to Telegram: broadcasting stays in the Telegram owner panel, which works on the same table, and queued replies are sent by the bot's reply outbox.

## Tabs

| Tab | Shows |
|---|---|
| Overview | Downloads 7d/30d, active users and chats, all-time groups and private users, daily private vs group downloads, platform mix, new chats per week |
| Platforms | Per-platform health (ok / drop / stale), daily downloads per platform, photo vs video split |
| Chats | Top groups, top private users, month-over-month retention, chats with custom settings or limits |
| Money | Restricted (gated) posts per month and per chat/user this month, Stars payments, refunds and boosts per month, unused bonus credits |
| Reports | Open vs resolved bug reports, reports per week, latest reports (filter Open / Resolved / All) with their replies and status (Queued / Sending / Sent / Failed with the reason / Delivery unknown: "May have been delivered — check before sending again"; a queued reply waiting for another attempt shows Telegram's last answer). Reply opens a composer with the blog editor's toolbar, a Telegram preview of the message as the reporter gets it (heading and length limit come from the server) and a length counter; "Resolve after it's delivered" is on by default: the bot resolves the report only once Telegram confirms the delivery. Resolve / Reopen sets the flag and the resolve time (resolving again keeps the first resolve time) |
| Blog | The posts the bot's owner panel broadcasts: list, editor with a live Telegram preview, publish/unpublish, delete. Broadcasting stays in Telegram (`/owner` → 📢 Broadcast), which sends only the content as the message text, never the title |

The page refreshes every 5 minutes and has a manual refresh button.

### Report replies

The dashboard never sends a reply itself. `POST /api/reports/{id}/replies` stores it in `report_replies` as `queued` (source `dashboard`, `resolve_on_send` from the "Resolve after it's delivered" box); the bot's outbox worker picks the oldest due queued reply every 5 seconds (`claimed_at` is set when it moves to `sending`) and sends it to the reporter's private chat with the heading `💬 Reply from the developer:` followed by the text, in Telegram HTML, exactly like `/reply` in Telegram. The heading is one constant (`model/reportreply`) shared by the bot and the dashboard, which returns it as `reply_prefix` with `reply_text_limit` in `GET /api/reports`; the package holds only constants, so the dashboard still shares no code path with the bot. The outcome:

- `sent`: Telegram answered with the message. With `resolve_on_send` the bot resolves the report (`resolved=true`, `resolved_at` kept when already set) in the same statement that marks the reply sent.
- Back to `queued`: Telegram refused it for a passing reason (`429 Too Many Requests`, a `5xx`). `attempts` goes up and `next_attempt_at` is Telegram's `retry_after` (or 30 s times the attempts); a `429` also ends the outbox's current batch. After 5 attempts the reply is `failed` ("gave up after 5 attempts"). No owner notice until then.
- `failed`: a definite "not delivered": Telegram refused it (e.g. `Forbidden: bot was blocked by the user`), or the request could not even be built, so it never left the bot. The owner gets a Telegram notice.
- `unknown`: no answer from Telegram after the request left (timeout, dropped connection, an answer cut off before its end: `delivery unknown (no answer from Telegram)`), or a reply that stayed `sending` more than 5 minutes after its claim (well above the 120 s Telegram client timeout; checked on every outbox pass: `interrupted, delivery unknown, not resent`). It may have been delivered; the owner gets a notice saying so.

Failed and unknown replies are never resent automatically and leave the report open. After queuing, the page checks the report every 5 seconds until the reply is no longer queued or sending, for at most 2 minutes. Replies sent with `/reply` in Telegram are recorded too (source `telegram`, `sending` with `claimed_at` while it is sent, then `sent`, `failed` or `unknown`; a passing refusal is not retried there, the owner sees the reason) and listed with the report.

## Security model

- No login. The binary binds to loopback by default (`127.0.0.1:8090`) and refuses a non-loopback address unless `DASHBOARD_ALLOW_PUBLIC=true`. Reach it over a private network (VPN) or put it behind a reverse proxy that does its own access control; do not expose it to the internet.
- Known hosts only: a request whose `Host` header is not in `DASHBOARD_ALLOWED_HOSTS` answers `403 {"error":"forbidden"}` on every path, so a DNS-rebinding page cannot read the statistics or write blog posts.
- Reads run in a `READ ONLY` transaction with a 5 s `statement_timeout`. Only the blog and report writes open a `READ WRITE` transaction, which overrides the role's `default_transaction_read_only=on`. Queuing a reply never resolves the report: the bot does that once the reply is delivered.
- Database role (set up on the server, not in this repository): `SELECT` on the tables it reads (including `report_replies`); write grants on `blog_posts` (`INSERT, UPDATE, DELETE`, plus `USAGE` on `blog_posts_id_seq`), `report_replies` (`INSERT` on the table, which covers every column the dashboard writes, `resolve_on_send` included, plus `USAGE` on `report_replies_id_seq`) and the `resolved` and `resolved_at` columns of `reports` (`UPDATE`, for the Resolve / Reopen button only); keep `default_transaction_read_only=on`. Without them the writes answer `500` (the log shows `permission denied for table ...`). For example:

  ```sql
  GRANT INSERT, UPDATE, DELETE ON blog_posts TO <dashboard_role>;
  GRANT USAGE ON SEQUENCE blog_posts_id_seq TO <dashboard_role>;
  GRANT SELECT, INSERT ON report_replies TO <dashboard_role>;
  GRANT USAGE ON SEQUENCE report_replies_id_seq TO <dashboard_role>;
  GRANT UPDATE (resolved, resolved_at) ON reports TO <dashboard_role>;
  ```

- Only the blog and report write endpoints accept `POST`, `PUT` or `DELETE`; every other path accepts only `GET` and `HEAD` (anything else: `405`). A blog or report write must carry `X-Dashboard-Request: 1`, an `Origin` equal to the request's own origin (`http`/`https` from `X-Forwarded-Proto`, else from TLS, plus the `Host` header) and, with a body, `Content-Type: application/json`; otherwise it answers `403`. Bodies are limited to 64 KiB (`413`) and unknown JSON fields are refused (`400`). No response carries CORS headers.
- Behind a reverse proxy: pass the browser's `Host` unchanged (nginx: `proxy_set_header Host $host;`), set `X-Forwarded-Proto` (`proxy_set_header X-Forwarded-Proto $scheme;`), and list the public host in `DASHBOARD_ALLOWED_HOSTS`. Otherwise every request answers `403` (unknown host) or every write does (origin mismatch).
- Every response carries `Content-Security-Policy: default-src 'self'; ...`, `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`; API answers are `Cache-Control: no-store`. The frontend bundles all its code and makes no external requests.

## Configuration

Read from the process environment (not the bot's `.env`):

| Variable | Default | Purpose |
|---|---|---|
| `DASHBOARD_ADDR` | `127.0.0.1:8090` | Listen address; a non-loopback host is refused |
| `DASHBOARD_ALLOW_PUBLIC` | unset | `true` allows a non-loopback `DASHBOARD_ADDR` |
| `DASHBOARD_ALLOWED_HOSTS` | `localhost`, `127.0.0.1` and `[::1]` on the port of `DASHBOARD_ADDR`, plus `DASHBOARD_ADDR` itself unless it is all interfaces | Comma-separated `Host` header values the dashboard answers, as the browser sends them (`host:port`, no port for 80/443), case-insensitive; set it when reaching the dashboard by a name or through a proxy, e.g. `stats.example.com,127.0.0.1:8090` |
| `DASHBOARD_DATABASE_URL` | `postgres:///social_link_bot?host=/var/run/postgresql&sslmode=disable` | PostgreSQL URL (peer auth over the local socket by default) |
| `DASHBOARD_TIMEZONE` | `Europe/Kyiv` | IANA timezone for day/week/month buckets; checked against Go and PostgreSQL at start-up (`Local` is refused) |

## Build and run

```bash
make dashboard        # npm ci + npm run build in dashboard/web, then go build ./cmd/dashboard
make dashboard-dev    # the same, then runs ./social-link-dashboard
```

Without a frontend build the binary still compiles (a committed `dashboard/web/dist/.gitkeep` keeps the embed valid), but every page answers `503`. For frontend work run the binary and `cd dashboard/web && npm run dev`: Vite serves the app with `/api` proxied to `127.0.0.1:8090`.

Deployment: when `bin/deploy.conf` sets `DASHBOARD_SERVICE_NAME`, `./bin/deploy` also builds the frontend and the binary on the server and restarts that systemd service; `./bin/deploy --dashboard-only` deploys only the dashboard and `--restart-only` restarts it together with the bot. The service unit, the database role and any reverse proxy are set up by the owner outside this repository.

## Endpoints

All answers are JSON. Every data answer contains `generated_at` (RFC3339, UTC) and `timezone`. Days are `YYYY-MM-DD` and months `YYYY-MM` in the dashboard timezone, weeks are named by their Monday, timestamps are RFC3339. Series are gap-filled with zeros. A chat with `chat_id > 0` is private, `chat_id < 0` is a group.

Query parameters are clamped to their range; an empty one takes the default; a non-integer one answers `400`.

| Endpoint | Parameters (default, range) | Contents |
|---|---|---|
| `GET /healthz` | | `{"ok":true}` after pinging the database |
| `GET /api/overview` | `days` (90, 1-365) | `totals`, `daily` (private/group per day), `platform_mix`, `new_chats_weekly` |
| `GET /api/platforms` | `days` (90, 1-365) | `platforms`, `daily` (`counts` per platform), `media_types`, `health` |
| `GET /api/chats` | `days` (30, 1-365), `limit` (20, 1-200) | `top_groups`, `top_users`, `retention`, `custom_settings` |
| `GET /api/money` | `months` (12, 1-36) | `gated_monthly`, `gated_current`, `stars_monthly`, `credits` |
| `GET /api/reports` | `limit` (50, 1-200), `weeks` (26, 1-104), `status` (`all`; `open`, `resolved` or `all`, anything else `400`) | `status`, `reply_prefix` (the heading the bot puts above a reply), `reply_text_limit`, `totals`, `weekly` (both over all reports), `latest` (filtered by `status`; each with `resolved_at` and `replies`: `id`, `text`, `source`, `status` (`queued`, `sending`, `sent`, `failed`, `unknown`), `resolve_on_send`, `attempts`, `next_attempt_at`, `error`, `created_at`, `sent_at`, oldest first) |
| `GET /api/reports/{id}` | | `report` with its replies, or `404` (polled while a reply is queued) |
| `POST /api/reports/{id}/replies` | body `{"text","resolve":true\|false}` | `201` with the queued `reply` and the `report` (unchanged); with `resolve: true` the reply carries `resolve_on_send` and the bot resolves the report once it is delivered; `404` for an unknown report |
| `PUT /api/reports/{id}/resolved` | body `{"resolved":true\|false}` | `report` (`resolved_at` set to now, kept when it already is resolved, cleared on reopen), or `404` |
| `GET /api/blog-posts` | | `posts` (newest first: `id`, `title`, `content`, `published`, `created_at`, `updated_at`, `length`) |
| `GET /api/blog-posts/{id}` | | `post`, or `404` |
| `POST /api/blog-posts` | body `{"title","content"}` | `201` with the new `post` (unpublished) |
| `PUT /api/blog-posts/{id}` | body `{"title","content","updated_at"}` | `post`; `404` when it is gone, `409 {"error":"changed elsewhere"}` when `updated_at` is not the one the post was loaded with |
| `PUT /api/blog-posts/{id}/published` | body `{"published":true\|false}` | `post`, or `404` |
| `DELETE /api/blog-posts/{id}` | | `204`, or `404` |

Blog validation (`400` with the reason): the title is trimmed, 1-255 characters; the content is trimmed, non-empty, valid Telegram HTML (only the tags and attributes of Telegram's HTML parse mode, every tag closed and nested, `<`, `>` and `&` escaped; the error names the line and column) and at most 3963 characters as Telegram counts them (tags removed, entities decoded, UTF-16 code units). That is Telegram's 4096 minus the up to 133 characters the bot's Broadcast preview adds around the post: a longer post could not be shown there, so it could never be sent. `length` is that count.

Reply validation (`400` with the reason): the text is trimmed, non-empty, valid Telegram HTML (same check as blog posts) and at most 4066 characters as Telegram counts them: Telegram's 4096 minus the 30 of the `💬 Reply from the developer:` heading the bot puts above it. `resolve` (and `resolved` on the resolve endpoint) is required.

Platform health lists every platform with a download in the last 90 days and compares the average daily downloads of the last 7 days with the 28 days before them; for a platform silent longer than 7 days the 28 days end at its last download, so a dead platform keeps its baseline. `stale` when nothing was downloaded for 48 hours, `drop` when the 7-day average is under half of the previous one (both only when the previous average is at least 1 per day), otherwise `ok`.

Custom settings list the chats with a daily limit, per-user limits or a setting changed from its default (every setting defaults to on: a platform is off by its enabled flag, any other setting by a false value). The all-default rows the bot stores when a group opens its platform menu are left out, and only the changed settings are shown.

In `gated_current`, `limit` is a chat's or user's custom monthly limit of restricted posts and `custom` says whether one is set; `limit` is `null` both for the bot's default limit (`custom: false`) and for an unlimited chat or user (`unlimited: true`, `custom: true`, the stored `-1`).

The exact response shapes are defined twice and kept in sync:

- Go: [`dashboard/types.go`](../dashboard/types.go)
- TypeScript client: [`dashboard/web/src/api.ts`](../dashboard/web/src/api.ts)

Errors are `{"error":"<short message>"}`: `400` for bad parameters, an invalid blog post or reply, `403` for an unknown host or an unprotected write, `404` for an unknown API path or a missing post or report, `405` for a method the path does not accept, `409` for a blog edit that lost to a concurrent one, `413` for a body over 64 KiB, `500` for a database failure (the real error is only logged).

## Tests

```bash
go test ./dashboard/                                                  # unit tests (no database)
DASHBOARD_TEST_DATABASE_URL=postgres://... go test ./dashboard/       # plus SQL integration tests
cd dashboard/web && npm run build                                     # strict type check and bundle
```

The integration tests apply `migrations/*.up.sql` to the given database and insert fixtures, so point them at a throwaway database only.
