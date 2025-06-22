package config

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"
)

// ProxyConfig represents a single proxy configuration
type ProxyConfig struct {
	URL      string
	Type     string // "socks5", "http", "https"
	Host     string
	Port     string
	Username string
	Password string
}

// ProxyManager manages proxy rotation and availability
type ProxyManager struct {
	proxies      []ProxyConfig
	currentIndex int
	mutex        sync.Mutex
	failureCount map[string]int
	lastFailure  map[string]time.Time
}

var (
	youtubeProxyManager *ProxyManager
	once                sync.Once
)

// GetYouTubeProxyManager returns the singleton YouTube proxy manager
func GetYouTubeProxyManager() *ProxyManager {
	once.Do(func() {
		youtubeProxyManager = NewProxyManager(getYouTubeProxyConfigs())
	})
	return youtubeProxyManager
}

// NewProxyManager creates a new proxy manager
func NewProxyManager(proxies []ProxyConfig) *ProxyManager {
	return &ProxyManager{
		proxies:      proxies,
		currentIndex: 0,
		failureCount: make(map[string]int),
		lastFailure:  make(map[string]time.Time),
	}
}

// GetNextProxy returns the next available proxy in rotation
func (pm *ProxyManager) GetNextProxy() *ProxyConfig {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	if len(pm.proxies) == 0 {
		return nil
	}

	// Try to find a working proxy (not recently failed)
	maxAttempts := len(pm.proxies)
	for attempts := 0; attempts < maxAttempts; attempts++ {
		proxy := &pm.proxies[pm.currentIndex]
		pm.currentIndex = (pm.currentIndex + 1) % len(pm.proxies)

		// Check if proxy has recently failed
		if lastFail, exists := pm.lastFailure[proxy.URL]; exists {
			// Skip if failed in last 5 minutes
			if time.Since(lastFail) < 5*time.Minute {
				continue
			}
		}

		// Check failure count
		if pm.failureCount[proxy.URL] >= 3 {
			// Reset after 30 minutes
			if lastFail, exists := pm.lastFailure[proxy.URL]; exists {
				if time.Since(lastFail) > 30*time.Minute {
					pm.failureCount[proxy.URL] = 0
				} else {
					continue
				}
			}
		}

		return proxy
	}

	// All proxies have failed recently, return least recently failed
	return &pm.proxies[pm.currentIndex]
}

// MarkProxyFailed marks a proxy as failed
func (pm *ProxyManager) MarkProxyFailed(proxyURL string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pm.failureCount[proxyURL]++
	pm.lastFailure[proxyURL] = time.Now()
	log.Printf("🚫 Proxy failed: %s (failure count: %d)", maskProxyCredentials(proxyURL), pm.failureCount[proxyURL])
}

// MarkProxySuccess marks a proxy as successful
func (pm *ProxyManager) MarkProxySuccess(proxyURL string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	pm.failureCount[proxyURL] = 0
	delete(pm.lastFailure, proxyURL)
	log.Printf("✅ Proxy succeeded: %s", maskProxyCredentials(proxyURL))
}

// GetAllProxies returns all configured proxies (for fallback attempts)
func (pm *ProxyManager) GetAllProxies() []ProxyConfig {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// Return a copy to avoid race conditions
	proxies := make([]ProxyConfig, len(pm.proxies))
	copy(proxies, pm.proxies)
	return proxies
}

// getYouTubeProxyConfigs parses YouTube proxy configurations from environment
func getYouTubeProxyConfigs() []ProxyConfig {
	var configs []ProxyConfig

	// Parse the provided proxies
	proxies := []struct {
		host     string
		port     string
		username string
		password string
	}{
		{"PROXY_IP_1", "6712", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_2", "6543", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_3", "6349", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_4", "6837", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_5", "6661", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_6", "6732", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_7", "6593", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_8", "6655", "PROXY_USER", "PROXY_PASS"},
		{"PROXY_IP_9", "5653", "PROXY_USER", "PROXY_PASS"},
	}

	// Create SOCKS5 proxy configurations
	for _, p := range proxies {
		proxyURL := fmt.Sprintf("socks5://%s:%s@%s:%s", p.username, p.password, p.host, p.port)
		config := ProxyConfig{
			URL:      proxyURL,
			Type:     "socks5",
			Host:     p.host,
			Port:     p.port,
			Username: p.username,
			Password: p.password,
		}
		configs = append(configs, config)
	}

	// Shuffle the list for better distribution
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(configs), func(i, j int) {
		configs[i], configs[j] = configs[j], configs[i]
	})

	log.Printf("🎯 Loaded %d YouTube proxies", len(configs))
	return configs
}

// maskProxyCredentials masks sensitive parts of proxy URL for logging
func maskProxyCredentials(proxyURL string) string {
	// Find username:password part
	if atIndex := len(proxyURL) - 1; atIndex >= 0 {
		for i := 0; i < len(proxyURL); i++ {
			if proxyURL[i] == '@' {
				// Found @ sign, mask everything before it after protocol
				protocolEnd := 0
				if idx := findString(proxyURL, "://"); idx >= 0 {
					protocolEnd = idx + 3
				}
				if protocolEnd < i {
					return proxyURL[:protocolEnd] + "***:***@" + proxyURL[i+1:]
				}
				break
			}
		}
	}
	return proxyURL
}

// findString finds substring in string
func findString(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// MaskProxyURL is a public wrapper for maskProxyCredentials
func MaskProxyURL(proxyURL string) string {
	return maskProxyCredentials(proxyURL)
}