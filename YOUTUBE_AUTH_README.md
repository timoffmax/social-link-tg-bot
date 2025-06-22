# YouTube Authentication Setup Guide

⚠️ **CRITICAL**: YouTube has disabled username/password and OAuth2 authentication as of 2025. Only cookie-based authentication works.

## Quick Setup

```bash
./bin/refresh-youtube-cookies
```

## Manual Setup (Recommended)

### Step 1: Prepare Browser
1. Open a **private/incognito** browser window
2. Go to https://www.youtube.com and log in
3. Navigate to https://www.youtube.com/robots.txt
4. Keep this tab open

### Step 2: Install Cookie Extension
- **Chrome**: [Get cookies.txt LOCALLY](https://chrome.google.com/webstore/detail/get-cookiestxt-locally/cclelndahbckbenkjhflpdbgdldlbecc)
- **Firefox**: [cookies.txt](https://addons.mozilla.org/en-US/firefox/addon/cookies-txt/)

### Step 3: Export Cookies
1. Click the extension icon on the robots.txt tab
2. Export cookies and save as `youtube_cookies.txt` in bot directory
3. **Immediately close** the private browsing window

### Step 4: Test
```bash
yt-dlp --cookies youtube_cookies.txt --dump-json --no-download "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
```

## Configuration Options

### Rate Limiting (Recommended)
```bash
echo "YOUTUBE_DELAY=5" >> .env
```

### IP Consistency (Optional)
```bash
echo "SOURCE_ADDRESS=your.server.ip" >> .env
```

## Current Status

| Method | Status | Notes |
|--------|--------|-------|
| Username/Password | ❌ Disabled | YouTube blocked this method |
| OAuth2 | ❌ Disabled | YouTube blocked this method |
| Browser Cookies | ✅ Working | Only working method |
| Rate Limiting | ✅ Required | Prevents blocking |

## Bot Features

- **Multi-strategy cookie detection**: Browser extraction → Cookie file → Fallbacks
- **SOCKS5 proxy rotation**: 9 working proxies with intelligent failure tracking
- **Rate limiting**: 5-second default delay between requests
- **Enhanced error detection**: YouTube bot detection and CAPTCHA warnings
- **Account protection**: Warnings about ban risks and best practices

## Troubleshooting

### "Sign in required" Error
- Cookies have expired - refresh them
- Try the manual incognito method
- Use a different YouTube account

### Proxy Issues
The bot automatically rotates through 9 SOCKS5 proxies:
- PROXY_IP_1:6712 (Buffalo, US)
- PROXY_IP_2:6543 (Bloomingdale, US)
- PROXY_IP_3:6349 (Buffalo, US)
- And 6 more...

### Account Ban Prevention
- Use a throwaway YouTube account
- Keep `YOUTUBE_DELAY=5` or higher
- Refresh cookies weekly
- Monitor logs for warning signs

## Log Monitoring

Watch for these warning patterns:
```
🤖 YouTube bot detection or rate limit
🔌 Connection error with proxy
⚠️ No YouTube cookies available
```

## Best Practices

1. **Use throwaway accounts** - Never use your main YouTube account
2. **Regular cookie refresh** - Weekly or when errors occur
3. **Monitor rate limits** - Watch logs for blocking signs
4. **Respect YouTube's ToS** - Use responsibly
5. **Backup working configs** - Save working cookie/proxy combinations

---

**Remember**: YouTube actively fights bot access. This setup maximizes success while minimizing ban risk, but no method is 100% guaranteed.