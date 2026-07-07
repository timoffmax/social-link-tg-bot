package services

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

// RedditExtractor handles Reddit post extraction via the JSON API with proxy support
type RedditExtractor struct {
	directClient *http.Client
	tempDir      string
}

// redditProxyServiceName is the outage-tracker key for the Reddit proxy pool.
const redditProxyServiceName = "reddit-proxy"

// redditHTTPError is a non-200 answer from the Reddit JSON API, carrying the
// status code so post-level failures (404 for deleted posts) can be told apart
// from proxy/blocking failures.
type redditHTTPError struct {
	StatusCode int
	Excerpt    string
}

// Error returns the error description with a capped body excerpt.
func (e *redditHTTPError) Error() string {
	return fmt.Sprintf("Reddit API returned status %d: %s", e.StatusCode, e.Excerpt)
}

// isRedditPostLevelError reports whether the error is specific to the
// requested post (deleted/private/nonexistent) rather than a proxy problem.
func isRedditPostLevelError(err error) bool {
	result := false

	var httpErr *redditHTTPError

	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusNotFound, http.StatusGone, http.StatusBadRequest:
			result = true
		}
	}

	return result
}

// redditListingResponse represents the top-level Reddit .json response
type redditListingResponse struct {
	Data struct {
		Children []struct {
			Data redditPostData `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

// redditPostData represents a single Reddit post
type redditPostData struct {
	Title         string                     `json:"title"`
	Author        string                     `json:"author"`
	Subreddit     string                     `json:"subreddit"`
	IsGallery     bool                       `json:"is_gallery"`
	IsVideo       bool                       `json:"is_video"`
	IsSelf        bool                       `json:"is_self"`
	URL           string                     `json:"url"`
	Selftext      string                     `json:"selftext"`
	MediaMetadata map[string]redditMediaMeta `json:"media_metadata"`
	GalleryData   *redditGalleryData         `json:"gallery_data"`
	Media         *redditMedia               `json:"media"`
	Preview       *redditPreview             `json:"preview"`
}

type redditMediaMeta struct {
	E      string            `json:"e"`
	Status string            `json:"status"`
	S      redditMediaSource `json:"s"`
}

type redditMediaSource struct {
	U string `json:"u"`
	X int    `json:"x"`
	Y int    `json:"y"`
}

type redditGalleryData struct {
	Items []struct {
		MediaID string `json:"media_id"`
	} `json:"items"`
}

type redditMedia struct {
	RedditVideo *redditVideo `json:"reddit_video"`
}

type redditVideo struct {
	FallbackURL string `json:"fallback_url"`
	DashURL     string `json:"dash_url"`
	Height      int    `json:"height"`
	Width       int    `json:"width"`
	Duration    int    `json:"duration"`
}

type redditPreview struct {
	Images []struct {
		Source struct {
			URL    string `json:"url"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
		} `json:"source"`
	} `json:"images"`
}

// NewRedditExtractor creates a new Reddit extractor
func NewRedditExtractor(tempDir string) *RedditExtractor {
	return &RedditExtractor{
		directClient: SharedDownloadClient,
		tempDir:      tempDir,
	}
}

// createProxyClient creates an HTTP client with the given proxy URL.
func createProxyClient(proxyURLStr string) (*http.Client, error) {
	proxyURL, err := url.Parse(proxyURLStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse proxy URL: %v", err)
	}

	result := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
			MaxIdleConns:    10,
			IdleConnTimeout: 90 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	return result, nil
}

// ExtractRedditPost extracts media from a Reddit post using the JSON API via proxy
func (re *RedditExtractor) ExtractRedditPost(redditURL string) ([]*MediaInfo, error) {
	log.Printf("🟠 Starting Reddit JSON API extraction for: %s", redditURL)

	proxies := config.GetYtDlpProxies()

	// The Reddit JSON API requires proxies, but honor the global kill-switch
	// so a dead pool can be disabled entirely instead of burning requests.
	if config.IsProxyEnabled() == false {
		proxies = []string{}
	}

	// Resolve short URLs first — direct resolution works without proxies
	if strings.Contains(redditURL, "/s/") {
		resolvedURL, err := re.resolveShortURL(redditURL, proxies)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve short URL: %v", err)
		}

		redditURL = resolvedURL
	}

	if len(proxies) == 0 {
		return nil, fmt.Errorf("no usable proxies — required for Reddit API access (check YTDLP_PROXIES / ENABLE_PROXIES)")
	}

	// Build the JSON API URL
	jsonURL := re.buildJSONURL(redditURL)
	log.Printf("🟠 Reddit JSON URL: %s", jsonURL)

	// Fetch post data via proxy (try each proxy)
	var postData *redditPostData
	var lastErr error

	for _, proxy := range proxies {
		postData, lastErr = re.fetchPostData(jsonURL, proxy)
		if lastErr == nil {
			break
		}

		log.Printf("⚠️ Reddit proxy failed, trying next: %v", lastErr)
	}

	if postData == nil {
		// A post-level 4xx (deleted/private post) fails identically through
		// every proxy — that is not a proxy-pool outage.
		if isRedditPostLevelError(lastErr) == false {
			ReportServiceFailure(redditProxyServiceName, fmt.Sprintf("all %d proxies failed: %v", len(proxies), lastErr))
		}

		return nil, fmt.Errorf("all proxies failed for Reddit API: %v", lastErr)
	}

	ReportServiceSuccess(redditProxyServiceName)

	// Extract media based on post type
	result, err := re.extractMedia(postData, redditURL)
	if err != nil {
		return nil, err
	}

	log.Printf("✅ Reddit JSON API extracted %d media items", len(result))

	return result, nil
}

// resolveShortURL resolves Reddit short URLs (/r/.../s/...), trying a direct
// connection first and falling back to the proxy pool.
func (re *RedditExtractor) resolveShortURL(shortURL string, proxies []string) (string, error) {
	log.Printf("🔗 Resolving Reddit short URL: %s", shortURL)

	// Direct resolution first — the /s/ 301 redirect is served without auth
	// and is not gated the way the JSON API is, so no proxy is needed for
	// this hop in most cases.
	location, directErr := re.resolveShortURLDirect(shortURL)

	if directErr == nil {
		log.Printf("🔗 Resolved Reddit short URL (direct) → %s", location)

		return location, nil
	}

	log.Printf("⚠️ Direct short URL resolution failed (%v), retrying through %d proxies", directErr, len(proxies))

	for _, proxy := range proxies {
		proxyClient, err := createProxyClient(proxy)
		if err != nil {
			continue
		}

		// Don't follow redirects — capture the Location header
		proxyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}

		req, err := http.NewRequest("GET", shortURL, nil)
		if err != nil {
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

		resp, err := proxyClient.Do(req)
		if err != nil {
			log.Printf("⚠️ Proxy failed for short URL resolution: %v", err)
			continue
		}
		resp.Body.Close()

		if location := resp.Header.Get("Location"); location != "" {
			log.Printf("🔗 Resolved Reddit short URL → %s", location)
			return location, nil
		}

		// If no redirect, maybe the proxy followed it — try following redirects
		proxyClient.CheckRedirect = nil

		resp, err = proxyClient.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if (resp.StatusCode == http.StatusOK) && (resp.Request != nil) {
			finalURL := resp.Request.URL.String()
			if finalURL != shortURL {
				log.Printf("🔗 Resolved Reddit short URL → %s", finalURL)
				return finalURL, nil
			}
		}
	}

	return "", fmt.Errorf("failed to resolve Reddit short URL through any proxy")
}

// resolveShortURLDirect resolves a Reddit short URL with a plain no-proxy
// request, capturing the redirect Location without following it.
func (re *RedditExtractor) resolveShortURLDirect(shortURL string) (string, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequest("GET", shortURL, nil)

	if err != nil {
		return "", err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := client.Do(req)

	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if (resp.StatusCode < 300) || (resp.StatusCode >= 400) {
		return "", fmt.Errorf("expected redirect for short URL, got HTTP %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")

	if location == "" {
		return "", fmt.Errorf("redirect without Location header")
	}

	if strings.HasPrefix(location, "/") {
		location = "https://www.reddit.com" + location
	}

	return location, nil
}

// buildJSONURL converts a Reddit URL to its old.reddit.com/.json equivalent
func (re *RedditExtractor) buildJSONURL(redditURL string) string {
	parsed, err := url.Parse(redditURL)
	if err != nil {
		return redditURL + ".json"
	}

	path := strings.TrimRight(parsed.Path, "/")

	return fmt.Sprintf("https://old.reddit.com%s/.json", path)
}

// fetchPostData fetches post data from the Reddit JSON API via a specific proxy
func (re *RedditExtractor) fetchPostData(jsonURL, proxy string) (*redditPostData, error) {
	proxyClient, err := createProxyClient(proxy)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("GET", jsonURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := proxyClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("proxy request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))

		return nil, &redditHTTPError{
			StatusCode: resp.StatusCode,
			Excerpt:    string(body),
		}
	}

	var listings []redditListingResponse

	err = json.NewDecoder(resp.Body).Decode(&listings)
	if err != nil {
		return nil, fmt.Errorf("failed to decode Reddit JSON: %v", err)
	}

	if (len(listings) == 0) || (len(listings[0].Data.Children) == 0) {
		return nil, fmt.Errorf("no post data found in Reddit JSON response")
	}

	return &listings[0].Data.Children[0].Data, nil
}

// extractMedia dispatches media extraction based on post type
func (re *RedditExtractor) extractMedia(post *redditPostData, originalURL string) ([]*MediaInfo, error) {
	// Captions are sent with Telegram HTML parse mode, and Reddit returns the
	// title with HTML entities — normalize instead of double-escaping.
	caption := FormatAuthorCaption("reddit", post.Subreddit, post.Author, EscapeForTelegramHTML(post.Title))

	// Gallery posts
	if post.IsGallery {
		return re.extractGallery(post, caption, originalURL)
	}

	// Video posts
	if (post.IsVideo) && (post.Media != nil) && (post.Media.RedditVideo != nil) {
		return re.extractVideo(post, caption, originalURL)
	}

	// Single image posts
	if re.isImageURL(post.URL) {
		return re.downloadAndReturn(html.UnescapeString(post.URL), "photo", caption, originalURL)
	}

	// Preview image fallback
	if (post.Preview != nil) && (len(post.Preview.Images) > 0) {
		imageURL := html.UnescapeString(post.Preview.Images[0].Source.URL)
		return re.downloadAndReturn(imageURL, "photo", caption, originalURL)
	}

	// Text-only post — send the selftext as a message
	if post.IsSelf {
		return re.extractTextPost(post, caption, originalURL)
	}

	return nil, fmt.Errorf("unsupported Reddit post type (external link): %s", post.URL)
}

// extractGallery downloads all images from a gallery post
func (re *RedditExtractor) extractGallery(post *redditPostData, caption, originalURL string) ([]*MediaInfo, error) {
	if (post.GalleryData == nil) || (len(post.GalleryData.Items) == 0) {
		return nil, fmt.Errorf("gallery post but no gallery_data items")
	}

	log.Printf("📸 Reddit gallery with %d items", len(post.GalleryData.Items))

	var mediaInfos []*MediaInfo

	for i, item := range post.GalleryData.Items {
		meta, exists := post.MediaMetadata[item.MediaID]
		if !exists {
			log.Printf("⚠️ Gallery item %s not found in media_metadata, skipping", item.MediaID)
			continue
		}

		imageURL := html.UnescapeString(meta.S.U)
		if imageURL == "" {
			log.Printf("⚠️ Gallery item %s has no source URL, skipping", item.MediaID)
			continue
		}

		log.Printf("⬇️ Downloading gallery image %d/%d", i+1, len(post.GalleryData.Items))

		filename := filepath.Join(re.tempDir, fmt.Sprintf("%d_reddit_gallery_%d.jpg", time.Now().UnixNano(), i))
		err := downloadToFile(imageURL, filename)
		if err != nil {
			log.Printf("⚠️ Failed to download gallery image %d: %v", i+1, err)
			continue
		}

		itemCaption := ""
		if i == 0 {
			itemCaption = caption
		}

		mediaInfos = append(mediaInfos, &MediaInfo{
			Type:     "photo",
			URL:      originalURL,
			Caption:  itemCaption,
			Filename: filename,
		})
	}

	if len(mediaInfos) == 0 {
		return nil, fmt.Errorf("failed to download any gallery images")
	}

	return mediaInfos, nil
}

// extractVideo downloads a Reddit video and attempts to merge audio
func (re *RedditExtractor) extractVideo(post *redditPostData, caption, originalURL string) ([]*MediaInfo, error) {
	videoURL := post.Media.RedditVideo.FallbackURL
	log.Printf("🎥 Reddit video: %s", videoURL)

	videoFilename := filepath.Join(re.tempDir, fmt.Sprintf("%d_reddit_video.mp4", time.Now().UnixNano()))
	err := downloadToFile(videoURL, videoFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to download video: %v", err)
	}

	// Try to merge audio
	audioURL := re.buildAudioURL(videoURL)
	if audioURL != "" {
		mergedFilename, mergeErr := re.mergeVideoAudio(videoFilename, audioURL)
		if mergeErr == nil {
			log.Printf("✅ Merged Reddit video with audio")
			os.Remove(videoFilename)
			videoFilename = mergedFilename
		} else {
			log.Printf("⚠️ Audio merge failed, sending video without audio: %v", mergeErr)
		}
	}

	result := []*MediaInfo{{
		Type:     "video",
		URL:      originalURL,
		Caption:  caption,
		Filename: videoFilename,
	}}

	return result, nil
}

// cleanRedditMarkdown removes Reddit markdown formatting from text
func cleanRedditMarkdown(text string) string {
	// Remove bold markers
	text = strings.ReplaceAll(text, "**", "")

	// Remove Reddit markdown escape backslashes: \- \[ \] \~ \( \) \# \. \! \*
	replacer := strings.NewReplacer(
		`\-`, `-`,
		`\[`, `[`,
		`\]`, `]`,
		`\~`, `~`,
		`\(`, `(`,
		`\)`, `)`,
		`\#`, `#`,
		`\.`, `.`,
		`\!`, `!`,
		`\*`, `*`,
		`\_`, `_`,
		`\>`, `>`,
	)
	text = replacer.Replace(text)

	// Remove heading markers (# ## ### etc.) at the start of lines
	headingPattern := regexp.MustCompile(`(?m)^#{1,6}\s+`)
	text = headingPattern.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}

// extractTextPost returns the selftext content as a text message
func (re *RedditExtractor) extractTextPost(post *redditPostData, caption, originalURL string) ([]*MediaInfo, error) {
	text := cleanRedditMarkdown(post.Selftext)
	if text == "" {
		text = post.Title
	}

	textIsTitle := text == post.Title

	// The message is sent with Telegram HTML parse mode — unescaped '<' or '&'
	// in the selftext would make Telegram reject the whole message with a 400.
	text = EscapeForTelegramHTML(text)

	// Build full message: author caption + selftext
	fullText := caption
	if (fullText != "") && (text != "") && (textIsTitle == false) {
		fullText = fullText + "\n\n" + text
	} else if (fullText == "") && (text != "") {
		fullText = text
	}

	log.Printf("📝 Reddit text-only post (%d chars)", len(fullText))

	result := []*MediaInfo{{
		Type:     "text",
		URL:      originalURL,
		Caption:  fullText,
		Filename: "",
	}}

	return result, nil
}

var redditDashPattern = regexp.MustCompile(`(https://v\.redd\.it/[^/]+/)DASH_\d+\.mp4`)

// buildAudioURL derives the DASH audio URL from the video fallback URL
func (re *RedditExtractor) buildAudioURL(videoURL string) string {
	matches := redditDashPattern.FindStringSubmatch(videoURL)
	if len(matches) > 1 {
		return matches[1] + "DASH_audio.mp4"
	}

	return ""
}

// mergeVideoAudio downloads audio and merges it with video using ffmpeg
func (re *RedditExtractor) mergeVideoAudio(videoPath, audioURL string) (string, error) {
	ffmpegPath := findFFmpegPath()
	if ffmpegPath == "" {
		return "", fmt.Errorf("ffmpeg not available for audio merge")
	}

	audioPath := videoPath + ".audio.mp4"
	err := downloadToFile(audioURL, audioPath)
	if err != nil {
		return "", fmt.Errorf("failed to download audio: %v", err)
	}
	defer os.Remove(audioPath)

	mergedPath := strings.Replace(videoPath, ".mp4", "_merged.mp4", 1)
	cmd := exec.Command(ffmpegPath,
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", "aac",
		"-movflags", "+faststart",
		"-y",
		mergedPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("❌ ffmpeg merge failed: %v\nOutput: %s", err, string(output))
		os.Remove(mergedPath)
		return "", fmt.Errorf("ffmpeg merge failed: %v", err)
	}

	return mergedPath, nil
}

func (re *RedditExtractor) isImageURL(u string) bool {
	return strings.Contains(u, "i.redd.it") || strings.Contains(u, "preview.redd.it")
}

// downloadAndReturn downloads a single media file and returns it as MediaInfo
func (re *RedditExtractor) downloadAndReturn(mediaURL, mediaType, caption, originalURL string) ([]*MediaInfo, error) {
	log.Printf("⬇️ Downloading Reddit %s: %s", mediaType, mediaURL)

	ext := ".jpg"
	if strings.Contains(mediaURL, ".png") {
		ext = ".png"
	} else if strings.Contains(mediaURL, ".gif") {
		ext = ".gif"
	} else if strings.Contains(mediaURL, ".webp") {
		ext = ".webp"
	} else if strings.Contains(mediaURL, ".mp4") {
		ext = ".mp4"
		mediaType = "video"
	}

	filename := filepath.Join(re.tempDir, fmt.Sprintf("%d_reddit_%s%s", time.Now().UnixNano(), mediaType, ext))
	err := downloadToFile(mediaURL, filename)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %v", mediaType, err)
	}

	result := []*MediaInfo{{
		Type:     mediaType,
		URL:      originalURL,
		Caption:  caption,
		Filename: filename,
	}}

	return result, nil
}
