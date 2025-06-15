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

## Production Service Management

The bot runs as a systemd service on the production server:

```bash
# Deploy code and build
make deploy

# Restart the service (on server)
ssh YOUR_USER@YOUR_SERVER_IP
sudo systemctl restart tg-social-link-bot
sudo systemctl status tg-social-link-bot

# Check service logs
sudo journalctl -u tg-social-link-bot -f

# Service management commands
sudo systemctl start tg-social-link-bot
sudo systemctl stop tg-social-link-bot
sudo systemctl restart tg-social-link-bot
sudo systemctl status tg-social-link-bot
```

## Helper Scripts

```bash
# Restart service helper
./bin/restart-service

# Manual bot management (for debugging)
ssh YOUR_USER@YOUR_SERVER_IP
cd /var/www/social-link-tg-bot
./manage-bot status
./manage-bot logs
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