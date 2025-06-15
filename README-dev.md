# Local Development Setup

## Quick Start

1. **Set up the environment:**
   ```bash
   make setup
   ```

2. **Edit your bot token:**
   ```bash
   nano .env
   # Set TG_BOT_TOKEN to your test bot token
   ```

3. **Run the bot:**
   ```bash
   make run
   ```

## Development Commands

```bash
# Build the application
make build

# Run with auto-restart (requires entr)
make dev

# Run tests
make test

# Deploy to production
make deploy

# Check production status
make status

# View production logs
make logs

# Show all commands
make help
```

## Testing Bot Locally

1. **Create a test bot** via @BotFather on Telegram
2. **Set the token** in `.env`:
   ```bash
   TG_BOT_TOKEN="your-test-bot-token"
   ```
3. **Start the bot** and test with Instagram/TikTok/Twitter links

## Dependencies

The bot requires:
- **Go 1.21+** (available at ~/go/bin/go)
- **yt-dlp** for media downloading
- **ffmpeg** for video processing (optional, enables better quality)

## Project Structure

```
├── app/           # Main application logic
├── config/        # Configuration management
├── model/         # Telegram API models
├── services/      # Core services (yt-dlp downloader)
├── bin/           # Deployment scripts
├── .env           # Local environment (not in git)
└── Makefile       # Development commands
```

## Troubleshooting

### Bot not responding
- Check `.env` file has correct token
- Verify bot is running: `ps aux | grep social-link-bot`
- Check logs for errors

### Downloads not working
- Install yt-dlp: `pip3 install --user yt-dlp`
- Install ffmpeg: `sudo apt install ffmpeg`
- Check if `bin/yt-dlp` symlink exists

### Permission errors
- Ensure `make build` completes successfully
- Check file permissions: `chmod +x social-link-bot`