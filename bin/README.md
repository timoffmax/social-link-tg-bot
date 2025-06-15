# Deployment Scripts

This folder contains deployment and management scripts for the Social Link Telegram Bot.

## Setup

1. Copy the configuration template:
   ```bash
   cp deploy.conf.sample deploy.conf
   ```

2. Edit `deploy.conf` with your server details

## Usage

### Deploy to Production
```bash
./bin/deploy
```

### Other Options
```bash
# Preview changes without deploying
./bin/deploy --dry-run

# Just restart the service
./bin/deploy --restart-only

# Initial server setup
./bin/deploy --setup
```

## Configuration

The `deploy.conf` file contains all deployment settings:
- Server credentials and paths
- Service configuration
- Dependency management
- Optional notifications

## Requirements

- SSH access to the production server
- Go installed locally (for local builds)
- Python3 and pip on the server (for yt-dlp)
- sudo access on server (for system services and ffmpeg)