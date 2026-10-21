package services

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ThreadsExtractor downloads Threads posts (text, photos, videos, carousels).
// yt-dlp has no Threads extractor, so it reads Meta's server-rendered post
// embed, and falls back to the vxthreads mirror's og/twitter tags.
type ThreadsExtractor struct {
	tempDir  string
	pageBase string
	vxBase   string
}

const (
	// ThreadsPlatform is the platform key of Threads (captions, settings,
	// per-chat toggles, stats).
	ThreadsPlatform = "threads"

	// threadsPageBase is where post pages and embeds are fetched from.
	threadsPageBase = "https://www.threads.com"

	// threadsVxBase is the vxthreads mirror; it is also the fixed-link host.
	threadsVxBase = "https://vxthreads.com"

	// threadsEmbedUserAgent gets the server-rendered embed; a browser UA gets
	// an empty JavaScript app shell.
	threadsEmbedUserAgent = "curl/8.0"

	// threadsVxUserAgent makes vxthreads answer with og/twitter tags instead of
	// redirecting to threads.com.
	threadsVxUserAgent = "TelegramBot (like TwitterBot)"

	threadsFetchTimeout = 20 * time.Second
	threadsMaxPage      = 2 * 1024 * 1024

	// threadsMaxText caps a text-only post (Telegram allows 4096 characters
	// per message; the original link is appended).
	threadsMaxText = 3500

	threadsFilePrefix = "threads"
	threadsPhotoExt   = ".jpg"
	threadsVideoExt   = ".mp4"

	// Embed markers: the post's own block has the "Full" container (a reply's
	// parent post comes first without it); its media ends where a quoted post
	// or the date row starts.
	threadsBodyMarker  = `class="BodyTextContainer">`
	threadsQuoteMarker = `class="QuotePostContainer"`
	threadsDateMarker  = `class="PostDateContainer"`

	// threadsEmbedErrorMarker is Meta's "Thread not available" block, shown
	// for a deleted, private or mistyped post.
	threadsEmbedErrorMarker = `class="EmbedError"`
)

var (
	// threadsURLPattern matches post links on threads.com / threads.net.
	threadsURLPattern = regexp.MustCompile(`^https?://(www\.)?threads\.(com|net)/`)

	// threadsPostPathPattern captures "@user/post/<code>", "t/<code>" or
	// "share/<id>".
	threadsPostPathPattern = regexp.MustCompile(`threads\.(?:com|net)/((?:@[A-Za-z0-9._]+/post|t|share)/[A-Za-z0-9_-]+)`)

	// threadsMainPostPattern finds the post's own block in the embed.
	threadsMainPostPattern = regexp.MustCompile(`class="OuterContainer OuterContainerFull(?: OuterContainerFollowButton)?"`)

	// threadsAuthorPattern captures the author's username in the embed.
	threadsAuthorPattern = regexp.MustCompile(`class="HeaderLink"><span>([^<]+)</span>`)

	// threadsEmbedMediaPattern captures post photos (t51.*-15; -19 is the
	// avatar) and video sources, in post order.
	threadsEmbedMediaPattern = regexp.MustCompile(`<img[^>]*src="([^"]*t51\.\d+-15[^"]*)"|<source[^>]*src="([^"]+)"`)

	// threadsAvatarPattern matches profile pictures (t51.*-19), which the
	// mirror shows as og:image for text-only posts.
	threadsAvatarPattern = regexp.MustCompile(`t51\.\d+-19`)

	threadsBreakPattern = regexp.MustCompile(`<br\s*/?>`)
	threadsTagPattern   = regexp.MustCompile(`<[^>]+>`)

	// threadsMetaPattern captures og:/twitter: meta tags of the vxthreads page.
	threadsMetaPattern = regexp.MustCompile(`<meta (?:property|name)="((?:og|twitter):[a-z:_]+)" content="([^"]*)"`)

	// threadsLoginURLPattern matches the login page vxthreads shows in place
	// of a post Threads won't give it.
	threadsLoginURLPattern = regexp.MustCompile(`^https?://(www\.)?threads\.(com|net)/login`)
)

// Parse errors of a Threads page: an unavailable or quote-only post is not
// counted as an outage; an empty embed or a login wall on the mirror is.
var (
	errThreadsPostUnavailable = errors.New("the Threads post is not available (deleted, private or mistyped)")
	errThreadsPostEmpty       = errors.New("no text or media in the Threads embed")
	errThreadsPostQuoteOnly   = errors.New("the Threads post only quotes another post")
	errThreadsVxLoginPage     = errors.New("vxthreads answered with the Threads login page")
)

// Outage-tracker keys of the two Threads sources.
const (
	threadsEmbedServiceName = "threads-embed"
	vxthreadsServiceName    = "vxthreads"
)

// threadsCall is one page read of a Threads source and the code its parse
// failures carry.
type threadsCall struct {
	ctxErr    error
	fetchErr  error
	parseErr  error
	parseCode string
}

// classifyThreadsCall tells the tracker about one page read; an unavailable
// post, a 4xx other than 429 and the caller's deadline are not counted.
func classifyThreadsCall(call threadsCall) (serviceOutcome, string) {
	outcome := serviceOutcomeSuccess
	code := ""
	var statusErr *httpStatusError
	isFetchFailed := (call.fetchErr != nil)
	isCallerDone := (isFetchFailed == true) && (call.ctxErr != nil)
	isStatus := (isFetchFailed == true) && (errors.As(call.fetchErr, &statusErr) == true)
	isFetchCounted := (isFetchFailed == true) && (isCallerDone == false)
	isPostMissing := errors.Is(call.parseErr, errThreadsPostUnavailable) || errors.Is(call.parseErr, errThreadsPostQuoteOnly)
	isParseCounted := (isFetchFailed == false) && (call.parseErr != nil) && (isPostMissing == false)

	if (isFetchCounted == true) && (isStatus == true) {
		outcome, code = classifyServiceAnswerStatus(statusErr.StatusCode)
	}

	if (isFetchCounted == true) && (isStatus == false) {
		outcome = serviceOutcomeFailure
		code = classifyServiceErr(call.fetchErr)
	}

	if isCallerDone == true {
		outcome = serviceOutcomeNone
	}

	if isParseCounted == true {
		outcome = serviceOutcomeFailure
		code = call.parseCode
	}

	if (isFetchFailed == false) && (isPostMissing == true) {
		outcome = serviceOutcomeNone
	}

	return outcome, code
}

// reportThreadsCall feeds one page read into the outage tracker under
// service; err is the alert's detail.
func reportThreadsCall(service string, call threadsCall, err error) {
	outcome, code := classifyThreadsCall(call)
	detail := fmt.Sprintf("%v", err)
	reportServiceOutcome(service, outcome, code, detail)
}

// threadsPost is a parsed Threads post; Poster is the vxthreads twitter:image
// (a video's poster).
type threadsPost struct {
	Author  string
	Text    string
	Media   []threadsMedia
	Poster  string
	PageURL string
}

// threadsMedia is one photo or video of a post.
type threadsMedia struct {
	Type string
	URL  string
}

// NewThreadsExtractor creates a Threads extractor.
func NewThreadsExtractor(tempDir string) *ThreadsExtractor {
	return &ThreadsExtractor{tempDir: tempDir, pageBase: threadsPageBase, vxBase: threadsVxBase}
}

// IsThreadsURL reports whether a URL is a Threads link (by host, so a
// username like "@fox.com" can't be mistaken for another platform).
func IsThreadsURL(rawURL string) bool {
	result := threadsURLPattern.MatchString(rawURL)

	return result
}

// BuildThreadsFixedURL returns the vxthreads link of a post, or "".
func BuildThreadsFixedURL(rawURL string) string {
	result := ""
	postPath := threadsPostPath(rawURL)

	if postPath != "" {
		result = fmt.Sprintf("%s/%s", threadsVxBase, postPath)
	}

	return result
}

// threadsPostPath returns "@user/post/<code>", "t/<code>" or "share/<id>" of
// a post URL.
func threadsPostPath(rawURL string) string {
	result := ""
	matches := threadsPostPathPattern.FindStringSubmatch(rawURL)

	if len(matches) == 2 {
		result = matches[1]
	}

	return result
}

// ExtractEmbed reads the post from Meta's server-rendered embed.
func (e *ThreadsExtractor) ExtractEmbed(ctx context.Context, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var post threadsPost
	var page string
	call := threadsCall{parseCode: ServiceErrorParse}
	postPath, err := e.resolvePostPath(ctx, postURL)
	isResolved := (err == nil)

	if err == nil {
		embedURL := fmt.Sprintf("%s/%s/embed", e.pageBase, postPath)
		page, call.fetchErr = e.fetch(ctx, embedURL, threadsEmbedUserAgent)
		err = call.fetchErr
	}

	if err == nil {
		post, call.parseErr = parseThreadsEmbed(page)
		err = call.parseErr
	}

	if isResolved == true {
		call.ctxErr = ctx.Err()
		reportThreadsCall(threadsEmbedServiceName, call, err)
	}

	if err == nil {
		result, err = e.buildMedia(ctx, post, postURL)
	}

	return result, err
}

// ExtractVxthreads reads the post from the vxthreads mirror's meta tags. It
// is a fallback: a mixed carousel there exposes only one video and image.
func (e *ThreadsExtractor) ExtractVxthreads(ctx context.Context, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var post threadsPost
	var page string
	// Short /t/ and /share/ links are resolved first (the mirror shows only
	// one item of a carousel for them); if threads.com can't resolve one, the
	// mirror gets the link as is (it serves both forms).
	postPath, err := e.resolvePostPath(ctx, postURL)
	shortPath := threadsPostPath(postURL)

	if (err != nil) && (isThreadsShortPath(shortPath) == true) {
		postPath = shortPath
		err = nil
	}

	call := threadsCall{parseCode: ServiceErrorWrongContent}
	isResolved := (err == nil)

	if err == nil {
		vxURL := fmt.Sprintf("%s/%s", e.vxBase, postPath)
		page, call.fetchErr = e.fetch(ctx, vxURL, threadsVxUserAgent)
		err = call.fetchErr
	}

	if err == nil {
		post, call.parseErr = parseThreadsVxPage(page)
		err = call.parseErr
	}

	if isResolved == true {
		call.ctxErr = ctx.Err()
		reportThreadsCall(vxthreadsServiceName, call, err)
	}

	if err == nil {
		result, err = e.buildMedia(ctx, post, postURL)
	}

	return result, err
}

// resolvePostPath returns the "@user/post/<code>" path; a short "/t/<code>"
// or "/share/<id>" link is resolved through its redirect.
func (e *ThreadsExtractor) resolvePostPath(ctx context.Context, postURL string) (string, error) {
	result := threadsPostPath(postURL)
	err := fmt.Errorf("not a Threads post URL: %s", postURL)
	isShort := isThreadsShortPath(result)

	if (result != "") && (isShort == false) {
		err = nil
	}

	if isShort == true {
		result, err = e.resolveShortPath(ctx, result)
	}

	return result, err
}

// resolveShortPath follows the redirect of a "/t/<code>" or "/share/<id>"
// link once (Threads redirects both straight to the post).
func (e *ThreadsExtractor) resolveShortPath(ctx context.Context, shortPath string) (string, error) {
	result := ""
	var resp *http.Response
	requestCtx, cancel := context.WithTimeout(ctx, threadsFetchTimeout)

	defer cancel()

	shortURL := fmt.Sprintf("%s/%s", e.pageBase, shortPath)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, shortURL, nil)
	client := &http.Client{Transport: SharedHTTPClient.Transport, CheckRedirect: stopAtFirstRedirect}

	if err == nil {
		req.Header.Set("User-Agent", threadsEmbedUserAgent)
		resp, err = client.Do(req)
	}

	if err == nil {
		defer resp.Body.Close()

		location := resp.Header.Get("Location")
		result = threadsRedirectPostPath(location)
	}

	if (err == nil) && ((result == "") || (isThreadsShortPath(result) == true)) {
		err = fmt.Errorf("short Threads link did not resolve: %s", shortPath)
	}

	return result, err
}

// threadsRedirectPostPath turns a redirect Location (absolute or relative)
// into the post path, matched on the URL path only (not a query such as a
// login page's next= parameter).
func threadsRedirectPostPath(location string) string {
	result := ""
	parsed, err := url.Parse(location)

	if err == nil {
		pathURL := fmt.Sprintf("threads.com%s", parsed.Path)
		result = threadsPostPath(pathURL)
	}

	return result
}

// isThreadsShortPath reports a link that redirects to the post: the short
// "/t/<code>" and the app's "/share/<id>" links.
func isThreadsShortPath(postPath string) bool {
	isShortLink := strings.HasPrefix(postPath, "t/")
	isShareLink := strings.HasPrefix(postPath, "share/")
	result := (isShortLink == true) || (isShareLink == true)

	return result
}

// stopAtFirstRedirect keeps the redirect response so its Location is read.
func stopAtFirstRedirect(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}

// fetch downloads a page with a User-Agent, capped in size.
func (e *ThreadsExtractor) fetch(ctx context.Context, pageURL, userAgent string) (string, error) {
	headers := map[string]string{"User-Agent": userAgent}
	result, err := fetchCappedPage(ctx, pageURL, headers, threadsMaxPage, threadsFetchTimeout)

	return result, err
}

// parseThreadsEmbed reads author, text and media of the post's own block.
func parseThreadsEmbed(page string) (threadsPost, error) {
	result := threadsPost{}
	err := fmt.Errorf("no Threads post in the embed (login wall or changed format)")
	location := threadsMainPostPattern.FindStringIndex(page)
	isUnavailable := (location == nil) && (strings.Contains(page, threadsEmbedErrorMarker) == true)

	if isUnavailable == true {
		err = errThreadsPostUnavailable
	}

	if location != nil {
		section := page[location[0]:]
		result = parseThreadsSection(section)
		err = nil
	}

	if (err == nil) && (result.Author == "") {
		err = fmt.Errorf("no author in the Threads embed")
	}

	// Neither text nor media: the next method tries instead of sending only
	// the author.
	isEmpty := (err == nil) && (result.Text == "") && (len(result.Media) == 0)

	if isEmpty == true {
		err = threadsEmptyPostError(page)
	}

	return result, err
}

// threadsEmptyPostError tells a quote-only post from an embed whose markup
// changed.
func threadsEmptyPostError(page string) error {
	result := errThreadsPostEmpty
	isQuoteOnly := strings.Contains(page, threadsQuoteMarker)

	if isQuoteOnly == true {
		result = errThreadsPostQuoteOnly
	}

	return result
}

// parseThreadsSection reads the post block: author, body text, then media up
// to a quoted post or the date row (quoted media is not the post's).
func parseThreadsSection(section string) threadsPost {
	result := threadsPost{}
	authorMatch := threadsAuthorPattern.FindStringSubmatch(section)
	// Text and media both end at a quoted post: a post without text of its
	// own must not take the quoted post's text.
	mediaRegion := threadsMediaRegion(section)
	bodyStart := strings.Index(mediaRegion, threadsBodyMarker)

	if len(authorMatch) == 2 {
		result.Author = html.UnescapeString(authorMatch[1])
	}

	if bodyStart >= 0 {
		bodyHTML := mediaRegion[(bodyStart + len(threadsBodyMarker)):]
		result.Text = threadsBodyText(bodyHTML)
	}

	result.Media = threadsEmbedMedia(mediaRegion)

	return result
}

// threadsBodyText turns the body's spans into plain text (up to the first
// block element).
func threadsBodyText(body string) string {
	end := strings.Index(body, "<div")
	raw := body

	if end >= 0 {
		raw = body[:end]
	}

	withBreaks := threadsBreakPattern.ReplaceAllString(raw, "\n")
	withoutTags := threadsTagPattern.ReplaceAllString(withBreaks, "")
	unescaped := html.UnescapeString(withoutTags)
	result := strings.TrimSpace(unescaped)

	return result
}

// threadsMediaRegion cuts the section before a quoted post or the date row.
func threadsMediaRegion(section string) string {
	result := section

	for _, marker := range []string{threadsQuoteMarker, threadsDateMarker} {
		end := strings.Index(result, marker)

		if end >= 0 {
			result = result[:end]
		}
	}

	return result
}

// threadsEmbedMedia lists photos and videos in post order.
func threadsEmbedMedia(region string) []threadsMedia {
	var result []threadsMedia

	matches := threadsEmbedMediaPattern.FindAllStringSubmatch(region, -1)

	for _, match := range matches {
		photoURL := html.UnescapeString(match[1])
		videoURL := html.UnescapeString(match[2])
		item := threadsMedia{Type: MediaTypePhoto, URL: photoURL}

		if match[2] != "" {
			item = threadsMedia{Type: MediaTypeVideo, URL: videoURL}
		}

		result = append(result, item)
	}

	return result
}

// parseThreadsVxPage reads author, text and media from the vxthreads meta
// tags, refusing a login page in place of the post.
func parseThreadsVxPage(page string) (threadsPost, error) {
	result := threadsPost{}
	var err error

	matches := threadsMetaPattern.FindAllStringSubmatch(page, -1)

	for _, match := range matches {
		value := html.UnescapeString(match[2])
		result = applyThreadsVxTag(result, match[1], value)
	}

	if result.Author == "" {
		err = fmt.Errorf("no Threads post on the vxthreads page")
	}

	if threadsLoginURLPattern.MatchString(result.PageURL) == true {
		err = errThreadsVxLoginPage
	}

	result.Media = dropThreadsPoster(result.Media, result.Poster)

	return result, err
}

// dropThreadsPoster removes the video's poster from the photos: when the
// post has a video, the og:image equal to twitter:image is its poster, not
// another photo.
func dropThreadsPoster(media []threadsMedia, poster string) []threadsMedia {
	var result []threadsMedia
	hasVideo := false

	for _, item := range media {
		hasVideo = hasVideo || (item.Type == MediaTypeVideo)
	}

	for _, item := range media {
		isPoster := (hasVideo == true) && (item.Type == MediaTypePhoto) && (item.URL == poster)

		if isPoster == false {
			result = append(result, item)
		}
	}

	return result
}

// applyThreadsVxTag adds one meta tag to the post.
func applyThreadsVxTag(post threadsPost, name, value string) threadsPost {
	result := post
	isNewVideo := (name == "og:video:secure_url") && (value != "")
	isAvatar := threadsAvatarPattern.MatchString(value)
	isNewImage := (name == "og:image") && (value != "") && (isAvatar == false)

	if name == "twitter:creator" {
		result.Author = strings.TrimPrefix(value, "@")
	}

	if name == "og:description" {
		result.Text = value
	}

	if name == "twitter:image" {
		result.Poster = value
	}

	if name == "og:url" {
		result.PageURL = value
	}

	if isNewVideo == true {
		result.Media = append(result.Media, threadsMedia{Type: MediaTypeVideo, URL: value})
	}

	if isNewImage == true {
		result.Media = append(result.Media, threadsMedia{Type: MediaTypePhoto, URL: value})
	}

	return result
}

// buildMedia downloads the post's media (the first item carries the caption)
// or returns a text-only post as one text item.
func (e *ThreadsExtractor) buildMedia(ctx context.Context, post threadsPost, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error

	if len(post.Media) == 0 {
		caption := threadsCaption(post, threadsMaxText, postURL)
		result = []*MediaInfo{{Type: MediaTypeText, URL: postURL, Caption: caption}}
	}

	if len(post.Media) > 0 {
		result, err = e.downloadMedia(ctx, post, postURL)
	}

	return result, err
}

// downloadMedia downloads every item; the loop stops at the chain's deadline.
func (e *ThreadsExtractor) downloadMedia(ctx context.Context, post threadsPost, postURL string) ([]*MediaInfo, error) {
	var result []*MediaInfo
	var err error
	timestamp := time.Now().UnixNano()

	for i, item := range post.Media {
		mediaInfo, itemErr := e.downloadItem(ctx, item, timestamp, i)

		if itemErr == nil {
			mediaInfo.URL = postURL
			result = append(result, mediaInfo)
		}
	}

	if len(result) == 0 {
		err = fmt.Errorf("no Threads media could be downloaded for %s", postURL)
	}

	if err == nil {
		result[0].Caption = threadsCaption(post, 0, postURL)
		appendOriginalURL(result, postURL)
	}

	return result, err
}

// downloadItem downloads one photo or video, within Telegram's upload limit.
func (e *ThreadsExtractor) downloadItem(ctx context.Context, item threadsMedia, timestamp int64, index int) (*MediaInfo, error) {
	var result *MediaInfo
	ext := threadsPhotoExt

	if item.Type == MediaTypeVideo {
		ext = threadsVideoExt
	}

	filename := fmt.Sprintf("%s_%d_%d%s", threadsFilePrefix, timestamp, (index + 1), ext)
	filePath := filepath.Join(e.tempDir, filename)
	err := ctx.Err()

	if err == nil {
		err = downloadToFile(item.URL, filePath)
	}

	if err == nil {
		err = checkUploadSize(filePath)
	}

	if err != nil {
		os.Remove(filePath)
	}

	if err == nil {
		result = &MediaInfo{Type: item.Type, Filename: filePath}
	}

	return result, err
}

// threadsCaption formats author and text (escaped for HTML), the text
// fitted into maxText bytes or, when maxText is 0, into what the media
// caption limit leaves next to the author line and the original link.
func threadsCaption(post threadsPost, maxText int, postURL string) string {
	caption := authorCaption{Platform: ThreadsPlatform, Username: post.Author, Text: post.Text, PostURL: postURL, MaxText: maxText}
	result := buildAuthorCaption(caption)

	return result
}
