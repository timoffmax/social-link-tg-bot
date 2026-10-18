# Owner dashboard

A statistics page for the bot's owner: a React app (`dashboard/web`) embedded into a separate Go binary (`cmd/dashboard`, package `dashboard`) that reads the bot's PostgreSQL database. It does not share code paths or configuration with the bot. It only reads, except the Blog tab, which creates, edits, publishes and deletes rows of `blog_posts`. It has no bot token and sends nothing to Telegram: broadcasting stays in the Telegram owner panel, which works on the same table.

## Tabs

| Tab | Shows |
|---|---|
| Overview | Downloads 7d/30d, active users and chats, all-time groups and private users, daily private vs group downloads, platform mix, new chats per week |
| Platforms | Per-platform health (ok / drop / stale), daily downloads per platform, photo vs video split |
| Chats | Top groups, top private users, month-over-month retention, chats with custom settings or limits |
| Money | Restricted (gated) posts per month and per chat/user this month, Stars payments, refunds and boosts per month, unused bonus credits |
| Reports | Open vs resolved bug reports, reports per week, latest reports |
| Blog | The posts the bot's owner panel broadcasts: list, editor with a live Telegram preview, publish/unpublish, delete. Broadcasting stays in Telegram (`/owner` → 📢 Broadcast), which sends only the content as the message text, never the title |

The page refreshes every 5 minutes and has a manual refresh button.

## Security model

- No login. The binary binds to loopback by default (`127.0.0.1:8090`) and refuses a non-loopback address unless `DASHBOARD_ALLOW_PUBLIC=true`. Reach it over a private network (VPN) or put it behind a reverse proxy that does its own access control; do not expose it to the internet.
- Known hosts only: a request whose `Host` header is not in `DASHBOARD_ALLOWED_HOSTS` answers `403 {"error":"forbidden"}` on every path, so a DNS-rebinding page cannot read the statistics or write blog posts.
- Reads run in a `READ ONLY` transaction with a 5 s `statement_timeout`. Only the blog writes open a `READ WRITE` transaction, which overrides the role's `default_transaction_read_only=on`.
- Database role (set up on the server, not in this repository): `SELECT` on the tables it reads; write grants on `blog_posts` only (`INSERT, UPDATE, DELETE`, plus `USAGE` on `blog_posts_id_seq`); keep `default_transaction_read_only=on`. Without the blog grants every Blog Save, Publish and Delete answers `500` (the log shows `permission denied for table blog_posts`). For example:

  ```sql
  GRANT INSERT, UPDATE, DELETE ON blog_posts TO <dashboard_role>;
  GRANT USAGE ON SEQUENCE blog_posts_id_seq TO <dashboard_role>;
  ```

- Only the blog endpoints accept `POST`, `PUT` or `DELETE`; every other path accepts only `GET` and `HEAD` (anything else: `405`). A blog write must carry `X-Dashboard-Request: 1`, an `Origin` equal to the request's own origin (`http`/`https` from `X-Forwarded-Proto`, else from TLS, plus the `Host` header) and, with a body, `Content-Type: application/json`; otherwise it answers `403`. Bodies are limited to 64 KiB (`413`) and unknown JSON fields are refused (`400`). No response carries CORS headers.
- Behind a reverse proxy: pass the browser's `Host` unchanged (nginx: `proxy_set_header Host $host;`), set `X-Forwarded-Proto` (`proxy_set_header X-Forwarded-Proto $scheme;`), and list the public host in `DASHBOARD_ALLOWED_HOSTS`. Otherwise every request answers `403` (unknown host) or every blog write does (origin mismatch).
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
| `GET /api/reports` | `limit` (50, 1-200), `weeks` (26, 1-104) | `totals`, `weekly`, `latest` |
| `GET /api/blog-posts` | | `posts` (newest first: `id`, `title`, `content`, `published`, `created_at`, `updated_at`, `length`) |
| `GET /api/blog-posts/{id}` | | `post`, or `404` |
| `POST /api/blog-posts` | body `{"title","content"}` | `201` with the new `post` (unpublished) |
| `PUT /api/blog-posts/{id}` | body `{"title","content","updated_at"}` | `post`; `404` when it is gone, `409 {"error":"changed elsewhere"}` when `updated_at` is not the one the post was loaded with |
| `PUT /api/blog-posts/{id}/published` | body `{"published":true\|false}` | `post`, or `404` |
| `DELETE /api/blog-posts/{id}` | | `204`, or `404` |

Blog validation (`400` with the reason): the title is trimmed, 1-255 characters; the content is trimmed, non-empty, valid Telegram HTML (only the tags and attributes of Telegram's HTML parse mode, every tag closed and nested, `<`, `>` and `&` escaped; the error names the line and column) and at most 3963 characters as Telegram counts them (tags removed, entities decoded, UTF-16 code units). That is Telegram's 4096 minus the up to 133 characters the bot's Broadcast preview adds around the post: a longer post could not be shown there, so it could never be sent. `length` is that count.

Platform health lists every platform with a download in the last 90 days and compares the average daily downloads of the last 7 days with the 28 days before them; for a platform silent longer than 7 days the 28 days end at its last download, so a dead platform keeps its baseline. `stale` when nothing was downloaded for 48 hours, `drop` when the 7-day average is under half of the previous one (both only when the previous average is at least 1 per day), otherwise `ok`.

Custom settings list the chats with a daily limit, per-user limits or a setting changed from its default (every setting defaults to on: a platform is off by its enabled flag, any other setting by a false value). The all-default rows the bot stores when a group opens its platform menu are left out, and only the changed settings are shown.

The exact response shapes are defined twice and kept in sync:

- Go: [`dashboard/types.go`](../dashboard/types.go)
- TypeScript client: [`dashboard/web/src/api.ts`](../dashboard/web/src/api.ts)

Errors are `{"error":"<short message>"}`: `400` for bad parameters or an invalid blog post, `403` for an unknown host or an unprotected blog write, `404` for an unknown API path or a missing post, `405` for a method the path does not accept, `409` for a blog edit that lost to a concurrent one, `413` for a body over 64 KiB, `500` for a database failure (the real error is only logged).

## Tests

```bash
go test ./dashboard/                                                  # unit tests (no database)
DASHBOARD_TEST_DATABASE_URL=postgres://... go test ./dashboard/       # plus SQL integration tests
cd dashboard/web && npm run build                                     # strict type check and bundle
```

The integration tests apply `migrations/*.up.sql` to the given database and insert fixtures, so point them at a throwaway database only.
