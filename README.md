# Social Link Telegram Bot

A Telegram bot that downloads and sends actual media files from social media links instead of leaving broken previews. Drop an Instagram, TikTok, Twitter/X, YouTube, Reddit, Facebook, or 9GAG link into any chat — the bot fetches the real photos/videos and replies with them directly.

## Features

- **Real media, not links** — downloads photos, videos, galleries, slideshows, and stories (Instagram carousels: first item only), and sends them as native Telegram media
- **Layered extraction chains with automatic fallbacks** — every platform has multiple extraction methods tried in order, so a single dead third-party service never silently kills a platform:
  - **Instagram**: kkinstagram redirector → OG-preview fallback; posts gated by their author (e.g. age limits) get an explanation instead
  - **Reddit**: direct JSON API (with inline JS-challenge solving) → vxreddit → yt-dlp → headless browser; `/s/` share links resolved; text posts sent as formatted messages
  - **TikTok**: direct scraping → slideshow API → tikwm.com → kktiktok redirector → headless browser; handles both videos and photo slideshows
  - **Twitter/X**: hybrid extraction — full tweet text (with author display name and recursive quote resolution) via FxTwitter + media via yt-dlp
  - **YouTube**: HLS stream merging (separate audio+video) via ffmpeg, with an EJS challenge solver for nsig decryption
- **Concurrent processing** — every Telegram update and every link within a message is processed in its own goroutine
- **Health monitoring** — hourly probes of third-party redirectors and proxies; consecutive failures trigger Telegram alerts to the admin, with recovery notices
- **Response validation** — third-party redirector responses are verified as real CDN media (content type, size, redirect target) so a broken service fails loudly instead of sending garbage
- **Media conversion** — automatic WebM→MP4 conversion for Telegram compatibility
- **Fixed-link fallback** — if every extraction method fails, the bot falls back to fixed-link services (fixupx, vxreddit, etc.) so the user still gets a working preview

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
                       instagram_* chain, redirector_health, outage_tracker,
                       notify_admin, video_converter, database, ...
migrations/          → PostgreSQL migrations (stats, limits, chat settings,
                       blog posts, reports)
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
| `ENABLE_<PLATFORM>_COOKIES` | Per-platform cookie usage toggles |
| `ENABLE_PROXIES` / `YTDLP_PROXIES` | Global proxy kill switch + comma-separated proxy list (fallback only) |
| `KKINSTAGRAM_HOST`, `KKTIKTOK_HOST`, `VXREDDIT_HOST` | Override third-party fallback hosts |
| `DEFAULT_DAILY_MEDIA_LIMIT` | Default per-user daily limit (0 = unlimited) |

See `.env.example` for the full annotated list.

## Deployment

```bash
cp bin/deploy.conf.sample bin/deploy.conf   # set SERVER_IP, SERVER_USER, SERVER_SSH_KEY, paths
./bin/deploy                                # build, upload, restart
./bin/deploy --restart-only                 # restart without rebuilding
```

On the server, `bin/manage-bot` (uploaded by deploy) provides `status`/`logs`/`follow`/`restart`. `make logs` and `make status` read the same `bin/deploy.conf`. See [bin/README.md](bin/README.md) for the full deployment and one-time server setup guide.

### Cookies

Several platforms validate cookies against the IP that created them, so cookies must be extracted **from the server itself**: set up a throwaway desktop with `./bin/quick-vnc-setup`, log in to the platforms in Firefox over VNC, then run `./bin/extract-firefox-cookies [platform]`. Cookie files live in `cookies/<platform>_cookies.txt` and should be refreshed when authentication errors appear in the logs.

## Development

See [README-dev.md](README-dev.md) for local development, testing with a BotFather test bot, and the project structure.

## License

MIT — see [LICENSE](LICENSE).
