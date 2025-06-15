# Social Link Telegram Bot

Downloads and sends actual media files from social media platforms instead of just providing fixed links.

## Supported Platforms
- Instagram (photos, videos, carousels, stories)
- TikTok (videos, photo slideshows)
- Twitter/X (photos, videos)
- 9GAG (videos, images)
- YouTube Shorts

## Quick Start

### Local Development
```bash
cp .env.example .env
# Edit .env with your bot token
go mod download
go build -o social-link-bot .
./social-link-bot
```

### Deploy to Production
```bash
./bin/deploy
```

### Server Management
```bash
# SSH into server
ssh -i ~/.ssh/your_deploy_key botuser@YOUR_SERVER_IP

# Navigate to bot directory and manage
cd /var/www/social-link-tg-bot
./manage-bot status    # Check status
./manage-bot logs      # Show logs  
./manage-bot restart   # Restart bot
```

## Documentation

- [Development Guide](README-dev.md) - Local development and testing
- [Deployment Guide](bin/README.md) - Production deployment
