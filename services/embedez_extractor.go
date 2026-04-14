package services

import (
	"fmt"
	"github.com/timoffmax/social-link-bot/config"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

// EmbedezExtractor handles video extraction from instagramez/kksave/embedez service
type EmbedezExtractor struct {
	client *http.Client
}

// NewEmbedezExtractor creates a new instance of EmbedezExtractor
func NewEmbedezExtractor() *EmbedezExtractor {
	return &EmbedezExtractor{
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				// Allow up to 10 redirects
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// ExtractInstagramVideo extracts video URL from Instagram via configured service
func (e *EmbedezExtractor) ExtractInstagramVideo(instagramURL string) (string, error) {
	// Convert Instagram URL to the configured service URL
	serviceURL := e.convertToServiceURL(instagramURL)
	service := config.GetInstagramService()
	log.Printf("🔍 [Embedez] Converting to %s URL: %s", service, serviceURL)
	
	// Follow service redirect to embedez (or equivalent for kksave)
	embedezURL, err := e.followToEmbedez(serviceURL)
	if err != nil {
		return "", fmt.Errorf("failed to get embedez URL: %v", err)
	}
	log.Printf("🔍 [Embedez] Got embedez URL: %s", embedezURL)
	
	// Extract video URL from embedez page
	videoURL, err := e.extractVideoFromEmbedez(embedezURL)
	if err != nil {
		return "", fmt.Errorf("failed to extract video URL: %v", err)
	}
	log.Printf("✅ [Embedez] Extracted video URL: %s", videoURL)
	
	// Decode the proxied URL to get the actual Instagram CDN URL
	actualURL, err := e.decodeProxiedURL(videoURL)
	if err != nil {
		// If we can't decode, return the proxied URL
		log.Printf("⚠️ [Embedez] Could not decode proxied URL, using as-is: %v", err)
		return videoURL, nil
	}
	
	log.Printf("✅ [Embedez] Decoded actual URL: %s", actualURL)
	return actualURL, nil
}

// convertToServiceURL converts Instagram URL to the configured service URL
func (e *EmbedezExtractor) convertToServiceURL(instagramURL string) string {
	service := config.GetInstagramService()
	if service == "kksave" {
		result := strings.Replace(instagramURL, "https://www.instagram.com", "https://www.kksave.com", 1)
		result = strings.Replace(result, "https://instagram.com", "https://www.kksave.com", 1)
		return result
	}
	// Default to instagramez
	result := strings.Replace(instagramURL, "https://www.instagram.com", "https://www.instagramez.com", 1)
	result = strings.Replace(result, "https://instagram.com", "https://www.instagramez.com", 1)
	return result
}

// convertToInstagramez is deprecated, use convertToServiceURL instead
func (e *EmbedezExtractor) convertToInstagramez(instagramURL string) string {
	return e.convertToServiceURL(instagramURL)
}

// followToEmbedez follows the service redirect to get the embedez URL (or equivalent)
func (e *EmbedezExtractor) followToEmbedez(instagramezURL string) (string, error) {
	resp, err := e.client.Get(instagramezURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	// The final URL after redirects should be the embedez URL
	finalURL := resp.Request.URL.String()
	
	// Verify it's an embedez URL
	if !strings.Contains(finalURL, "embedez.com") {
		// If not redirected to embedez, try to extract from the page
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read response body: %v", err)
		}
		
		// Look for embedez URL in the page
		embedezPattern := regexp.MustCompile(`https://embedez\.com/embed/[^"'\s]+`)
		matches := embedezPattern.FindString(string(body))
		if matches != "" {
			return matches, nil
		}
		
		return "", fmt.Errorf("no embedez URL found in response")
	}
	
	return finalURL, nil
}

// extractVideoFromEmbedez extracts the video URL from the embedez page
func (e *EmbedezExtractor) extractVideoFromEmbedez(embedezURL string) (string, error) {
	resp, err := e.client.Get(embedezURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	
	bodyStr := string(body)
	
	// Try multiple patterns to extract video URL
	patterns := []string{
		// Pattern 1: Direct proxy.embedez.com URL
		`https://proxy\.embedez\.com/[^"'\s]+\.mp4[^"'\s]*`,
		// Pattern 2: Video source tag
		`<source[^>]+src=["']([^"']+\.mp4[^"']*?)["']`,
		// Pattern 3: Video tag
		`<video[^>]+src=["']([^"']+\.mp4[^"']*?)["']`,
		// Pattern 4: JSON data with video URL
		`"url"\s*:\s*"(https://[^"]+\.mp4[^"]*)"`,
		// Pattern 5: data-src attribute
		`data-src=["']([^"']+\.mp4[^"']*?)["']`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindStringSubmatch(bodyStr)
		if len(matches) > 0 {
			videoURL := matches[0]
			if len(matches) > 1 && matches[1] != "" {
				videoURL = matches[1]
			}
			// Clean up the URL
			videoURL = strings.ReplaceAll(videoURL, "\\u0026", "&")
			videoURL = strings.ReplaceAll(videoURL, "\\/", "/")
			return videoURL, nil
		}
	}
	
	// Try to find any MP4 URL in the page
	mp4Pattern := regexp.MustCompile(`https://[^"'\s]+\.mp4[^"'\s]*`)
	mp4Matches := mp4Pattern.FindString(bodyStr)
	if mp4Matches != "" {
		return mp4Matches, nil
	}
	
	return "", fmt.Errorf("no video URL found in embedez page")
}

// decodeProxiedURL decodes a proxied embedez URL to get the actual CDN URL
func (e *EmbedezExtractor) decodeProxiedURL(proxiedURL string) (string, error) {
	// Check if this is a proxy.embedez.com URL
	if !strings.Contains(proxiedURL, "proxy.embedez.com") {
		// Not a proxied URL, return as-is
		return proxiedURL, nil
	}
	
	// Parse the URL
	parsedURL, err := url.Parse(proxiedURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %v", err)
	}
	
	// Get the 'url' parameter which contains the actual video URL
	urlParam := parsedURL.Query().Get("url")
	if urlParam == "" {
		// Try to extract from the path if it's embedded there
		pathPattern := regexp.MustCompile(`url=([^&]+)`)
		matches := pathPattern.FindStringSubmatch(proxiedURL)
		if len(matches) > 1 {
			urlParam = matches[1]
		}
	}
	
	if urlParam == "" {
		return "", fmt.Errorf("no URL parameter found in proxied URL")
	}
	
	// URL decode the parameter
	decodedURL, err := url.QueryUnescape(urlParam)
	if err != nil {
		return "", fmt.Errorf("failed to decode URL parameter: %v", err)
	}
	
	return decodedURL, nil
}

// ExtractInstagramVideoWithFallback tries to extract video using embedez, with yt-dlp as fallback
func (e *EmbedezExtractor) ExtractInstagramVideoWithFallback(instagramURL string, ytdlp *YtDlpDownloader) (*MediaInfo, error) {
	// First, try embedez extraction
	videoURL, err := e.ExtractInstagramVideo(instagramURL)
	if err == nil && videoURL != "" {
		log.Printf("✅ [Embedez] Successfully extracted video URL via embedez")
		
		// Download the video file
		tempDir := os.TempDir()
		filename := fmt.Sprintf("instagram_video_%d.mp4", time.Now().Unix())
		filepath := fmt.Sprintf("%s/%s", tempDir, filename)
		
		// Download the video
		err = e.downloadFile(videoURL, filepath)
		if err != nil {
			return nil, fmt.Errorf("failed to download video: %v", err)
		}
		
		return &MediaInfo{
			Type:     "video",
			URL:      instagramURL,
			Caption:  "Downloaded from Instagram",
			Filename: filepath,
		}, nil
	}
	
	log.Printf("⚠️ [Embedez] Embedez extraction failed (%v), trying yt-dlp fallback", err)
	
	// Fallback to yt-dlp
	if ytdlp != nil && ytdlp.IsAvailable() {
		mediaInfos, err := ytdlp.DownloadMedia(instagramURL)
		if err == nil && len(mediaInfos) > 0 {
			// Return the first video found
			for _, media := range mediaInfos {
				if media.Type == "video" {
					return media, nil
				}
			}
			// If no video, return the first media item
			return mediaInfos[0], nil
		}
	}
	
	return nil, fmt.Errorf("failed to extract Instagram video via embedez and yt-dlp")
}

// downloadFile downloads a file from URL to filepath
func (e *EmbedezExtractor) downloadFile(url, filepath string) error {
	resp, err := e.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	
	_, err = io.Copy(out, resp.Body)
	return err
}

// Additional helper for testing
func (e *EmbedezExtractor) TestExtraction(instagramURL string) {
	log.Printf("🧪 [Embedez] Testing extraction for: %s", instagramURL)
	
	videoURL, err := e.ExtractInstagramVideo(instagramURL)
	if err != nil {
		log.Printf("❌ [Embedez] Test failed: %v", err)
		return
	}
	
	log.Printf("✅ [Embedez] Test successful!")
	log.Printf("   Original URL: %s", instagramURL)
	log.Printf("   Extracted video URL: %s", videoURL)
	
	// Test if the video URL is accessible
	resp, err := http.Head(videoURL)
	if err != nil {
		log.Printf("⚠️ [Embedez] Video URL not accessible: %v", err)
	} else {
		log.Printf("✅ [Embedez] Video URL is accessible (Status: %s)", resp.Status)
		if resp.ContentLength > 0 {
			log.Printf("   Video size: %.2f MB", float64(resp.ContentLength)/(1024*1024))
		}
	}
}