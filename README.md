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
| `/donate` | Support development (USDT + NOWPayments) |
| `/report` | Report a bug or issue to the owner |

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
| `/adminstats [period]` | Global stats: chats, media counts, platform breakdown, top chats |
| `/broadcast` | Announcements with test/preview targeting (users vs groups) |
| `/gated` | Paid fetch of author-gated Instagram posts: status, `on`/`off` per group, monthly `cap` per group and per-`user` allowance (`N`, `unlimited`, `default`) |
| Blog system | Publish/edit/toggle posts shown to users |
| Report inbox | Review `/report` submissions and `/reply` to reporters |

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

See `.env.example` for the full annotated list.

## Deployment

```bash
cp bin/deploy.conf.sample bin/deploy.conf   # set SERVER_IP, SERVER_USER, SERVER_SSH_KEY, REMOTE_DIR, SERVICE_NAME
./bin/deploy                                # git pull and build on the server, then restart
./bin/deploy --restart-only                 # restart without pulling or rebuilding
```

`make status` and `make logs` read the same `bin/deploy.conf`. See [bin/README.md](bin/README.md) for the full deployment and one-time server setup guide.

## Development

See [README-dev.md](README-dev.md) for local development, testing with a BotFather test bot, and the project structure.

## License

Copyright (C) 2024-2026 Tymofii Maksymenko.

Licensed under the [GNU Affero General Public License v3.0](LICENSE), version 3 only (`SPDX-License-Identifier: AGPL-3.0-only`); the "any later version" option in the license's how-to-apply appendix is not used. You may use, modify and self-host the bot; if you run a modified version as a service for others, you must make your modified source available to its users under the same license.

Versions up to and including commit `6586ac6` were released under the MIT License and remain available under it.
