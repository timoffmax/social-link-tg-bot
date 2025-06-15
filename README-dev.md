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

# Server management (as tgbot user)
ssh -i ~/.ssh/your_deploy_key botuser@YOUR_SERVER_IP
./manage-bot restart
./manage-bot status
./manage-bot logs

# Service management commands (tgbot user has sudo access)
sudo systemctl start social-link-bot
sudo systemctl stop social-link-bot
sudo systemctl restart social-link-bot
sudo systemctl status social-link-bot
```

## Helper Scripts

```bash
# Manual bot management (for debugging)
ssh -i ~/.ssh/your_deploy_key botuser@YOUR_SERVER_IP
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

## Anti-Blocking Configuration

To improve download success rates:

### 1. **Cookies (Recommended)**
```bash
# Copy cookies template
cp cookies.txt.sample cookies.txt

# Export cookies from browser using browser extension
# Or manually extract from browser dev tools
# Edit cookies.txt with real session cookies
```

### 2. **Proxy Support**
```bash
# Set proxy in environment
export YTDLP_PROXY="socks5://proxy-server:port"
# or
export YTDLP_PROXY="http://proxy-server:port"
```

### 3. **Built-in Anti-Blocking**
- Platform-specific mobile user agents
- Request delays and retry logic  
- Geographic bypass (US)
- Enhanced headers and referers

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