package services

// RedditVxredditExtractor resolves Reddit posts through vxreddit.com, which
// scrapes Reddit and answers with og tags. Like the kk redirectors, this works
// from datacenter IPs where reddit.com blocks direct access, and vxreddit
// resolves /s/ share links itself.
//
// What the page gives: og:site_name "u/<author> on r/<subreddit> - ...",
// og:title (the post title; "vxReddit" for a title-only text post, whose
// title is then og:description), og:description (the body of a text post, the
// target URL of a link post), og:video (video re-served audio-merged from
// vxreddit's own CDN), one og:image per post image in order (raw i.redd.it
// URLs: galleries list every image, GIFs stay .gif) and, for link and video
// posts, an external-preview.redd.it thumbnail.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/timoffmax/social-link-bot/config"
)

const (
	// redditVxredditServiceName is the outage-tracker key for vxreddit.
	redditVxredditServiceName = "vxreddit"

	// RedditPlatform is the platform key of Reddit (captions, settings, stats).
	RedditPlatform = "reddit"

	// vxredditMinMediaSize guards against tiny HTML/error bodies posing as media.
	vxredditMinMediaSize = int64(10 * 1024)

	// vxredditMaxPage caps a vxreddit page (they are a few KB).
	vxredditMaxPage = 1024 * 1024

	// vxredditFetchTimeout covers vxreddit scraping Reddit live.
	vxredditFetchTimeout = 20 * time.Second

	// vxredditPlaceholderTitle is og:title of a post without a body; the
	// title is then og:description.
	vxredditPlaceholderTitle = "vxReddit"

	// vxredditPreviewHost serves the link-preview and video thumbnails.
	vxredditPreviewHost = "external-preview.redd.it"

	// vxredditMediaScheme is the only scheme media is downloaded over.
	vxredditMediaScheme = "https"

	// vxredditImageConcurrency is how many gallery images download at once.
	vxredditImageConcurrency = 4

	// vxredditFilePrefix starts the temp file names.
	vxredditFilePrefix = "reddit_vx"

	// redditTruncationMarker ends a text post body cut to the message limit.
	redditTruncationMarker = "…"

	// redditTextSeparator separates the caption's parts.
	redditTextSeparator = "\n\n"

	// mimeTypeGIF is the mime type of a GIF.
	mimeTypeGIF = "image/gif"
)

var (
	// vxredditSiteNamePattern captures author and subreddit of og:site_name.
	vxredditSiteNamePattern = regexp.MustCompile(`^u/(\S+) on r/(\S+)`)

	// vxredditSubredditPath captures the subreddit of a Reddit URL.
	vxredditSubredditPath = regexp.MustCompile(`/r/([^/]+)/`)

	// vxredditLinkPattern matches a body that is one URL (a link post).
	vxredditLinkPattern = regexp.MustCompile(`^https?://\S+$`)

	// errVxredditEmptyPost marks a page with neither media nor text.
	errVxredditEmptyPost = errors.New("vxreddit page has no media and no text")

	// errVxredditNotReddit marks a URL whose host is not Reddit.
	errVxredditNotReddit = errors.New("not a Reddit URL")

	// errVxredditNoPost marks vxreddit's error page ("Failed to get data from
	// Reddit", "Invalid post path"): no "u/<author> on r/<subreddit>" site
	// name. Usually a deleted or private post, so it is no outage.
	errVxredditNoPost = errors.New("vxreddit shows no post")

	// errVxredditOffCDN marks a media URL or redirect off the Reddit and
	// vxreddit media hosts: the page is third-party content, so the bot must
	// not fetch whatever URL it names.
	errVxredditOffCDN = errors.New("media is not on a Reddit or vxreddit media host")

	// vxredditRendererDomain hosts vxreddit's video renderer (an AWS Lambda
	// function URL): vxreddit.com/redditvideo.mp4 redirects there, and it
	// redirects on to renders.<vxreddit host>. Accepted only as a redirect
	// from the vxreddit host, never as a URL the page names.
	vxredditRendererDomain = "lambda-url.us-east-1.on.aws"

	// redditMediaHosts serve Reddit's images, previews and videos.
	redditMediaHosts = []string{"i.redd.it", "preview.redd.it", vxredditPreviewHost, "v.redd.it"}

	// errVxredditUnresolvedMedia marks a post with only a thumbnail and no
	// body or link: media vxreddit did not resolve, left to the other methods.
	errVxredditUnresolvedMedia = errors.New("vxreddit shows only a thumbnail of this post")

	// redditMarkdownReplacer undoes Reddit's markdown escape backslashes.
	redditMarkdownReplacer = strings.NewReplacer(
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

	// redditHeadingPattern matches heading markers (# to ######) at line starts.
	redditHeadingPattern = regexp.MustCompile(`(?m)^#{1,6}\s+`)
)

// RedditVxredditExtractor downloads Reddit posts via the vxreddit.com frontend.
type RedditVxredditExtractor struct {
	tempDir string
	// base is the vxreddit scheme and host ("https://vxreddit.com").
	base string
	// vxredditHost is the vxreddit host: media may come from it (and its
	// subdomains), and it may redirect to its video renderer.
	vxredditHost string
	// mediaHTTPClient replaces SharedDownloadClient for media (tests).
	mediaHTTPClient *http.Client
}

// vxredditPost is a parsed vxreddit page.
type vxredditPost struct {
	IsPost       bool
	Author       string
	Subreddit    string
	Title        string
	Body         string
	LinkURL      string
	VideoURL     string
	Images       []string
	PreviewImage string
}

// NewRedditVxredditExtractor creates a new vxreddit-backed Reddit extractor.
func NewRedditVxredditExtractor(tempDir string) *RedditVxredditExtractor {
	host := config.GetVxredditHost()
	base := fmt.Sprintf("https://%s", host)
	result := &RedditVxredditExtractor{tempDir: tempDir, base: base, vxredditHost: host}

	return result
}

// ExtractMedia reads the post from vxreddit and returns what it holds: the
// video, every image (album order, GIFs as animations), the link of a link
// post (with its preview image when it downloads) or the text of a text post.
// Page and media download failures feed the outage tracker.
func (e *RedditVxredditExtractor) ExtractMedia(ctx context.Context, redditURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var page string
	pageURL, err := e.buildVxredditURL(redditURL)

	if err == nil {
		page, err = e.fetchPage(ctx, pageURL)
		reportVxredditOutcome(ctx, err)
	}

	post := parseVxredditPage(page, redditURL)

	if (err == nil) && (post.IsPost == false) {
		err = fmt.Errorf("%w: %s (%s)", errVxredditNoPost, redditURL, post.Title)
	}

	if err == nil {
		result, err = e.buildMedia(ctx, post, redditURL)
	}

	return result, err
}

// buildMedia picks the post's shape: video, images, link or text.
func (e *RedditVxredditExtractor) buildMedia(ctx context.Context, post vxredditPost, redditURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	hasVideo := (post.VideoURL != "")
	hasImages := (len(post.Images) > 0)
	isLink := (hasVideo == false) && (hasImages == false) && (post.LinkURL != "")
	isText := (hasVideo == false) && (hasImages == false) && (isLink == false)
	isUnresolved := (isText == true) && (post.PreviewImage != "") && (strings.TrimSpace(post.Body) == "")

	if hasVideo == true {
		result, err = e.downloadVideo(ctx, post, redditURL)
	}

	if (hasVideo == false) && (hasImages == true) {
		result, err = e.downloadImages(ctx, post, redditURL)
	}

	if isLink == true {
		result = e.linkPostMedia(ctx, post, redditURL)
	}

	if (isText == true) && (isUnresolved == false) {
		result, err = vxredditTextPost(post, redditURL)
	}

	if isUnresolved == true {
		err = fmt.Errorf("%w: %s", errVxredditUnresolvedMedia, redditURL)
	}

	return result, err
}

// downloadVideo downloads the audio-merged video vxreddit re-serves.
func (e *RedditVxredditExtractor) downloadVideo(ctx context.Context, post vxredditPost, redditURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	timestamp := time.Now().UnixNano()
	basePath := e.mediaBasePath(timestamp, 0)
	item, err := e.downloadItem(ctx, post.VideoURL, basePath, MediaTypeVideo)
	reportVxredditOutcome(ctx, err)

	if err == nil {
		item.Caption = vxredditMediaCaption(post, redditURL)
		result = []*MediaInfo{item}
		finishVxredditMedia(result, redditURL)
	}

	return result, err
}

// downloadImages downloads every image in order (a few at once); images that
// fail are left out, and it fails only when none downloads.
func (e *RedditVxredditExtractor) downloadImages(ctx context.Context, post vxredditPost, redditURL string) ([]*MediaInfo, error) {
	var wg sync.WaitGroup
	timestamp := time.Now().UnixNano()
	items := make([]*MediaInfo, len(post.Images))
	itemErrs := make([]error, len(post.Images))
	slots := make(chan struct{}, vxredditImageConcurrency)

	for i, imageURL := range post.Images {
		wg.Add(1)

		go func(index int, imageURL string) {
			defer wg.Done()

			slots <- struct{}{}
			defer func() { <-slots }()

			basePath := e.mediaBasePath(timestamp, index)
			items[index], itemErrs[index] = e.downloadItem(ctx, imageURL, basePath, MediaTypePhoto)
		}(i, imageURL)
	}

	wg.Wait()

	result, err := collectVxredditImages(items, itemErrs, redditURL)
	reportVxredditOutcome(ctx, err)

	if err == nil {
		result[0].Caption = vxredditMediaCaption(post, redditURL)
		finishVxredditMedia(result, redditURL)
	}

	return result, err
}

// collectVxredditImages keeps the downloaded images in order and logs the
// failed ones; with none downloaded it returns every failure joined.
func collectVxredditImages(items []*MediaInfo, itemErrs []error, redditURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	var failures []error

	for i, item := range items {
		if itemErrs[i] == nil {
			result = append(result, item)
		}

		if itemErrs[i] != nil {
			log.Printf("❌ [vxreddit] image %d failed: %v", (i + 1), itemErrs[i])
			failures = append(failures, itemErrs[i])
		}
	}

	if len(result) == 0 {
		joined := errors.Join(failures...)
		err = fmt.Errorf("no Reddit image could be downloaded for %s: %w", redditURL, joined)
	}

	return result, err
}

// linkPostMedia returns a link post as its preview image with the title and
// link as caption, or as a text item when there is no preview or it fails.
func (e *RedditVxredditExtractor) linkPostMedia(ctx context.Context, post vxredditPost, redditURL string) []*MediaInfo {
	result := vxredditLinkPostText(post, redditURL)
	var item *MediaInfo
	err := errors.New("no preview image")
	timestamp := time.Now().UnixNano()
	basePath := e.mediaBasePath(timestamp, 0)

	if post.PreviewImage != "" {
		item, err = e.downloadItem(ctx, post.PreviewImage, basePath, MediaTypePhoto)
	}

	if err != nil {
		log.Printf("🔗 [vxreddit] link post sent as text: %v", err)
	}

	if err == nil {
		item.Caption = vxredditLinkCaption(post, redditURL)
		result = []*MediaInfo{item}
		finishVxredditMedia(result, redditURL)
	}

	return result
}

// vxredditLinkPostText returns a link post as one text item.
func vxredditLinkPostText(post vxredditPost, redditURL string) []*MediaInfo {
	caption := vxredditLinkCaption(post, redditURL)
	result := []*MediaInfo{{Type: MediaTypeText, URL: redditURL, Caption: caption}}

	return result
}

// vxredditTextPost returns a text post as one text item: author line, title
// and body, the body cut so the message fits Telegram's limit.
func vxredditTextPost(post vxredditPost, redditURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	title := strings.TrimSpace(post.Title)
	body := cleanRedditMarkdown(post.Body)

	if (title == "") && (body == "") {
		err = fmt.Errorf("%w: %s", errVxredditEmptyPost, redditURL)
	}

	build := func(bodyPrefix string, isCut bool) string {
		return vxredditTextCaption(post, title, bodyPrefix, isCut)
	}

	if err == nil {
		caption := fitTextMessage(body, redditURL, build)
		result = []*MediaInfo{{Type: MediaTypeText, URL: redditURL, Caption: caption}}
	}

	return result, err
}

// vxredditTextCaption formats author line, title and the (escaped) body,
// ended with the truncation marker when isCut.
func vxredditTextCaption(post vxredditPost, title, body string, isCut bool) string {
	parts := []string{}

	if title != "" {
		escapedTitle := html.EscapeString(title)
		parts = append(parts, escapedTitle)
	}

	if body != "" {
		escapedBody := html.EscapeString(body)
		parts = append(parts, escapedBody)
	}

	text := strings.Join(parts, redditTextSeparator)

	if isCut == true {
		text = fmt.Sprintf("%s%s", text, redditTruncationMarker)
	}

	result := FormatAuthorCaption(RedditPlatform, post.Subreddit, post.Author, text)

	return result
}

// vxredditMediaCaption formats author line and title of a media post, the
// title cut so the caption fits Telegram's caption limit next to the
// original link.
func vxredditMediaCaption(post vxredditPost, redditURL string) string {
	build := func(titlePrefix string, isCut bool) string {
		return vxredditTextCaption(post, titlePrefix, "", isCut)
	}
	result := fitCaptionMessage(post.Title, redditURL, build)

	return result
}

// vxredditLinkCaption formats author line, title and the link of a link post
// to fit Telegram's caption limit next to the original link: the title is
// cut first, the link only when it doesn't fit on its own.
func vxredditLinkCaption(post vxredditPost, redditURL string) string {
	buildTitle := func(titlePrefix string, isCut bool) string {
		return vxredditLinkText(post, titlePrefix, post.LinkURL, isCut, false)
	}
	buildLink := func(linkPrefix string, isCut bool) string {
		return vxredditLinkText(post, "", linkPrefix, false, isCut)
	}
	result := fitCaptionMessage(post.Title, redditURL, buildTitle)
	size := captionSize(result, redditURL)

	if size > TelegramCaptionLimitBytes {
		result = fitCaptionMessage(post.LinkURL, redditURL, buildLink)
	}

	return result
}

// vxredditLinkText formats author line, (escaped) title and link, each ended
// with the truncation marker when cut.
func vxredditLinkText(post vxredditPost, title, link string, isTitleCut, isLinkCut bool) string {
	parts := []string{}
	escapedTitle := vxredditCutPart(title, isTitleCut)
	escapedLink := vxredditCutPart(link, isLinkCut)

	if escapedTitle != "" {
		parts = append(parts, escapedTitle)
	}

	if escapedLink != "" {
		parts = append(parts, escapedLink)
	}

	text := strings.Join(parts, redditTextSeparator)
	result := FormatAuthorCaption(RedditPlatform, post.Subreddit, post.Author, text)

	return result
}

// vxredditCutPart escapes one caption part, ended with the truncation marker
// when it was cut ("" for an empty part).
func vxredditCutPart(part string, isCut bool) string {
	result := html.EscapeString(part)
	isMarked := (isCut == true) && (part != "")

	if isMarked == true {
		result = fmt.Sprintf("%s%s", result, redditTruncationMarker)
	}

	return result
}

// finishVxredditMedia sets the post URL and appends the original link.
func finishVxredditMedia(items []*MediaInfo, redditURL string) {
	for _, item := range items {
		item.URL = redditURL
	}

	appendOriginalURL(items, redditURL)
}

// mediaBasePath is the temp path of one item before it gets its extension.
func (e *RedditVxredditExtractor) mediaBasePath(timestamp int64, index int) string {
	filename := fmt.Sprintf("%s_%d_%d", vxredditFilePrefix, timestamp, (index + 1))
	result := filepath.Join(e.tempDir, filename)

	return result
}

// downloadItem downloads one photo or video (a GIF photo becomes an
// animation) and checks its type and size; a rejected file is removed.
// sendPhoto's limits are applied before sending (PreparePhotos).
func (e *RedditVxredditExtractor) downloadItem(ctx context.Context, mediaURL, basePath, wantType string) (*MediaInfo, error) {
	var result *MediaInfo
	request := mediaRequest{
		URL:      mediaURL,
		Headers:  map[string]string{"User-Agent": redirectorBotUserAgent},
		Client:   e.mediaHTTPClient,
		CheckURL: e.checkMediaURL,
		LimitFor: vxredditLimitFor,
	}
	download, err := downloadMediaCapped(ctx, request, basePath)
	mimeType := extractMimeType(download.ContentType)

	if err == nil {
		err = checkVxredditMedia(mimeType, download.Size, wantType)
	}

	if err != nil {
		os.Remove(basePath)
	}

	if err == nil {
		result = vxredditMediaItem(basePath, mimeType, download)
	}

	return result, err
}

// checkMediaURL accepts only an https URL on one of the media hosts, or on
// vxreddit's video renderer when the vxreddit host redirected there (fromURL
// is the previous hop, "" for the first request); it runs before the request
// and on every redirect.
func (e *RedditVxredditExtractor) checkMediaURL(mediaURL, fromURL string) error {
	var result error
	host, scheme := urlHostAndScheme(mediaURL)
	fromHost, _ := urlHostAndScheme(fromURL)
	isFromVxreddit := (fromHost != "") && (hostMatchesDomain(fromHost, e.vxredditHost) == true)
	isRenderer := (isFromVxreddit == true) && (hostMatchesDomain(host, vxredditRendererDomain) == true)
	isAllowedHost := (e.isMediaHost(host) == true) || (isRenderer == true)
	isMediaURL := (scheme == vxredditMediaScheme) && (isAllowedHost == true)

	if isMediaURL == false {
		result = fmt.Errorf("%w: %s", errVxredditOffCDN, mediaURL)
	}

	return result
}

// urlHostAndScheme returns the lowercase host and the scheme of a URL, empty
// for one that doesn't parse.
func urlHostAndScheme(rawURL string) (string, string) {
	host := ""
	scheme := ""
	parsed, err := url.Parse(rawURL)

	if err == nil {
		hostname := parsed.Hostname()
		host = strings.ToLower(hostname)
		scheme = parsed.Scheme
	}

	return host, scheme
}

// isMediaHost reports a host on (or under) the vxreddit host or one of
// Reddit's media hosts.
func (e *RedditVxredditExtractor) isMediaHost(host string) bool {
	result := (host != "") && (hostMatchesDomain(host, e.vxredditHost) == true)

	for _, mediaHost := range redditMediaHosts {
		result = result || hostMatchesDomain(host, mediaHost)
	}

	return result
}

// vxredditMediaItem names the downloaded file by its type: a GIF keeps .gif
// and is an animation.
func vxredditMediaItem(basePath, mimeType string, download mediaDownload) *MediaInfo {
	mediaType, ext := classifyRedirectorMedia(download.ContentType, download.FinalURL)
	isGIF := (mimeType == mimeTypeGIF)

	if isGIF == true {
		mediaType = MediaTypeAnimation
		ext = gifExtension
	}

	filePath := fmt.Sprintf("%s%s", basePath, ext)
	err := os.Rename(basePath, filePath)

	if err != nil {
		filePath = basePath
	}

	log.Printf("✅ [vxreddit] Downloaded %s (%d bytes)", mediaType, download.Size)
	result := &MediaInfo{Type: mediaType, Filename: filePath}

	return result
}

// vxredditLimitFor caps a download by type: GIFs may be converted to MP4,
// the rest must fit the upload limit (a photo above sendPhoto's limit is
// shrunk before sending, see PreparePhotos).
func vxredditLimitFor(mimeType string) int64 {
	result := TelegramUploadLimitBytes

	if mimeType == mimeTypeGIF {
		result = gifDownloadLimitBytes
	}

	return result
}

// checkVxredditMedia requires the wanted media type and a plausible size.
func checkVxredditMedia(mimeType string, size int64, wantType string) error {
	var result error
	isVideo := strings.HasPrefix(mimeType, "video/")
	isImage := strings.HasPrefix(mimeType, "image/")
	isWanted := ((MediaTypeVideo == wantType) && (isVideo == true)) || ((MediaTypePhoto == wantType) && (isImage == true))

	if isWanted == false {
		result = fmt.Errorf("expected %s from vxreddit, got content-type %q", wantType, mimeType)
	}

	if (result == nil) && (size < vxredditMinMediaSize) {
		result = fmt.Errorf("suspiciously small vxreddit media body (%d bytes)", size)
	}

	return result
}

// fetchPage retrieves the vxreddit page with the bot User-Agent.
func (e *RedditVxredditExtractor) fetchPage(ctx context.Context, pageURL string) (string, error) {
	headers := map[string]string{"User-Agent": redirectorBotUserAgent}
	result, err := fetchCappedPage(ctx, pageURL, headers, vxredditMaxPage, vxredditFetchTimeout)

	if err != nil {
		err = fmt.Errorf("vxreddit page failed: %w", err)
	}

	return result, err
}

// buildVxredditURL moves a Reddit URL (any subdomain, /s/ share links
// included) to the vxreddit host, keeping path and query.
func (e *RedditVxredditExtractor) buildVxredditURL(redditURL string) (string, error) {
	result := ""
	var base *url.URL
	parsed, err := url.Parse(redditURL)

	if err == nil {
		hostname := parsed.Hostname()
		host := strings.ToLower(hostname)
		err = checkRedditHost(host, redditURL)
	}

	if err == nil {
		base, err = url.Parse(e.base)
	}

	if err == nil {
		parsed.Scheme = base.Scheme
		parsed.Host = base.Host
		result = parsed.String()
	}

	return result, err
}

// checkRedditHost accepts reddit.com and its subdomains.
func checkRedditHost(host, redditURL string) error {
	var result error
	isReddit := hostMatchesDomain(host, "reddit.com")

	if isReddit == false {
		result = fmt.Errorf("%w: %s", errVxredditNotReddit, redditURL)
	}

	return result
}

// reportVxredditOutcome feeds the outage tracker: a page or media download
// failure is an outage signal (not the chain's deadline, not media refused
// off the media hosts), a success clears it.
func reportVxredditOutcome(ctx context.Context, err error) {
	isCancelled := (ctx.Err() != nil)
	isOffCDN := errors.Is(err, errVxredditOffCDN)

	if err == nil {
		ReportServiceSuccess(redditVxredditServiceName)
	}

	if (err != nil) && (isCancelled == false) && (isOffCDN == false) {
		message := err.Error()
		ReportServiceFailure(redditVxredditServiceName, message)
	}
}

// parseVxredditPage reads the og tags of a vxreddit page.
func parseVxredditPage(page, redditURL string) vxredditPost {
	result := vxredditPost{}
	description := ""
	matches := threadsMetaPattern.FindAllStringSubmatch(page, -1)

	for _, match := range matches {
		value := html.UnescapeString(match[2])
		result, description = applyVxredditTag(result, description, match[1], value)
	}

	result = applyVxredditDescription(result, description)
	result = applyVxredditSubreddit(result, redditURL)

	return result
}

// applyVxredditTag adds one meta tag to the post; og:description is returned
// apart, its meaning depends on the title.
func applyVxredditTag(post vxredditPost, description, name, value string) (vxredditPost, string) {
	result := post
	resultDescription := description
	isImage := (name == "og:image") && (value != "")
	isPreview := (isImage == true) && (isVxredditPreviewImage(value) == true)

	switch name {
	case "og:site_name":
		result.Author, result.Subreddit = vxredditSiteName(value)
		result.IsPost = (result.Subreddit != "")
	case "og:title":
		result.Title = value
	case "og:description":
		resultDescription = value
	case "og:video":
		result.VideoURL = value
	}

	if (isImage == true) && (isPreview == true) && (result.PreviewImage == "") {
		result.PreviewImage = value
	}

	if (isImage == true) && (isPreview == false) {
		result.Images = append(result.Images, value)
	}

	return result, resultDescription
}

// applyVxredditDescription reads og:description as the title (when og:title
// is the placeholder), the link of a link post, or the body.
func applyVxredditDescription(post vxredditPost, description string) vxredditPost {
	result := post
	trimmed := strings.TrimSpace(description)
	isPlaceholderTitle := (post.Title == vxredditPlaceholderTitle)
	isLink := (isPlaceholderTitle == false) && (vxredditLinkPattern.MatchString(trimmed) == true)
	isBody := (isPlaceholderTitle == false) && (isLink == false)

	if isPlaceholderTitle == true {
		result.Title = trimmed
	}

	if isLink == true {
		result.LinkURL = trimmed
	}

	if isBody == true {
		result.Body = description
	}

	return result
}

// applyVxredditSubreddit takes the subreddit from the URL when the page names
// none.
func applyVxredditSubreddit(post vxredditPost, redditURL string) vxredditPost {
	result := post

	if result.Subreddit == "" {
		result.Subreddit = firstSubmatch(vxredditSubredditPath, redditURL)
	}

	return result
}

// vxredditSiteName returns author and subreddit of og:site_name.
func vxredditSiteName(siteName string) (string, string) {
	author := ""
	subreddit := ""
	match := vxredditSiteNamePattern.FindStringSubmatch(siteName)

	if len(match) == 3 {
		author = match[1]
		subreddit = match[2]
	}

	return author, subreddit
}

// isVxredditPreviewImage reports a link-preview or video thumbnail: an image
// on the preview host.
func isVxredditPreviewImage(imageURL string) bool {
	host := ""
	parsed, err := url.Parse(imageURL)

	if err == nil {
		host = parsed.Hostname()
	}

	result := strings.EqualFold(host, vxredditPreviewHost)

	return result
}

// cleanRedditMarkdown removes Reddit's bold markers, escapes and heading
// markers from text.
func cleanRedditMarkdown(text string) string {
	withoutBold := strings.ReplaceAll(text, "**", "")
	unescaped := redditMarkdownReplacer.Replace(withoutBold)
	withoutHeadings := redditHeadingPattern.ReplaceAllString(unescaped, "")
	result := strings.TrimSpace(withoutHeadings)

	return result
}
