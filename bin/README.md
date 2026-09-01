# Deployment & Server Tooling

Scripts for deploying and operating the Social Link Telegram Bot.

## Scripts

| Script | Purpose |
|---|---|
| `deploy` | Build locally, upload, and restart the service on the server |
| `deploy.conf.sample` | Template for deployment settings — copy to `deploy.conf` (gitignored) |
| `manage-bot` | Server-side management: `status` / `logs` / `follow` / `restart` |
| `extract-firefox-cookies` | Extract platform cookies from the server's Firefox profile |
| `quick-vnc-setup` | Set up a VNC desktop on the server for cookie extraction |
| `fix-vnc-fonts` | Fix font rendering in the VNC session |
| `install-chrome` | Install Chrome/Chromium for headless-browser extraction |
| `install-ffmpeg` | Install a static ffmpeg build without system dependencies |

## Deploying

```bash
cp bin/deploy.conf.sample bin/deploy.conf
# edit deploy.conf: SERVER_IP, SERVER_USER, SERVER_SSH_KEY, REMOTE_DIR, SERVICE_NAME

./bin/deploy                 # full deploy: build, upload, restart
./bin/deploy --restart-only  # restart without rebuilding
```

## Server Management

```bash
ssh -i ~/.ssh/your_deploy_key deploy-user@your-server

# On the server:
cd /path/to/bot
./manage-bot status    # Check if the bot is running
./manage-bot logs      # Show recent logs
./manage-bot follow    # Follow logs in real-time
./manage-bot restart   # Restart the bot
```

## One-time Server Setup

1. **Create a dedicated service user** on the server and generate an SSH keypair **locally**; add the public key to that user's `~/.ssh/authorized_keys`. Never copy private keys off the server or into chats/repos.

2. **Allow the service user to manage its service:**
   ```bash
   echo 'deploy-user ALL=(ALL) NOPASSWD: /usr/bin/systemctl * social-link-bot*' | sudo tee /etc/sudoers.d/social-link-bot
   sudo chmod 440 /etc/sudoers.d/social-link-bot
   ```

3. **Enable the systemd service:**
   ```bash
   sudo systemctl enable social-link-bot
   ```

## Cookie Extraction (server-IP authenticated)

Many platforms bind cookies to the IP that created them, so extract cookies from the server itself:

```bash
./bin/quick-vnc-setup                      # 1. VNC desktop on the server
ssh -L 5901:localhost:5901 user@server     # 2. Tunnel from your machine
# 3. Connect a VNC client to localhost:5901, log in to platforms in Firefox
./bin/extract-firefox-cookies              # 4. Extract all platforms
./bin/extract-firefox-cookies youtube      #    ...or a single platform
```

Cookie files are written to `cookies/<platform>_cookies.txt` and are gitignored.
