# Deployment Scripts

Simple deployment for the Social Link Telegram Bot.

## Quick Start

### Deploy to Production
```bash
./bin/deploy
```

### Restart Only (no build/upload)
```bash
./bin/deploy --restart-only
```

## Files

- `deploy` - Main deployment script (builds, uploads, restarts)
- `deploy.conf` - Configuration (server IP, SSH key, paths)
- `manage-bot` - Server-side bot management script
- `yt-dlp` - yt-dlp binary for media downloads

## Server Management

SSH into the server and use the management script:

```bash
ssh -i ~/.ssh/your_deploy_key botuser@YOUR_SERVER_IP

# On the server:
cd /var/www/social-link-tg-bot
./manage-bot status    # Check if bot is running
./manage-bot logs      # Show recent logs
./manage-bot follow    # Follow logs in real-time
./manage-bot restart   # Restart the bot
```

## Configuration

The `deploy.conf` file contains the deployment settings:
- `SERVER_IP` - Production server IP
- `SERVER_USER` - SSH user (tgbot)
- `SERVER_SSH_KEY` - Path to SSH private key
- `REMOTE_DIR` - Bot directory on server
- `SERVICE_NAME` - systemd service name

## Requirements

- SSH key authentication configured for tgbot user
- Go installed locally (for building)
- systemd service configured on server
- tgbot user has sudo access for systemctl commands

## One-time Setup

If you need to set up a new server:

1. **Generate SSH key for tgbot user:**
   ```bash
   ssh YOUR_USER@YOUR_SERVER_IP
   sudo mkdir -p /home/botuser/.ssh
   sudo -u tgbot ssh-keygen -t rsa -f /home/botuser/.ssh/id_rsa -N ""
   # Generate an SSH keypair locally and add its public key to the deploy user
   ```

2. **Configure sudo for systemctl:**
   ```bash
   echo 'tgbot ALL=(ALL) NOPASSWD: /usr/bin/systemctl * social-link-bot*' | sudo tee /etc/sudoers.d/tgbot-social-link-bot
   sudo chmod 440 /etc/sudoers.d/tgbot-social-link-bot
   ```

3. **Create system service:**
   ```bash
   sudo systemctl enable social-link-bot
   ```

That's it! Simple and clean deployment.