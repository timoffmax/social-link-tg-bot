package config

import (
	"github.com/joho/godotenv"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"
)

const tgBotTokenKey = "TG_BOT_TOKEN"

func init() {
	loadConfig()
}

func loadConfig() {
	godotenv.Load()
}

func GetTgBotToken() string {
	return os.Getenv(tgBotTokenKey)
}

// Social media authentication configuration
func GetInstagramUsername() string {
	return os.Getenv("INSTAGRAM_USERNAME")
}

func GetInstagramPassword() string {
	return os.Getenv("INSTAGRAM_PASSWORD")
}

func GetTikTokUsername() string {
	return os.Getenv("TIKTOK_USERNAME")
}

func GetTikTokPassword() string {
	return os.Getenv("TIKTOK_PASSWORD")
}

func GetTwitterUsername() string {
	return os.Getenv("TWITTER_USERNAME")
}

func GetTwitterPassword() string {
	return os.Getenv("TWITTER_PASSWORD")
}

// 9GAG authentication configuration
func GetNineGagUsername() string {
	return os.Getenv("NINEGAG_USERNAME")
}

func GetNineGagPassword() string {
	return os.Getenv("NINEGAG_PASSWORD")
}

// Proxy configuration
func GetYtDlpProxy() string {
	return os.Getenv("YTDLP_PROXY")
}

// Get list of proxies from YTDLP_PROXIES environment variable
// Format: proxy1,proxy2,proxy3 (comma-separated)
func GetYtDlpProxies() []string {
	proxiesStr := os.Getenv("YTDLP_PROXIES")
	if proxiesStr == "" {
		// Fallback to single proxy if available
		if proxy := GetYtDlpProxy(); proxy != "" {
			return []string{proxy}
		}
		return []string{}
	}
	
	// Split by comma and trim spaces
	var proxies []string
	for _, proxy := range strings.Split(proxiesStr, ",") {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	
	return proxies
}

// Get list of free proxies from FREE_PROXIES environment variable
// Format: proxy1,proxy2,proxy3 (comma-separated)
func GetFreeProxies() []string {
	proxiesStr := os.Getenv("FREE_PROXIES")
	if proxiesStr == "" {
		return []string{}
	}
	
	// Split by comma and trim spaces
	var proxies []string
	for _, proxy := range strings.Split(proxiesStr, ",") {
		proxy = strings.TrimSpace(proxy)
		if proxy != "" {
			proxies = append(proxies, proxy)
		}
	}
	
	return proxies
}

// Get premium proxy (limited-free, highest priority)
func GetPremiumProxy() string {
	return os.Getenv("PREMIUM_PROXY")
}

// Get YouTube-specific proxy configuration
func GetYouTubeProxy() string {
	return os.Getenv("YOUTUBE_PROXY")
}

// Get source address for IP consistency (yt-dlp FAQ recommendation)
func GetSourceAddress() string {
	return os.Getenv("SOURCE_ADDRESS")
}

// Get YouTube download delay in seconds (rate limiting)
func GetYouTubeDelay() string {
	delay := os.Getenv("YOUTUBE_DELAY")
	if delay == "" {
		return "5" // Default 5 second delay
	}
	return delay
}

// Get prioritized list of proxies for YouTube
func GetYouTubeProxies() []string {
	var proxies []string
	
	// 1. Premium proxy (highest priority)
	if premiumProxy := GetPremiumProxy(); premiumProxy != "" {
		proxies = append(proxies, premiumProxy)
		// Log premium proxy (mask credentials)
		if strings.Contains(premiumProxy, "@") {
			parts := strings.Split(premiumProxy, "@")
			if len(parts) == 2 {
				// Show only the server part for security
				log.Printf("🔒 Premium proxy configured: ***@%s", parts[1])
			}
		}
	}
	
	// 2. YouTube-specific proxy
	if youtubeProxy := GetYouTubeProxy(); youtubeProxy != "" {
		proxies = append(proxies, youtubeProxy)
	}
	
	// 3. 2-3 random free proxies
	freeProxies := GetFreeProxies()
	if len(freeProxies) > 0 {
		// Shuffle the free proxies
		rand.Seed(time.Now().UnixNano())
		shuffled := make([]string, len(freeProxies))
		copy(shuffled, freeProxies)
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		
		// Add 2-3 random free proxies
		count := 2 + rand.Intn(2) // 2 or 3
		if count > len(shuffled) {
			count = len(shuffled)
		}
		
		proxies = append(proxies, shuffled[:count]...)
	}
	
	return proxies
}
