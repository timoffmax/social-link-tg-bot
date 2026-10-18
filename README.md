# Social Link Telegram Bot

A Telegram bot that downloads and sends actual media files from social media links instead of leaving broken previews. Drop an Instagram, TikTok, Twitter/X, YouTube, Reddit, Facebook, 9GAG, or Threads link into any chat — the bot fetches the real photos/videos and replies with them directly.

## Features

- **Real media, not links** — downloads photos, videos, galleries, slideshows, carousels and stories, and sends them as native Telegram media; a post's photos and videos arrive as one album in post order (split evenly when over Telegram's 10-item limit). Free Instagram extraction gets the first carousel item; the paid fallback gets them all
- **Layered extraction chains with automatic fallbacks** — every platform has multiple extraction methods tried in order, so a single dead third-party service never silently kills a platform:
  - **Instagram**: kkinstagram redirector → OG-preview fallback; posts gated by their author (e.g. age limits) are fetched through a paid Apify actor within monthly quotas (20 per chat by default; the caption shows how many are left; age-limited posts are blurred), and otherwise explained (with the reason, e.g. a used-up quota)
  - **Reddit**: vxreddit (works from datacenter IPs, resolves `/s/` share links) → direct JSON API (with inline JS-challenge solving; text posts) → yt-dlp
  - **TikTok**: direct scraping → slideshow API → tikwm.com → kktiktok redirector → headless browser; handles both videos and photo slideshows
  - **Twitter/X**: hybrid extraction — full tweet text (with author display name and recursive quote resolution) via FxTwitter + media via yt-dlp
  - **YouTube**: Shorts and community posts only (regular videos are ignored). Shorts: yt-dlp (HLS merging via ffmpeg, EJS challenge solver) → a paid Apify downloader when YouTube walls the server IP. Posts: text and images read from the post page
  - **Facebook**: yt-dlp (videos and reels; photo posts unsupported)
  - **Instagram stories**: a paid Apify stories scraper (stories need a login)
  - **Threads**: Meta's server-rendered post embed (text, photos, videos and mixed carousels in order) → vxthreads mirror → vxthreads fixed link
- **Paid fallbacks with a hard budget** — where free methods are blocked from datacenter IPs (author-gated Instagram posts, Instagram stories, YouTube Shorts), Apify actors take over. Every run is capped, and before each run the bot reads the account's real monthly spend and refuses runs that could exceed `APIFY_MONTHLY_BUDGET_USD`; runs in flight are counted too. Gated posts also have monthly quotas per chat/user, reserved atomically in PostgreSQL
- **Concurrent processing** — every Telegram update and every link within a message is processed in its own goroutine
- **Health monitoring** — hourly probes of third-party redirectors and proxies, plus failure tracking of the paid and page-reading extractors; consecutive failures trigger Telegram alerts to the admin, with recovery notices
- **Response validation** — third-party redirector responses are verified as real CDN media (content type, size, redirect target) so a broken service fails loudly instead of sending garbage
- **Media conversion** — automatic WebM→MP4 conversion for Telegram compatibility
- **Fixed-link fallback** — if every extraction method fails, the bot falls back to fixed-link services (fixupx, vxreddit, vxthreads, etc.) so the user still gets a working preview
- **Correct author links** — Instagram, TikTok and Threads usernames link to the author's profile (Telegram would otherwise turn `@name.with.dots` into a link to an unrelated Telegram user)

## Bot Commands

**Users**
| Command | Description |
|---|---|
| `/menu`, `/start`, `/help` | Main menu and usage guide (context-aware for private chats vs groups) |
| `/stats` | Media statistics (today/week/month/year) |
| `/mylimit` | Check your daily media limit |
| `/boost` | Raise a group's limit of restricted posts with Telegram Stars (needs `STARS_SUPPORT_ENABLED=true`). In a group it links to the private chat's boost screen (`/start boost_<chat_id>`) with the same tiers as `/donate` (25 / 50 / 100 Stars for +10 / +25 / +60). The group's bonus downloads are shared by all members and used after its free monthly ones, before the poster's own; the group gets a notice. Refunds remove the group's unused ones |
| `/info` | How the bot works: private chats vs groups, free vs restricted content, the default limits and why they exist (the same guide as `/start`, without the menu) |
| `/donate` (alias `/support`) | Support the bot. With `STARS_SUPPORT_ENABLED=true`: Telegram Stars tier buttons first (25 / 50 / 100 Stars, thank-you of +10 / +25 / +60 bonus downloads of restricted Instagram posts, used after the monthly free ones) and the user's bonus balance; always: a card option (monobank jar, any card or Apple/Google Pay) and crypto options (NOWPayments + USDT ERC-20/TRC-20), which unlock nothing. Also the "💚 Support the bot" menu button |
| `/report` | Report a bug or issue to the owner |
| `/paysupport`, `/terms` | Help with Stars payments and the support terms. They always answer (Telegram requires `/paysupport` once a bot has taken payments); they appear in the command menu only when `STARS_SUPPORT_ENABLED=true` |

**Group admins**
| Command | Description |
|---|---|
| `/setlimit` | Set a user's daily media limit |
| `/setgrouplimit` | Set the group default limit |
| `/setgrouptimezone` | Set the group timezone for limit resets |
| Platform toggles | Enable/disable individual platforms per chat |

**Owner**
| Command | Description |
|---|---|
| `/owner` | Owner panel |
| `/adminstats [period]` | Global stats: chats, media counts, platform breakdown, top chats, plus "Paid fetching": restricted-post fetches this month (vs the bot-wide cap, none by default), Apify spend vs budget, Stars support totals (month / all time, refunds excluded) and unused bonus downloads |
| `/broadcast` | Announcements with test/preview targeting (users vs groups) |
| `/gated` | Paid fetch of author-gated Instagram posts: status, `on`/`off` per group, monthly `cap` per group and per-`user` allowance (`N`, `unlimited`, `default`) |
| Blog system | Publish/edit/toggle posts shown to users (posts can also be written in the [owner dashboard](#owner-dashboard); sending them stays here) |
| Report inbox | Review `/report` submissions and `/reply` to reporters |
| `/starsrefund USER_ID CHARGE_ID` | Refund a Stars support payment in full and remove its unused bonus downloads (hidden; works even with Stars support off) |

## Architecture

```
main.go              → app.Run()
app/                 → polling loop, concurrent update processing, cleanup tasks
config/              → environment configuration
model/api/           → Telegram Bot API models (requests, payloads, responses)
services/            → one extractor per platform + shared infrastructure:
                       smart_downloader (strategy pattern, method fallbacks),
                       ytdlp_downloader, tiktok_extractor, twitter_extractor,
                       reddit_extractor + reddit_vxreddit_extractor + reddit_challenge,
                       instagram_* chain + instagram_gate (oEmbed gate check),
                       threads_extractor, youtube_post_extractor,
                       apify_client (shared budget) + gated_post_provider,
                       youtube_apify_extractor, instagram_stories_apify_extractor,
                       gated_fetch + gated_fetch_store (quotas),
                       redirector_health, outage_tracker, notify_admin,
                       video_converter, database, ...
app/media_album.go   → mixed photo/video albums
migrations/          → PostgreSQL migrations (stats, limits, chat settings,
                       blog posts, reports, gated-fetch quotas)
bin/                 → deployment and server tooling
cmd/dashboard/       → owner dashboard binary (social-link-dashboard)
dashboard/           → dashboard HTTP server, JSON API and SQL; web/ is the React app
```

The bot polls `getUpdates`, detects social links via regex, picks an extraction strategy per platform/URL type, walks the method chain until one succeeds, and sends the result via `sendPhoto`/`sendVideo`/`sendMediaGroup`/`sendDocument`. Temporary files are cleaned immediately after sending, plus an hourly sweep for stragglers.

## Setup

**Prerequisites**: Go 1.21+, [yt-dlp](https://github.com/yt-dlp/yt-dlp), ffmpeg (for YouTube merging and WebM conversion), PostgreSQL (optional — needed for stats/limits/blog/reports).

```bash
cp .env.example .env      # fill in TG_BOT_TOKEN (from @BotFather) and ADMIN_USER_ID
go mod download
go build -o social-link-bot .
./social-link-bot
```

### Key environment variables

| Variable | Purpose |
|---|---|
| `TG_BOT_TOKEN` | Bot token from @BotFather (required) |
| `ADMIN_USER_ID` | Your Telegram user ID for owner commands and outage alerts |
| `DB_*` | PostgreSQL connection (optional) |
| `ENABLE_<PLATFORM>` | Per-platform kill switches (`ENABLE_TIKTOK`, `ENABLE_REDDIT`, ...) |
| `ENABLE_PROXIES` / `YTDLP_PROXIES` | Global proxy kill switch + comma-separated proxy list (fallback only) |
| `APIFY_TOKEN`, `GATED_FETCH_*` | Paid fetch of author-gated Instagram posts and its monthly quotas (see `.env.example`, owner command `/gated`) |
| `APIFY_MONTHLY_BUDGET_USD`, `APIFY_YOUTUBE_ACTOR`, `APIFY_STORIES_ACTOR` | Shared monthly Apify spend limit and the YouTube / Instagram stories actors |
| `KKINSTAGRAM_HOST`, `KKTIKTOK_HOST`, `VXREDDIT_HOST` | Override third-party fallback hosts |
| `DEFAULT_DAILY_MEDIA_LIMIT` | Default per-user daily limit (0 = unlimited) |
| `STARS_SUPPORT_ENABLED` | Optional "support the developer" payments in Telegram Stars (tier buttons on `/donate`, alias `/support`; `/paysupport`, `/terms`); default `false` |

See `.env.example` for the full annotated list.

## Owner dashboard

A separate statistics page for the bot's owner, served by its own binary (`social-link-dashboard`) from the bot's database: downloads per day and platform, platform health (drops and stale platforms), top groups and users, retention, chats with custom settings, restricted posts, Stars payments and bonus credits, and bug reports. Its Blog tab creates, edits, publishes and deletes the posts the owner panel broadcasts. Broadcasting itself stays in the Telegram owner panel (`/owner` → 📢 Broadcast, which sends only the content, not the title): the dashboard has no bot token and sends nothing to Telegram.

It only reads, except the Blog tab: the blog endpoints accept POST/PUT/DELETE, everything else only GET/HEAD, and every other query runs in a read-only transaction. The database role needs SELECT on the tables it reads and write grants on `blog_posts` only (INSERT/UPDATE/DELETE, plus USAGE on `blog_posts_id_seq`) (keep `default_transaction_read_only=on`; the blog writes open their own read-write transaction). Blog writes (`POST /api/blog-posts`, `PUT /api/blog-posts/{id}`, `PUT /api/blog-posts/{id}/published`, `DELETE /api/blog-posts/{id}`) must carry `X-Dashboard-Request: 1`, an `Origin` equal to the dashboard's own origin and, with a body, `Content-Type: application/json` (otherwise `403`; bodies up to 64 KiB, unknown fields refused); no response carries CORS headers, and requests with a `Host` outside `DASHBOARD_ALLOWED_HOSTS` are refused (DNS rebinding). It has no login: it binds to loopback by default and is meant to be reached over a private network (VPN) or through a reverse proxy that does the access control (pass `Host`, set `X-Forwarded-Proto`), never directly from the internet.

```bash
make dashboard       # npm ci + npm run build in dashboard/web (the React app is embedded), then go build ./cmd/dashboard
make dashboard-dev   # build, then run ./social-link-dashboard locally
```

Without the frontend build the binary still compiles, but every page answers 503.

| Variable | Default | Purpose |
|---|---|---|
| `DASHBOARD_ADDR` | `127.0.0.1:8090` | Listen address; a non-loopback host is refused |
| `DASHBOARD_ALLOW_PUBLIC` | unset | `true` allows a non-loopback `DASHBOARD_ADDR` |
| `DASHBOARD_ALLOWED_HOSTS` | `localhost`, `127.0.0.1`, `[::1]` on the listen port, plus `DASHBOARD_ADDR` | Comma-separated `Host` values (`host:port`) the dashboard answers; set it for a host name or a reverse proxy |
| `DASHBOARD_DATABASE_URL` | `postgres:///social_link_bot?host=/var/run/postgresql&sslmode=disable` | PostgreSQL URL (peer auth over the local socket); the role needs SELECT plus the `blog_posts` grants above |
| `DASHBOARD_TIMEZONE` | `Europe/Kyiv` | IANA timezone for day/week/month buckets (`Local` is refused) |

These are read from the process environment, not from the bot's `.env`. `cd dashboard/web && npm run dev` serves the frontend with `/api` proxied to `127.0.0.1:8090`. SQL integration tests run when `DASHBOARD_TEST_DATABASE_URL` points to a throwaway database: `DASHBOARD_TEST_DATABASE_URL=... go test ./dashboard/`. Endpoints and response shapes: [docs/dashboard.md](docs/dashboard.md).

## Deployment

```bash
cp bin/deploy.conf.sample bin/deploy.conf   # set SERVER_IP, SERVER_USER, SERVER_SSH_KEY, REMOTE_DIR, SERVICE_NAME
./bin/deploy                                # git pull and build on the server, then restart
./bin/deploy --restart-only                 # restart without pulling or rebuilding
./bin/deploy --dashboard-only               # pull, build and restart only the owner dashboard
```

The dashboard step is optional: it runs only when `bin/deploy.conf` sets `DASHBOARD_SERVICE_NAME` (the server then needs Node 22+ for the frontend build); with it set, `--restart-only` restarts the dashboard too.

`make status` and `make logs` read the same `bin/deploy.conf`. See [bin/README.md](bin/README.md) for the full deployment and one-time server setup guide.

## Development

See [README-dev.md](README-dev.md) for local development, testing with a BotFather test bot, and the project structure.

## License

Copyright (C) 2024-2026 Tymofii Maksymenko.

Licensed under the [GNU Affero General Public License v3.0](LICENSE), version 3 only (`SPDX-License-Identifier: AGPL-3.0-only`); the "any later version" option in the license's how-to-apply appendix is not used. You may use, modify and self-host the bot; if you run a modified version as a service for others, you must make your modified source available to its users under the same license.

Versions up to and including commit `6586ac6` were released under the MIT License and remain available under it.
