# Server IP Authentication Guide

YouTube validates cookies against the IP address where they were created. This guide shows how to authenticate directly from your server IP.

## 🎯 The Problem

- ✅ Cookies created locally work on your local IP
- ❌ Cookies created locally **DON'T work** on server IP  
- ✅ Cookies created on server work on server IP
- 📍 **Your server IP:** `YOUR_SERVER_IP`

## 🚀 Quick Solution: VNC Desktop

**Fastest way to get YouTube cookies from server IP:**

```bash
./bin/quick-vnc-setup
```

This installs VNC + Firefox and gives you connection details.

---

## 🖥️ Method 1: VNC Desktop (Recommended)

### Install & Start
```bash
./bin/quick-vnc-setup
```

### Connect to VNC
**Option A - SSH Tunnel (secure):**
```bash
# From your local machine:
ssh -L 5901:localhost:5901 YOUR_USER@YOUR_SERVER_IP

# Then connect VNC client to: localhost:5901
```

**Option B - Direct connection:**
```bash
# VNC client to: YOUR_SERVER_IP:5901
# (May need firewall configuration)
```

### Get Cookies
1. Firefox opens automatically in VNC
2. Go to https://www.youtube.com
3. Log in with Google account
4. Install "Get cookies.txt LOCALLY" extension
5. Export cookies → save as `youtube_cookies.txt`

### VNC Management
```bash
vncserver -kill :1     # Stop
vncserver :1           # Start  
vncserver -list        # Status
```

---

## 🔄 Method 2: X11 Forwarding

### Setup
```bash
sudo apt install -y xorg firefox-esr xauth
```

### Connect & Use
```bash
# Disconnect SSH, then reconnect with X11:
ssh -X YOUR_USER@YOUR_SERVER_IP

# Launch Firefox (opens on your local screen):
firefox

# Firefox runs on server IP but displays locally
# Log into YouTube and export cookies normally
```

### Troubleshooting X11
```bash
echo $DISPLAY          # Should show something like localhost:10.0
xeyes                   # Test - should show eyes on your screen
ssh -Y                  # Try -Y if -X doesn't work
```

---

## 🤖 Method 3: Automated Headless Browser

### Install ChromeDriver
```bash
./bin/install-chromedriver
```

### Run Automated Login
```bash
./bin/setup-server-browser
# Choose option 3 (headless automation)

# Or directly:
python3 auto_youtube_login.py
```

**Limitations:**
- Requires your Google credentials
- May trigger Google security checks
- Use throwaway account recommended
- 2FA can complicate the process

---

## 🌐 Method 4: Web-based VNC (NoVNC)

### Install & Start
```bash
./bin/setup-server-browser
# Choose option 4 (Browser-in-browser)
```

### Access
- Open browser to: `http://YOUR_SERVER_IP:6080/vnc.html`
- Click "Connect" 
- Use VNC password you set
- Firefox available in web-based desktop

---

## 📊 Method Comparison

| Method | Setup Time | Ease of Use | Reliability | Security |
|--------|------------|-------------|-------------|----------|
| VNC Desktop | 5 min | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ | ⭐⭐⭐⭐ |
| X11 Forward | 2 min | ⭐⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐⭐⭐ |
| Headless Auto | 10 min | ⭐⭐ | ⭐⭐⭐ | ⭐⭐ |
| Web VNC | 7 min | ⭐⭐⭐ | ⭐⭐⭐⭐ | ⭐⭐⭐ |

**Recommendation:** Use VNC Desktop for best balance of ease and reliability.

---

## 🔧 Firewall Configuration

If direct VNC connection fails, configure UFW:

```bash
# Allow VNC port
sudo ufw allow 5901/tcp

# Or for web VNC
sudo ufw allow 6080/tcp

# Check status
sudo ufw status
```

**Security Note:** Consider using SSH tunnels instead of opening ports.

---

## 🧪 Testing Cookies

After getting cookies from server IP:

```bash
# Test basic functionality
yt-dlp --cookies youtube_cookies.txt --dump-json --no-download "https://www.youtube.com/watch?v=dQw4w9WgXcQ"

# Test with your bot
cd /var/www/social-link-tg-bot
./social-link-bot

# Check bot logs for authentication success
tail -f logs/bot.log | grep -i youtube
```

**Success indicators:**
- ✅ No "Sign in required" errors
- ✅ Video metadata extracted successfully  
- ✅ Bot processes YouTube links without errors

---

## 🔄 Cookie Maintenance

### Regular Refresh
```bash
# Weekly refresh recommended
./bin/quick-vnc-setup      # Start VNC if not running
# Connect, refresh login, export new cookies
```

### Monitoring
```bash
# Watch for authentication failures
tail -f logs/bot.log | grep -E "(Sign in required|403|401|CAPTCHA)"

# Test cookie validity
yt-dlp --cookies youtube_cookies.txt --dump-json "https://www.youtube.com/watch?v=test" 2>&1 | grep -i error
```

### Automatic Alerts
Add to crontab for daily cookie validation:
```bash
# Add to crontab -e
0 9 * * * /var/www/social-link-tg-bot/bin/test-youtube-cookies.sh
```

---

## 📱 VNC Client Recommendations

### Windows
- **TightVNC Viewer** (free)
- **RealVNC Viewer** (free tier)  
- **UltraVNC** (free)

### macOS
- **Built-in Screen Sharing** (Finder → Go → Connect to Server → vnc://server:5901)
- **RealVNC Viewer**
- **Jump Desktop**

### Linux
- **Remmina** (Ubuntu/Debian: `sudo apt install remmina`)
- **TigerVNC** (`sudo apt install tigervnc-viewer`)
- **Command line:** `vncviewer server:5901`

### Android/iOS
- **RealVNC Viewer**
- **VNC Viewer** (Android)
- **Jump Desktop** (iOS)

---

## 🛡️ Security Best Practices

1. **Use SSH tunnels** instead of opening VNC ports
2. **Strong VNC passwords** - use random 12+ character passwords
3. **Throwaway Google accounts** - don't risk your main account
4. **Regular password rotation** - change VNC password monthly
5. **Monitor access logs** - check `/var/log/auth.log` for unauthorized access
6. **Firewall rules** - restrict VNC access to specific IPs if possible

---

## 🆘 Troubleshooting

### VNC Won't Start
```bash
# Check if port is in use
sudo netstat -tlnp | grep :5901

# Kill existing VNC
vncserver -kill :1

# Check logs
cat ~/.vnc/*.log

# Manual start with debug
vncserver :1 -geometry 1280x800 -verbose
```

### Firefox Won't Start in VNC
```bash
# Install missing packages
sudo apt install firefox-esr xfce4 xfce4-terminal

# Test Firefox directly
DISPLAY=:1 firefox &

# Check X11 is working
DISPLAY=:1 xeyes &
```

### Cookie Export Issues
```bash
# Verify cookie format
head -5 youtube_cookies.txt

# Should start with:
# # Netscape HTTP Cookie File

# Check file permissions
ls -la youtube_cookies.txt
chmod 644 youtube_cookies.txt
```

### Authentication Still Fails
```bash
# Verify server IP matches cookie IP
curl ifconfig.me

# Clear browser data and re-login
# Delete ~/.mozilla and restart Firefox in VNC

# Try incognito/private browsing mode
# Export cookies from private session
```

---

**Result:** You'll have YouTube cookies created from your server IP (`YOUR_SERVER_IP`) that work perfectly with your bot!