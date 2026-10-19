# Social Link Telegram Bot

A Telegram bot that downloads and sends actual media files from social media links instead of leaving broken previews. Drop an Instagram, TikTok, Twitter/X, YouTube, Reddit, Facebook, 9GAG, or Threads link into any chat — the bot fetches the real photos/videos and replies with them directly.

## Features

- **Real media, not links** — downloads photos, videos, galleries, slideshows, carousels and stories, and sends them as native Telegram media; a post's photos and videos arrive as one album in post order (split evenly when over Telegram's 10-item limit). Free Instagram extraction gets the first carousel item; the paid fallback gets them all
- **Layered extraction chains with automatic fallbacks** — every platform has multiple extraction methods tried in order, so a single dead third-party service never silently kills a platform:
  - **Instagram**: kkinstagram redirector → OG-preview fallback; posts gated by their author (e.g. age limits) are fetched through a paid Apify actor (HikerAPI as the backup when configured) within monthly quotas (20 per chat by default; the caption shows how many are left; age-limited posts are blurred), and otherwise explained (with the reason, e.g. a used-up quota)
  - **Reddit**: vxreddit (works from datacenter IPs, resolves `/s/` share links) reads every post shape: videos (audio merged), every gallery image in order (albums of up to 10, a GIF in its place between them), GIFs as animations, text posts as one message (subreddit, author, title and body, cut to Telegram's 4096-character limit) and link posts as the title and link (with the link's preview image when it downloads); a media caption is cut to Telegram's 1024-character caption limit (the title first, the link only when it alone is too long), never inside an HTML entity → direct JSON API only when proxies are configured (`ENABLE_PROXIES` and `YTDLP_PROXIES`; Reddit blocks its `.json` endpoints even after the JS challenge is solved) → yt-dlp → vxreddit fixed link. `redd.it/<id>` short links are handled as `reddit.com/comments/<id>`. Media named by the vxreddit page is downloaded only over https from Reddit's media hosts (`i.redd.it`, `preview.redd.it`, `external-preview.redd.it`, `v.redd.it`) and the vxreddit host (plus vxreddit's video renderer, only as a redirect from vxreddit), redirects included; any other URL is refused and left to the next method
  - **TikTok**: direct scraping → slideshow API → tikwm.com → kktiktok redirector → headless browser; handles both videos and photo slideshows
  - **Twitter/X**: hybrid extraction — full tweet text (with author display name and recursive quote resolution) via FxTwitter + media via yt-dlp
  - **YouTube**: Shorts and community posts only (regular videos are ignored). Shorts: yt-dlp (HLS merging via ffmpeg, EJS challenge solver) → a paid Apify downloader when YouTube walls the server IP. With the optional home relay on for YouTube (`RELAY_*`, see below) the chain is the relay (yt-dlp from a residential IP) → Apify, and the server's own yt-dlp is skipped. Posts: text and images read from the post page
  - **Facebook**: photo links (`/photo/?fbid=…`, `photo.php`, `/<page>/photos/…`) through the facebed embed service only (yt-dlp can't fetch Facebook photos; Facebook walls datacenter IPs); posts (`/<page>/posts/…`, `permalink.php`, `story.php`, group posts, `/share/p/…`, `/share/<code>`) through facebed first, then yt-dlp, then the video facebed found on the post (the fbcdn MP4, up to 50 MB; yt-dlp fails on most `/posts/` links); videos, reels, watch and `/share/r|v/` links through yt-dlp first, then facebed (its video too). facebed gives up to 4 photos (downloaded in parallel) with the author and text; a public text-only post comes back as one text message with the author, the text and the original link (facebed cuts the text at 1024 characters, so a cut text ends with "…"; a link-share post loses its link preview); a photo or video link answered without media, a post with neither media nor text and an answer about another post than the link names are dropped. `FACEBED_HOST` (default `www.facebed.com`) sets the service; an hourly probe (page plus photo download) alerts the admin when it breaks
  - **9GAG**: yt-dlp for videos and GIFs (with the "😂 <title>" caption) → the 9GAG API helper (`scripts/9gag_fetch.py`) for photos and articles: an article comes as one text message (title and text, links kept) followed by its photos and videos as albums (up to 20, four downloaded at once within the link's 90 s deadline; a file over 50 MB is skipped). Files are downloaded only over https from 9GAG's CDN (`*.9cache.com`, e.g. `img-9gag-fun.9cache.com`), redirects included. `m.9gag.com` links are handled as `9gag.com`
  - **Instagram stories**: a paid Apify stories scraper, then HikerAPI as the backup when configured (stories need a login)
  - **Threads**: Meta's server-rendered post embed (text, photos, videos and mixed carousels in order) → vxthreads mirror → vxthreads fixed link
- **Paid fallbacks with a hard budget** — where free methods are blocked from datacenter IPs (author-gated Instagram posts, Instagram stories, YouTube Shorts), Apify actors take over, with HikerAPI as an optional backup for gated posts and stories (`INSTAGRAM_PAID_PROVIDERS`, default `apify,hiker`; off unless `HIKERAPI_TOKEN` is set). Every Apify run is capped, and before each run the bot reads the account's real monthly spend and refuses runs that could exceed `APIFY_MONTHLY_BUDGET_USD`; runs in flight are counted too. HikerAPI requests are recorded in a local ledger (`paid_requests`) and capped by `HIKERAPI_MONTHLY_BUDGET_USD`; a low prepaid balance alerts the admin. Gated posts also have monthly quotas per chat/user, reserved atomically in PostgreSQL
- **Concurrent processing** — every Telegram update and every link within a message is processed in its own goroutine
- **Health monitoring** — hourly probes of third-party redirectors and proxies, plus failure tracking of the paid and page-reading extractors; consecutive failures trigger Telegram alerts to the admin, with recovery notices
- **Response validation** — third-party redirector responses are verified as real CDN media (content type, size, redirect target) so a broken service fails loudly instead of sending garbage
- **Media conversion** — automatic WebM→MP4 conversion for Telegram compatibility; GIFs are sent as animations (never as a still photo) under a `.gif` / `.mp4` name matching the file's content, and a GIF over Telegram's 50 MB upload limit is converted to MP4 first (re-encoded at most 480 px wide when the first MP4 is still over the limit; a GIF no MP4 fits is left out). yt-dlp's metadata files (`.info.json`, `.description`) and unused thumbnails are deleted right after each download (only the download's own files, so two downloads of the same link at once never touch each other's)
- **Photo limits on every platform** — before sending, a photo over Telegram's 10 MB photo limit or with width + height over 10000 px is shrunk (at most 2560 px on its longest side); one whose sides are over 20:1 (a tall 9GAG post, a long comic strip), or one that can't be shrunk enough, is sent as a file at full size with the post caption instead of an unreadable strip
- **Document fallback** — a file Telegram refuses as a photo, video or GIF is sent again as a file named and labelled by what it holds (photo, video, GIF); it counts in the stats only when Telegram takes it
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

Requirement: **Group Privacy disabled in BotFather** (`/mybots` → Bot Settings → Group Privacy → Turn off; groups the bot is already in may need it removed and added again). Only then does the bot see every message in groups without admin rights; with Group Privacy on, Telegram gives it only commands, replies to its messages and mentions, unless it is an admin. At startup the bot reads this from `getMe` (`can_read_all_group_messages`): while Group Privacy is on, it logs a warning and tells `ADMIN_USER_ID`, and `/start` and `/info` say "I don't need admin rights" only when the check passed.

Admin rights matter for one more thing: a message sent over a daily limit is deleted only when the bot is an admin with the "Delete messages" right; without it the limit still applies (the bot answers with the limit notice instead of the media) and the message stays. Removing a limit (`/setlimit <user> 0`, `/setgrouplimit 0`, the Group Settings remove button) confirms the limit that applies afterwards (a removed individual limit falls back to the group limit, a removed group limit leaves `DEFAULT_DAILY_MEDIA_LIMIT` for users never given an individual limit, and no limit for users whose individual limit was removed). The confirmations of `/setlimit`, `/setgrouplimit` and the Group Settings limit buttons add a note about the missing right only when a limit is still enforced after the change (for a group change: the default or group limit, or any individual limit above 0 in that chat; nothing without a database) and the bot lacks the right; they check the right afresh each time, so promoting the bot shows at once.

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
| `/adminstats [period]` | Global stats: chats, media counts, platform breakdown, top chats, plus "Paid fetching": restricted-post fetches this month (vs the bot-wide cap, none by default), Apify spend vs budget, HikerAPI spend this month and balance (when configured), Stars support totals (month / all time, refunds excluded) and unused bonus downloads |
| `/broadcast` | Announcements with test/preview targeting (users vs groups) |
| `/gated` | Paid fetch of author-gated Instagram posts: status, `on`/`off` per group, monthly `cap` per group and per-`user` allowance (`N`, `unlimited`, `default`) |
| Blog system | Publish/edit/toggle posts shown to users (posts can also be written in the [owner dashboard](#owner-dashboard); sending them stays here) |
| Report inbox | Review `/report` submissions and `/reply` to reporters |
| `/starsrefund USER_ID CHARGE_ID` | Refund a Stars support payment in full and remove its unused bonus downloads (hidden; works even with Stars support off) |

## Architecture

```
main.go              → app.Run()
app/                 → polling loop, concurrent update processing, cleanup tasks
config/              → environment configuration; config/overrides holds the
                       owner-set settings the getters read before .env
model/settings/      → registry of the dashboard-managed settings (types,
                       ranges, hard ceilings, .env fallbacks, approval rules)
model/api/           → Telegram Bot API models (requests, payloads, responses)
services/            → one extractor per platform + shared infrastructure:
                       smart_downloader (strategy pattern, method fallbacks),
                       ytdlp_downloader, tiktok_extractor, twitter_extractor,
                       reddit_extractor + reddit_vxreddit_extractor + reddit_challenge,
                       instagram_* chain + instagram_gate (oEmbed gate check),
                       threads_extractor, youtube_post_extractor,
                       apify_client (shared budget) + gated_post_provider,
                       hikerapi_client + paid_requests_store (backup provider, ledger) + gated_provider_chain,
                       youtube_apify_extractor, instagram_stories_apify_extractor,
                       relay_client + relay_health + youtube_relay_extractor (home relay),
                       gated_fetch + gated_fetch_store (quotas),
                       redirector_health, outage_tracker, notify_admin,
                       animation + media_prepare (pre-send step: GIFs,
                       photo limits, names by content),
                       video_converter, database, ...
services/botsettings/ → settings goroutine: loads, reloads, cache file,
                       bot_runtime_status, profile re-push, change-request
                       applier, owner change digest
app/media_album.go   → mixed photo/video albums
app/animation.go, app/document_fallback.go → animations in post order, documents
migrations/          → PostgreSQL migrations (stats, limits, chat settings,
                       blog posts, reports, gated-fetch quotas, paid requests,
                       bot settings)
bin/                 → deployment and server tooling
cmd/dashboard/       → owner dashboard binary (social-link-dashboard)
dashboard/           → dashboard HTTP server, JSON API and SQL; web/ is the React app
```

The bot polls `getUpdates`, detects social links via regex, picks an extraction strategy per platform/URL type, walks the method chain until one succeeds, and sends the result via `sendPhoto`/`sendVideo`/`sendAnimation`/`sendMediaGroup`/`sendDocument`. Temporary files are cleaned immediately after sending, plus an hourly sweep for stragglers.

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
| `FACEBED_HOST` | Facebook photo, video and text post embed service (default `www.facebed.com`) |
| `APIFY_TOKEN`, `GATED_FETCH_*` | Paid fetch of author-gated Instagram posts and its monthly quotas (see `.env.example`, owner command `/gated`) |
| `APIFY_MONTHLY_BUDGET_USD`, `APIFY_YOUTUBE_ACTOR`, `APIFY_STORIES_ACTOR` | Shared monthly Apify spend limit and the YouTube / Instagram stories actors |
| `HIKERAPI_TOKEN`, `HIKERAPI_MONTHLY_BUDGET_USD`, `HIKERAPI_PRICE_PER_REQUEST_USD`, `HIKERAPI_LOW_BALANCE_REQUESTS`, `INSTAGRAM_PAID_PROVIDERS` | Optional HikerAPI backup for gated posts and stories (off without a token), its monthly cap (default $1), price per request for the ledger, low-balance alert threshold, and the provider order (stories use exactly the listed providers) |
| `KKINSTAGRAM_HOST`, `KKTIKTOK_HOST`, `VXREDDIT_HOST` | Override third-party fallback hosts |
| `RELAY_URL`, `RELAY_TOKEN_FILE`, `RELAY_PLATFORMS`, `RELAY_TIMEOUT_SECONDS` | Optional home relay: a loopback HTTP endpoint (e.g. `http://127.0.0.1:8791`, an SSH tunnel to a home machine; any non-loopback URL turns it off) that downloads videos from a residential IP. Off unless all of URL, token file and platforms are set; `RELAY_PLATFORMS` lists the platforms sent to it (only `youtube` has a relay method so far; default none). One call takes at most `RELAY_TIMEOUT_SECONDS` (default 45, values below 15 are raised to 15, the shortest call the relay accepts) and always leaves the next method 35 s. Every relay error falls through to the next method; relay failures and an hourly `/healthz` probe (3 misses, open breaker included) alert the admin |
| `DEFAULT_DAILY_MEDIA_LIMIT` | Default per-user daily limit (0 = unlimited) |
| `STARS_SUPPORT_ENABLED` | Optional "support the developer" payments in Telegram Stars (tier buttons on `/donate`, alias `/support`; `/paysupport`, `/terms`); default `false` |

See `.env.example` for the full annotated list.

**Dashboard-managed settings.** The platform and proxy switches, `INSTAGRAM_SERVICE`, the free-service hosts (`KKINSTAGRAM_HOST`, `KKTIKTOK_HOST`, `VXREDDIT_HOST`, `FACEBED_HOST`), the Apify and HikerAPI budgets, `HIKERAPI_PRICE_PER_REQUEST_USD`, `HIKERAPI_LOW_BALANCE_REQUESTS`, `INSTAGRAM_PAID_PROVIDERS`, the `GATED_FETCH_*` quotas, `STARS_SUPPORT_ENABLED` and `DEFAULT_DAILY_MEDIA_LIMIT` may be overridden by a value the owner sets, stored in the database (`bot_settings`, migration 011). The bot uses that value first, then the `.env` one, then the default; with no stored values (the state after the migration) every setting has the value it had before. The platform lists of the bot's profile and `/start` / `/info` now leave out a platform switched off in `.env`. The dashboard's Settings tab only queues a change; the bot validates and applies it within about 2 s, audits it and tells the owner in Telegram (one digest message at most every 5 minutes, changes made with `psql` included). Changes that need the owner's approval in Telegram are refused until that approval exists. Secrets, `ADMIN_USER_ID`, the database and relay settings and the Apify actors stay in `.env` only. If the configured database is down at start, the bot uses the last values it cached (`var/settings-cache.json`) and alerts the owner; without any `DB_*` variable set the stored values play no part. See [docs/settings.md](docs/settings.md).

## Owner dashboard

A separate statistics page for the bot's owner, served by its own binary (`social-link-dashboard`) from the bot's database: downloads per day and platform, platform health (drops and stale platforms), top groups and users, retention, chats with custom settings, restricted posts, Stars payments and bonus credits, and bug reports. Its Blog tab creates, edits, publishes and deletes the posts the owner panel broadcasts. Broadcasting itself stays in the Telegram owner panel (`/owner` → 📢 Broadcast, which sends only the content, not the title): the dashboard has no bot token and sends nothing to Telegram.

It only reads, except the Blog tab, the Reports tab's replies and resolving, and the Settings tab, which queues change requests the bot validates and applies (`POST /api/settings/requests`): those endpoints accept POST/PUT/DELETE, everything else only GET/HEAD, and every other query runs in a read-only transaction. The database role needs SELECT on the tables it reads and the write grants listed in [docs/dashboard.md](docs/dashboard.md) (`blog_posts`, `report_replies`, `reports (resolved, resolved_at)`, and `INSERT (kind, key, value, expected)` on `settings_change_requests`) (keep `default_transaction_read_only=on`; the writes open their own read-write transaction). Writes must carry `X-Dashboard-Request: 1`, an `Origin` equal to the dashboard's own origin and, with a body, `Content-Type: application/json` (otherwise `403`; bodies up to 64 KiB, unknown fields refused); no response carries CORS headers, and requests with a `Host` outside `DASHBOARD_ALLOWED_HOSTS` are refused (DNS rebinding). It has no login: it binds to loopback by default and is meant to be reached over a private network (VPN) or through a reverse proxy that does the access control (pass `Host`, set `X-Forwarded-Proto`), never directly from the internet.

```bash
make dashboard       # npm ci + npm run build in dashboard/web (the React app is embedded), then go build ./cmd/dashboard
make dashboard-dev   # build, then run ./social-link-dashboard locally
```

Without the frontend build the binary still compiles, but every page answers 503.

| Variable | Default | Purpose |
|---|---|---|
| `DASHBOARD_ADDR` | `127.0.0.1:8090` | Listen address; a non-loopback host is refused; `unix:/absolute/path.sock` listens on a unix socket (mode 0660, for a reverse proxy in the socket's group) and needs `DASHBOARD_ALLOWED_HOSTS` |
| `DASHBOARD_ALLOW_PUBLIC` | unset | `true` allows a non-loopback `DASHBOARD_ADDR` |
| `DASHBOARD_ALLOWED_HOSTS` | `localhost`, `127.0.0.1`, `[::1]` on the listen port, plus `DASHBOARD_ADDR` | Comma-separated `Host` values (`host:port`) the dashboard answers; set it for a host name or a reverse proxy |
| `DASHBOARD_DATABASE_URL` | `postgres:///social_link_bot?host=/var/run/postgresql&sslmode=disable` | PostgreSQL URL (peer auth over the local socket); the role needs SELECT plus the write grants above |
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
