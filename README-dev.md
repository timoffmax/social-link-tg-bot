# Local Development

## Quick Start

1. **Set up the environment:**
   ```bash
   make setup          # creates .env from .env.example
   ```

2. **Set your bot token** in `.env`:
   ```bash
   TG_BOT_TOKEN="your-test-bot-token"
   ```
   Create a separate test bot via [@BotFather](https://t.me/BotFather) so you don't disturb the production bot.

3. **Run the bot:**
   ```bash
   make run
   ```

## Development Commands

```bash
make build     # Build the binary
make run       # Build and run
make dev       # Run with auto-restart (requires entr)
make test      # Run tests
make deploy    # Deploy to production (needs bin/deploy.conf)
make status    # Production service status (needs bin/deploy.conf)
make logs      # Tail production logs (needs bin/deploy.conf)
make help      # List all targets
```

## Dependencies

- **Go 1.21+**
- **yt-dlp** — media downloading (`pip3 install --user yt-dlp`)
- **ffmpeg** — YouTube HLS merging and WebM→MP4 conversion (`./bin/install-ffmpeg` for a static build, or `sudo apt install ffmpeg`)
- **PostgreSQL** — optional; enables stats, limits, blog, and reports (migrations in `migrations/` run automatically)

## Anti-Blocking Configuration

### Cookies (recommended)

Export cookies from a logged-in browser session into Netscape format files named `<platform>_cookies.txt` (see `*_cookies.txt.example` templates). For production, cookies must be extracted from the server's own IP — see the Cookies section in [README.md](README.md).

### Proxies (optional fallback)

```bash
# Comma-separated list; used as a fallback for non-YouTube platforms
YTDLP_PROXIES="socks5://host:port,http://host:port"
ENABLE_PROXIES="true"   # global kill switch
```

### Built-in measures

- Platform-specific mobile user agents, request delays, and retry logic
- Multi-method extraction chains per platform (a blocked method falls through to the next)

## Project Structure

```
├── main.go          # Entry point
├── app/             # Polling loop, update processing, cleanup tasks
├── config/          # Environment configuration
├── model/api/       # Telegram Bot API models and request/response types
├── services/        # Platform extractors, smart downloader, health checks, DB
├── migrations/      # PostgreSQL schema migrations
├── scripts/         # Auxiliary helper scripts
├── bin/             # Deployment and server tooling
├── cookies/         # Cookie files (gitignored)
├── .env             # Local environment (gitignored)
└── Makefile         # Development commands
```

## Troubleshooting

**Bot not responding**
- Check `.env` has the correct token
- Verify the process is running: `ps aux | grep social-link-bot`
- Check log output for Telegram API errors

**Downloads not working**
- Verify yt-dlp is installed and on PATH: `yt-dlp --version`
- Install ffmpeg for YouTube (HLS merging requires it)
- Try the link manually: `yt-dlp --dump-json <url>`

**A single platform failing**
- Platforms depend on third-party fallback services; check the hourly health-monitor alerts (sent to `ADMIN_USER_ID`)
- Refresh that platform's cookies if logs show authentication errors
