# Deployment & Server Tooling

Scripts for deploying and operating the Social Link Telegram Bot.

## Scripts

| Script | Purpose |
|---|---|
| `deploy` | Pull the latest code on the server, build it there, and restart the service (and the optional owner dashboard) |
| `deploy.conf.sample` | Template for deployment settings — copy to `deploy.conf` (gitignored) |
| `install-chrome` | Install Chrome/Chromium for headless-browser extraction |
| `install-ffmpeg` | Install a static ffmpeg build without system dependencies |

## Deploying

```bash
cp bin/deploy.conf.sample bin/deploy.conf
# edit deploy.conf: SERVER_IP, SERVER_USER, SERVER_SSH_KEY, REMOTE_DIR, SERVICE_NAME
# optional: DASHBOARD_SERVICE_NAME to deploy the owner dashboard too

./bin/deploy                   # full deploy: git pull, build on the server, restart
./bin/deploy --restart-only    # restart without pulling or rebuilding
./bin/deploy --dashboard-only  # git pull, then build and restart only the dashboard
```

`bin/deploy` builds nothing locally: over SSH it runs `git pull origin master` in `REMOTE_DIR`, builds with `/usr/local/go/bin/go build` there (avoids GLIBC mismatches), restarts `SERVICE_NAME` with `sudo systemctl`, then prints the service status and the last log lines. Push your commits first.

**Dashboard step (optional).** Only when `deploy.conf` sets `DASHBOARD_SERVICE_NAME`: after the bot is built and restarted it runs `npm ci && npm run build` in `dashboard/web` (the React app is embedded into the binary, so the server needs Node 22+), builds `social-link-dashboard` from `./cmd/dashboard`, restarts `DASHBOARD_SERVICE_NAME` and prints its status. A failed dashboard step leaves the bot deploy as it is, is reported as an error and makes the script exit with 1 at the end. `--dashboard-only` skips the bot's build and restart; before pulling it checks whether master brings new files in `migrations/` (only the bot applies them, on restart) and, if so, stops without pulling and asks for the full deploy first. `--restart-only` restarts both services. Without `DASHBOARD_SERVICE_NAME`, `--dashboard-only` stops with an error. See [docs/dashboard.md](../docs/dashboard.md).

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

5. **Optional owner dashboard:** install Node 22+, create a systemd service that runs `REMOTE_DIR/social-link-dashboard` with its `DASHBOARD_*` environment (see [docs/dashboard.md](../docs/dashboard.md)), allow the service user to restart it in the sudoers rule above, and set `DASHBOARD_SERVICE_NAME` in `deploy.conf`. Give the dashboard its own database role: `SELECT` on the tables it reads, write grants on `blog_posts` only (`INSERT, UPDATE, DELETE`, plus `USAGE` on `blog_posts_id_seq`) and `default_transaction_read_only=on`. It has no login, so keep it on loopback and reach it over a private network or an access-controlled reverse proxy.
