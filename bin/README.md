# Deployment & Server Tooling

Scripts for deploying and operating the Social Link Telegram Bot.

## Scripts

| Script | Purpose |
|---|---|
| `deploy` | Pull the latest code on the server, build it there, and restart the service |
| `deploy.conf.sample` | Template for deployment settings — copy to `deploy.conf` (gitignored) |
| `install-chrome` | Install Chrome/Chromium for headless-browser extraction |
| `install-ffmpeg` | Install a static ffmpeg build without system dependencies |

## Deploying

```bash
cp bin/deploy.conf.sample bin/deploy.conf
# edit deploy.conf: SERVER_IP, SERVER_USER, SERVER_SSH_KEY, REMOTE_DIR, SERVICE_NAME

./bin/deploy                 # full deploy: git pull, build on the server, restart
./bin/deploy --restart-only  # restart without pulling or rebuilding
```

`bin/deploy` builds nothing locally: over SSH it runs `git pull origin master` in `REMOTE_DIR`, builds with `/usr/local/go/bin/go build` there (avoids GLIBC mismatches), restarts `SERVICE_NAME` with `sudo systemctl`, then prints the service status and the last log lines. Push your commits first.

## Server Management

All targets read `bin/deploy.conf`:

```bash
make status                  # systemd status of the bot service
make logs                    # follow the bot log in real time
./bin/deploy --restart-only  # restart the bot
```

## One-time Server Setup

1. **Create a dedicated service user** on the server and generate an SSH keypair **locally**; add the public key to that user's `~/.ssh/authorized_keys`. Never copy private keys off the server or into chats/repos.

2. **Allow the service user to manage its service:**
   ```bash
   echo 'deploy-user ALL=(ALL) NOPASSWD: /usr/bin/systemctl * social-link-bot*' | sudo tee /etc/sudoers.d/social-link-bot
   sudo chmod 440 /etc/sudoers.d/social-link-bot
   ```

3. **Clone the repository into `REMOTE_DIR`** as the service user, install Go at `/usr/local/go`, and create the `.env` there.

4. **Enable the systemd service:**
   ```bash
   sudo systemctl enable social-link-bot
   ```
